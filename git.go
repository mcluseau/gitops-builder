package main

import (
	"log"
	"os"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/spf13/pflag"
)

var (
	gitPrefix   string
	gitUser     string
	gitPassword string

	gitAllowedPrefixes []string

	gitAuth transport.AuthMethod
)

func init() {
	pflag.StringVar(&gitPrefix, "git-prefix", "", "git repo prefix (ie: \"git@git.myorg:\", \"https://git.myorg/\")")
	pflag.StringSliceVar(&gitAllowedPrefixes, "git-allow-prefix", gitAllowedPrefixes, "allow git prefix")

	if t := os.Getenv("GIT_TOKEN"); t != "" {
		log.Print("setting git auth from $GIT_TOKEN")
		gitAuth = &githttp.TokenAuth{Token: t}
	} else if u, p := os.Getenv("GIT_USER"), os.Getenv("GIT_PASSWORD"); u != "" && p != "" {
		log.Print("setting git auth from $GIT_USER and $GIT_PASSWORD")
		gitAuth = &githttp.BasicAuth{Username: u, Password: p}
	} else if sshUser, sshAuthSock := os.Getenv("GIT_SSH_USER"), os.Getenv("SSH_AUTH_SOCK"); sshUser != "" && sshAuthSock != "" {
		log.Print("setting git auth from $GIT_SSH_USER $SSH_AUTH_SOCK")
		var err error
		gitAuth, err = gitssh.NewSSHAgentAuth(sshUser)
		if err != nil {
			log.Print("WARNING: failed to setup SSH agent auth for git: ", err)
		}
	}
}

func gitURL(repo string) string {
	return gitPrefix + repo
}

func cutAllowedPrefix(url string) (base string, ok bool) {
	base, ok = strings.CutPrefix(url, gitPrefix)
	if ok {
		return
	}

	for _, allowedPrefix := range gitAllowedPrefixes {
		base, ok = strings.CutPrefix(url, allowedPrefix)
		if ok {
			return
		}
	}

	return
}
