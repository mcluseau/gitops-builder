package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/spf13/pflag"
)

var (
	workDir       = pflag.String("work-dir", "work", "work directory")
	triggerGit    = pflag.String("trigger-git", "", "trigger git")
	triggerBranch = pflag.String("trigger-branch", "main", "trigger branch")
	slackHook     = pflag.String("slack-hook", "", "Slack notification hook")
)

func main() {
	var err error

	log.SetFlags(log.Flags() | log.Lshortfile | log.Lmsgprefix)

	bind := pflag.String("bind", ":80", "HTTP bind for triggers")
	pflag.Parse()

	if appsRepo.Repo == "" {
		log.Fatal("--apps-repo is mandatory")
	}

	if gitAuth == nil {
		log.Print("WARNING: no git authentication defined (env GIT_TOKEN or GIT_USER and GIT_PASSWORD)")
	}

	updateApps()

	err = os.MkdirAll(*workDir, 0750)
	fail(err)

	if *triggerGit != "" {
		// single trigger run mode
		triggerFromURL(*triggerGit, *triggerBranch)
		return
	}

	setupHTTP()

	log.Print("listening on ", *bind)
	err = http.ListenAndServe(*bind, nil)
	if err != nil {
		log.Fatal(err)
	}
}

func fail(err error) {
	if err != nil {
		log.Output(2, err.Error())
		os.Exit(-1)
	}
}

var globalLock = sync.Mutex{}

func triggerFromURL(u, branch string) (ok bool) {
	log.Print("trigger from URL: ", u)

	repo, ok := cutAllowedPrefix(u)
	if !ok {
		log.Printf("trigger ignored: prefix not allowed")
		return
	}

	repo = strings.TrimSuffix(repo, ".git")

	log.Print("trigger: ", repo, " branch ", branch)
	triggerFrom(repo, branch)
	return true
}

func triggerFrom(repo, branch string) {
	globalLock.Lock()
	defer globalLock.Unlock()

	if repo == appsRepo.Repo && branch == appsRepo.Branch {
		updateApps()
	}

	config := currentProject

	for _, app := range config.apps {
		for _, build := range app.Builds {
			branches := make([]*BranchInfo, 0)

			switch repo {
			case build.Source:
				for _, b := range build.Branches {
					if b.Source == branch {
						log.Print("- matched build ", app.Name, " repo ", build.Source, ", branch ", branch)
						log.Print("  - matched branch ", branch)
						branches = append(branches, b)
					}
				}

			case build.Overlay:
				for _, b := range build.Branches {
					if b.Overlay == branch {
						log.Print("- matched build ", app.Name, " repo ", build.Source,
							" via overlay (", build.Overlay, "), branch ", branch)
						branches = append(branches, b)
					}
				}
			}

			if len(branches) == 0 {
				continue
			}

			for _, branch := range branches {
				runBuild(app, build, branch)
			}
		}
	}
}

