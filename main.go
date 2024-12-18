package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/spf13/pflag"
)

var (
	hostname, _   = os.Hostname()
	workDir       = pflag.String("work-dir", "work", "work directory")
	triggerGit    = pflag.String("trigger-git", "", "trigger git")
	triggerBranch = pflag.String("trigger-branch", "main", "trigger branch")
	slackHook     = pflag.String("slack-hook", "", "Slack notification hook")
	builderURL    = pflag.String("url", "http://"+hostname, "builder's URL")
	useExactTag   = pflag.Bool("use-exact-tag", false, "use tag name on commits with an annotated tag")
	tagDescribe   = pflag.Bool("tag-describe", false, "tag builds with describe, not just the commit")
)

func main() {
	var err error

	log.SetFlags(log.Flags() | log.Lshortfile | log.Lmsgprefix)

	bind := pflag.String("bind", ":80", "HTTP bind for triggers")
	pflag.Parse()

	if args := pflag.Args(); len(args) != 0 {
		for _, arg := range args {
			log.Printf("processing arg: %q", arg)
			switch arg {
			case "describe":
				g := gitOps{log.Default()}
				fmt.Println(g.Describe("", "main"))
			default:
				log.Fatal("unknown arg")
			}
		}
		return
	}

	if appsRepo.Repo == "" {
		log.Fatal("--apps-repo is mandatory")
	}

	if gitAuth == nil {
		log.Print("WARNING: no git authentication defined (env GIT_TOKEN or GIT_USER and GIT_PASSWORD)")
	}

	updateApps()

	err = os.MkdirAll(*workDir, 0750)
	if err != nil {
		log.Fatal("failed to create work dir: ", err)
	}

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
				run := &BuildRun{
					app:    app,
					build:  build,
					branch: branch,
				}
				run.Run()
			}
		}
	}
}

func execCmd(log *log.Logger, wd, bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir = wd
	cmd.Stdout = log.Writer()
	cmd.Stderr = log.Writer()

	log.Print("  ", wd, "$ ", bin, " ", args)
	return cmd.Run()
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
