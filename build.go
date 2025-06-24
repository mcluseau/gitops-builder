package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type BuildRun struct {
	app    App
	build  Build
	branch *BranchInfo
	log    *log.Logger
}

func (b *BuildRun) Run() (err error) {
	// connect to docker
	docker, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// setup build infos

	app, build, branchInfo := b.app, b.build, b.branch

	buildID := newUlid()

	notifPrefix := fmt.Sprint("[", buildID, "]("+*builderURL+"/build-logs/"+buildID+") running ", app.Name, "/", build.Source, " (branch ", branchInfo.Source, ")")

	defer func() {
		if err != nil {
			notify(notifPrefix + " failed: " + err.Error())
		} else {
			notify(notifPrefix + " successful")
		}
	}()

	var logOut *os.File
	{
		logsDir := filepath.Join(*workDir, "logs")
		os.MkdirAll(logsDir, 0750)
		logFile := filepath.Join(logsDir, buildID+".log")

		logOut, err = os.Create(logFile)
		if err != nil {
			log.Print("failed to create log file: ", err)
			return
		}
		defer logOut.Close()
	}
	out := io.MultiWriter(logOut, os.Stdout)
	log := log.New(out, "", log.Ldate|log.Ltime|log.LUTC)
	b.log = log

	g := gitOps{log}

	branch := branchInfo.Source

	// update app & deploy
	appDir := filepath.Join(*workDir, app.Name)

	// build
	baseDir := filepath.Join(appDir, "builds", build.Source)

	srcDir := filepath.Join(baseDir, "src")
	if err = g.FetchBranch(build.Source, branch, srcDir); err != nil {
		err = fmt.Errorf("failed to fetch source: %w", err)
		return
	}

	// copy overlay to source
	overlayDir := ""
	if build.Overlay != "" {
		overlayDir = filepath.Join(baseDir, "overlay")

		if err = g.FetchBranch(build.Overlay, branchInfo.Overlay, overlayDir); err != nil {
			err = fmt.Errorf("failed to fetch overlay: %w", err)
			return
		}

		log.Print("- copying overlay from ", overlayDir)
		err = filepath.Walk(overlayDir, func(srcPath string, info os.FileInfo, inErr error) (err error) {
			err = inErr
			if err != nil {
				return
			}
			if info.IsDir() {
				return
			}

			path, err := filepath.Rel(overlayDir, srcPath)
			if err != nil {
				return
			}

			if strings.HasPrefix(path, ".git") {
				return
			}
			if filepath.Base(path) == ".gitignore" {
				return
			}

			targetPath := filepath.Join(srcDir, path)
			log.Printf("  - overlay copy: %s (mode: %04o)", path, info.Mode())

			os.MkdirAll(filepath.Dir(targetPath), 0755)
			err = func() (err error) {
				in, err := os.Open(srcPath)
				if err != nil {
					return
				}
				defer in.Close()

				out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
				if err != nil {
					return
				}
				defer out.Close()

				_, err = io.Copy(out, in)
				if err != nil {
					os.Remove(targetPath)
				}
				return
			}()

			return
		})

		if err != nil {
			return
		}
	}

	// run the build
	var srcTag, imageTag string

	if *tagDescribe {
		srcTag, err = g.Describe(srcDir, branchInfo.Source)
	} else {
		srcTag, err = g.Tag(srcDir, branchInfo.Source)
	}
	if err != nil {
		err = fmt.Errorf("failed to get source tag: %w", err)
		return
	}

	overlayTag := ""
	if overlayDir == "" {
		imageTag = srcTag + branchInfo.DockerTagSuffix

	} else {
		if *tagDescribe {
			overlayTag, err = g.Describe(overlayDir, branchInfo.Overlay)
		} else {
			overlayTag, err = g.Tag(overlayDir, branchInfo.Overlay)
		}
		if err != nil {
			err = fmt.Errorf("failed to get overlay tag: %w", err)
			return
		}
		imageTag = srcTag + "_overlay." + overlayTag + branchInfo.DockerTagSuffix
	}

	dockerImage := build.Docker
	if dockerImage == "" {
		dockerImage = build.Source
	}
	dockerImageName := dockerPrefix + dockerImage
	dockerImage = dockerImageName + ":" + imageTag

	// build-args caching is crap so at least check if we already build the target image
	if _, _, inspectErr := docker.ImageInspectWithRaw(ctx, dockerImage); inspectErr == nil {
		log.Print("image ", dockerImage, " already exists, not rebuilding.")
	} else {
		dockerArgs := []string{"build", "-t", dockerImage, ".",
			"--network=host", // we don't really want the network isolation overload
			"--build-arg=GIT_TAG=" + srcTag,
			"--build-arg=IMAGE_TAG=" + imageTag,
		}

		if sshAuthSock := os.Getenv("SSH_AUTH_SOCK"); sshAuthSock != "" {
			dockerArgs = append(dockerArgs, "--ssh=default="+sshAuthSock)
		}

		if overlayTag != "" {
			dockerArgs = append(dockerArgs, "--build-arg", "OVERLAY_TAG="+overlayTag)
		}

		for _, args := range [][]string{dockerArgs, app.DockerArgs, build.DockerArgs, branchInfo.DockerArgs} {
			for _, arg := range args {
				dockerArgs = append(dockerArgs, "--build-arg", arg)
			}
		}

		err = execCmd(log, srcDir, "docker", dockerArgs...)
		if err != nil {
			return
		}
	}

	err = pushImage(log, appDir, dockerImage)
	if err != nil {
		return
	}

	// cleanup old images (keep latest N images)
	type ImageTag struct {
		Tag     string
		ImageID string
		Created int64
	}
	myImages := make([]ImageTag, 0)

	allImages, err := docker.ImageList(ctx, image.ListOptions{All: true})
	for _, img := range allImages {
		for _, tag := range img.RepoTags {
			if !strings.HasPrefix(tag, dockerImage+":") {
				continue
			}
			myImages = append(myImages, ImageTag{
				Tag:     tag,
				ImageID: img.ID,
				Created: img.Created,
			})
		}
	}

	// sort by created
	sort.Slice(myImages, func(i, j int) bool {
		ti, tj := myImages[i], myImages[j]
		return ti.Created < tj.Created
	})

	if len(myImages) > 5 {
		for _, imageTag := range myImages[5:] {
			docker.ImageRemove(ctx, imageTag.Tag, image.RemoveOptions{
				PruneChildren: true,
			})
		}
	}

	// update the deployment
	deployDir := filepath.Join(appDir, "deploy")
	if err = g.FetchBranch(app.Deploy, branchInfo.Deploy, deployDir); err != nil {
		return
	}

	log.Print("- updating deployment repository")
	for idx, deployUpdate := range build.DeployUpdates {
		log.Print("  - step ", idx+1)
		if deployUpdate.Script != "" {
			absDeployDir, absErr := filepath.Abs(deployDir)
			if absErr != nil {
				err = fmt.Errorf("failed to get absolute deploy path: %w", absErr)
				return
			}

			cmd := exec.Command("docker", "run", "--rm",
				"-v", absDeployDir+":/work", "-w", "/work",
				"--entrypoint", "/bin/ash",
				"alpine:3.18", // FIXME allow configuration of this
				"-c", deployUpdate.Script)
			cmd.Dir = deployDir
			cmd.Stdout = log.Writer()
			cmd.Stderr = log.Writer()

			cmd.Env = append(os.Environ(), "IMAGE_TAG="+imageTag)

			err = cmd.Run()
			if err != nil {
				return
			}
		}

		if set := deployUpdate.YamlSet; set != nil {
			set := *set

			filePath := filepath.Join(deployDir, set.File)

			origValue := set.Value
			set.Value = strings.ReplaceAll(origValue, "${IMAGE_TAG}", imageTag)

			log.Printf("    - yaml set %s:%s to %q (%q)", set.File, set.Path, set.Value, origValue)

			var in, out []byte

			in, err = os.ReadFile(filePath)
			if err != nil {
				return
			}

			out, err = set.Apply(in)
			if err != nil {
				return
			}

			err = os.WriteFile(filePath, out, 0600)
			if err != nil {
				return
			}
		}
	}

	deploy, err := g.Open(deployDir+".git", deployDir)
	if err != nil {
		err = fmt.Errorf("failed to open deploy dir: %w", err)
		return
	}

	wt, err := deploy.Worktree()
	if err != nil {
		err = fmt.Errorf("failed get deploy worktree: %w", err)
		return
	}

	status, err := wt.Status()
	if err != nil {
		err = fmt.Errorf("failed to get deploy status: %w", err)
		return
	}

	if len(status) == 0 {
		log.Print("  `-> no changes made")
		return
	}

	log.Printf("  %d changes:", len(status))
	for f, st := range status {
		log.Print("  - ", string([]byte{byte(st.Worktree)}), " ", f)
		_, err = wt.Add(f)
		if err != nil {
			err = fmt.Errorf("failed to add change: %w", err)
			return
		}
	}

	commit, err := wt.Commit("auto-commit: app "+app.Name+": "+build.Source+": image tag "+imageTag,
		&git.CommitOptions{
			Author: &object.Signature{
				Name: "builder",
				When: time.Now(),
			},
		})
	if err != nil {
		err = fmt.Errorf("failed to commit on deploy: %w", err)
		return
	}

	log.Print("- deploy commit: ", commit)

	opts := git.PushOptions{
		RemoteName: "origin",
		Auth:       gitAuth,
		RefSpecs:   []config.RefSpec{config.RefSpec(branchInfo.Deploy + ":" + branchInfo.Deploy)},
	}

	log.Print("- git push ", opts.RemoteName, " ", opts.RefSpecs[0])

	if false {
		err = deploy.Push(&opts) // FIXME does not work (or a least not like git push)
	} else {
		cmd := exec.Command("git", "push", opts.RemoteName, string(opts.RefSpecs[0]))
		cmd.Stderr = os.Stderr
		cmd.Dir = deployDir + ".git"
		err = cmd.Run()
	}

	return
}