func runBuild(app App, build Build, branchInfo *BranchInfo) (err error) {
	buildID := newUlid()

	notifPrefix := fmt.Sprint("[", buildID, "] running ", app.Name, "/", build.Source, " (branch ", branchInfo.Source, ")")

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
	//bLog := log.New(out, "", log.Ldate|log.Ltime|log.LUTC)

	branch := branchInfo.Source

	// update app & deploy
	appDir := filepath.Join(*workDir, app.Name)

	// build
	baseDir := filepath.Join(appDir, "builds", build.Source)

	srcDir := filepath.Join(baseDir, "src")
	if err = gitFetchBranch(build.Source, branch, srcDir); err != nil {
		err = fmt.Errorf("failed to fetch source: %w", err)
		return
	}

	// copy overlay to source
	overlayDir := ""
	if build.Overlay != "" {
		overlayDir = filepath.Join(baseDir, "overlay")

		if err = gitFetchBranch(build.Overlay, branchInfo.Overlay, overlayDir); err != nil {
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
	var imageTag string

	srcTag, err := gitTag(srcDir, branchInfo.Source)
	if err != nil {
		err = fmt.Errorf("failed to get source tag: %w", err)
		return
	}

	overlayTag := ""
	if overlayDir == "" {
		imageTag = srcTag + branchInfo.DockerTagSuffix

	} else {
		overlayTag, err = gitTag(overlayDir, branchInfo.Overlay)
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
	dockerImage = dockerPrefix + dockerImage + ":" + imageTag

	// build-args caching is crap so at least check if we already build the target image
	if inspectErr := execCmd(srcDir, "docker", "inspect", dockerImage, "-f", "ok"); inspectErr != nil {
		dockerArgs := []string{"build", "-t", dockerImage, ".",
			"--build-arg=GIT_TAG=" + srcTag,
			"--build-arg=IMAGE_TAG=" + imageTag,
		}

		if sshAuthSock := os.Getenv("SSH_AUTH_SOCK"); sshAuthSock != "" {
			dockerArgs = append(dockerArgs, "--ssh=default="+sshAuthSock)
		}

		if overlayTag != "" {
			dockerArgs = append(dockerArgs, "--build-arg", "OVERLAY_TAG="+overlayTag)
		}

		for _, args := range [][]string{app.DockerArgs, build.DockerArgs, branchInfo.DockerArgs} {
			for _, arg := range args {
				dockerArgs = append(dockerArgs, "--build-arg", arg)
			}
		}

		err = execCmdWithOutput(out, srcDir, "docker", dockerArgs...)
		if err != nil {
			return
		}
	}

	err = execCmd(srcDir, "docker", "push", dockerImage)

	if err != nil {
		return
	}

	deployDir := filepath.Join(appDir, "deploy")
	if err = gitFetchBranch(app.Deploy, branchInfo.Deploy, deployDir); err != nil {
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
				"alpine:3.17", // FIXME allow configuration of this
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
			filePath := filepath.Join(deployDir, set.File)

			set.Value = strings.ReplaceAll(set.Value, "${IMAGE_TAG}", imageTag)

			log.Printf("    - yaml set %s:%s to %q", set.File, set.Path, set.Value)

			var in, out []byte

			in, err = ioutil.ReadFile(filePath)
			if err != nil {
				return
			}

			out, err = set.Apply(in)
			if err != nil {
				return
			}

			err = ioutil.WriteFile(filePath, out, 0600)
			if err != nil {
				return
			}
		}
	}

	deploy, err := gitOpen(deployDir+".git", deployDir)
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

	branchRef := plumbing.NewBranchReferenceName(branchInfo.Deploy)

	err = deploy.Push(&git.PushOptions{
		RemoteName: "origin",
		Auth:       gitAuth,
		RefSpecs:   []config.RefSpec{config.RefSpec(branchRef + ":" + branchRef)},
	})

	return
}

func execCmd(wd, bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir = wd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Print("  ", wd, "$ ", bin, " ", args)
	return cmd.Run()
}

func execCmdWithOutput(out io.Writer, wd, bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir = wd
	cmd.Stdout = out
	cmd.Stderr = out

	log.Print("  ", wd, "$ ", bin, " ", args)
	return cmd.Run()
}

func gitFetchBranch(repoURL, branch, targetDir string) (err error) {
	repoURL = gitURL(repoURL)

	dir := targetDir + ".git"

	log.Print("- fetching ", repoURL, " branch ", branch, " to ", dir)

retry:
	isFresh := true
	repo, err := git.PlainClone(dir, true, &git.CloneOptions{
		URL:  repoURL,
		Auth: gitAuth,
	})

	if err == git.ErrRepositoryAlreadyExists {
		isFresh = false
		repo, err = git.PlainOpen(dir)
		if err != nil {
			err = fmt.Errorf("failed to open existing repository: %w", err)
			return
		}

	} else if err != nil {
		err = fmt.Errorf("failed to clone %s: %w", repoURL, err)
		return
	}

	remote, err := repo.Remote("origin")
	if err != nil {
		err = fmt.Errorf("failed to get remote origin: %w", err)
		return
	}

	if !isFresh {
		found := false
		for _, remoteURL := range remote.Config().URLs {
			if remoteURL == repoURL {
				found = true
				break
			}
		}
		if !found {
			log.Printf("remote for origin is not %q, cloning from scratch", repoURL)
			os.RemoveAll(targetDir)
			goto retry
		}
	}

	err = remote.Fetch(&git.FetchOptions{
		Auth: gitAuth,
		Tags: git.AllTags,
	})
	if err != nil {
		if err != git.NoErrAlreadyUpToDate {
			return
		}
		err = nil
	}

	log.Print("  `-> resetting deploy to remote branch ", branch)
	err = gitCleanBranch(branch, targetDir)
	if err != nil {
		return
	}

	return
}

func gitCleanBranch(branch, dir string) (err error) {
	err = os.RemoveAll(dir)
	if err != nil {
		err = fmt.Errorf("failed to clean %s: %w", dir, err)
		return
	}
	err = os.MkdirAll(dir, 0750)
	if err != nil {
		err = fmt.Errorf("failed to create %s: %w", dir, err)
		return
	}

	repo, err := gitOpen(dir+".git", dir)
	if err != nil {
		err = fmt.Errorf("failed to open repository in %s: %w", dir+".git", err)
		return
	}

	ref, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return
	}

	log.Print("- branch ", branch, " is on commit ", ref.Hash())

	w, err := repo.Worktree()
	if err != nil {
		err = fmt.Errorf("failed to get worktree: %w", err)
		return
	}

	if err = w.Clean(&git.CleanOptions{}); err != nil {
		err = fmt.Errorf("failed to clean: %w", err)
		return
	}

	if err = w.Reset(&git.ResetOptions{Commit: ref.Hash(), Mode: git.HardReset}); err != nil {
		err = fmt.Errorf("failed to reset: %w", err)
		return
	}

	return
}

func gitOpen(dotGitDir, dir string) (repo *git.Repository, err error) {
	dotGit := filesystem.NewStorage(osfs.New(dotGitDir), cache.NewObjectLRUDefault())
	work := osfs.New(dir)

	repo, err = git.Open(dotGit, work)
	if err != nil {
		err = fmt.Errorf("failed to open repository in %s: %w", dir+".git", err)
		return
	}

	return
}

func gitTag(dir, branch string) (tag string, err error) {
	repo, err := git.PlainOpen(dir + ".git") // FIXME ".git" should be given, not added
	if err != nil {
		return
	}

	ref, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return
	}

	tag = ref.String()[:7]
	return

	// TODO really go for a git describe equivalent? commit ID seems better in every case
}

func notify(msg string) {
	log.Output(2, "notification: "+msg)
	if *slackHook == "" {
		return
	}

	ba, _ := json.Marshal(map[string]interface{}{"text": msg})
	form := url.Values{}
	form.Set("payload", string(ba))
	r, err := http.PostForm(*slackHook, form)
	if err == nil {
		r.Body.Close()

		if r.StatusCode >= 300 {
			log.Print("notification rejected: ", r.Status)
		}
	} else {
		log.Print("notification failed: ", err)
	}
}

func notifyf(pattern string, args ...interface{}) {
	notify(fmt.Sprintf(pattern, args...))
}
