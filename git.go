package main

import (
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/filesystem"
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

type gitOps struct {
	log *log.Logger
}

func (g gitOps) FetchBranch(repoURL, branch, targetDir string) (err error) {
	log := g.log

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

	if !isFresh && !slices.Contains(remote.Config().URLs, repoURL) {
		log.Printf("remote for origin is not %q, cloning from scratch", repoURL)
		os.RemoveAll(targetDir)
		goto retry
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
	err = g.CleanBranch(branch, targetDir)
	if err != nil {
		return
	}

	return
}

func (g gitOps) CleanBranch(branch, dir string) (err error) {
	log := g.log

	// err = os.RemoveAll(dir)
	// if err != nil {
	// 	err = fmt.Errorf("failed to clean %s: %w", dir, err)
	// 	return
	// }
	err = os.MkdirAll(dir, 0750)
	if err != nil {
		err = fmt.Errorf("failed to create %s: %w", dir, err)
		return
	}

	repo, err := g.Open(dir+".git", dir)
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

	branchRef := plumbing.NewBranchReferenceName(branch)
	w.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Create: true,
		Force:  true,
	})

	w.Reset(&git.ResetOptions{
		Commit: ref.Hash(),
		Mode:   git.HardReset,
	})

	if err = w.Clean(&git.CleanOptions{
		Dir: true,
	}); err != nil {
		err = fmt.Errorf("failed to clean: %w", err)
		return
	}

	return
}

func (g gitOps) Open(dotGitDir, dir string) (repo *git.Repository, err error) {
	dotGit := filesystem.NewStorage(osfs.New(dotGitDir), cache.NewObjectLRUDefault())
	work := osfs.New(dir)

	repo, err = git.Open(dotGit, work)
	if err != nil {
		err = fmt.Errorf("failed to open repository in %s: %w", dir+".git", err)
		return
	}

	return
}

func (g gitOps) BranchRef(dir, branch string) (repo *git.Repository, ref *plumbing.Reference, err error) {
	repo, err = git.PlainOpen(dir + ".git") // FIXME ".git" should be given, not added
	if err != nil {
		return
	}
	ref, err = repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	return
}

func (g gitOps) Tag(dir, branch string) (tag string, err error) {
	repo, ref, err := g.BranchRef(dir, branch)
	if err != nil {
		return
	}

	tag = ref.String()[:7]

	if *useExactTag {
		tagObj, err := repo.TagObject(ref.Hash())
		if err == nil {
			tag = tagObj.Name
		} else if err != plumbing.ErrObjectNotFound {
			return "", err // true error
		}
	}

	return
}

func (g gitOps) Describe(dir, branch string) (describe string, err error) {
	repo, ref, err := g.BranchRef(dir, branch)
	if err != nil {
		return
	}

	tags, err := repo.Tags()
	if err != nil {
		err = fmt.Errorf("failed to fetch tags: %w", err)
		return
	}

	commitsTag := map[plumbing.Hash]string{}
	err = tags.ForEach(func(ref *plumbing.Reference) error {
		tag, err := repo.TagObject(ref.Hash())
		if err != nil {
			return fmt.Errorf("failed to get tag details on %s: %w", ref.Name(), err)
		}

		commit, err := tag.Commit()
		if err != nil {
			return fmt.Errorf("failed to get tag commit on %s: %w", ref.Name(), err)
		}

		name := ref.Name().Short()
		if prev, ok := commitsTag[commit.Hash]; !ok || prev < name {
			commitsTag[commit.Hash] = name
		}
		return nil
	})
	if err != nil {
		return
	}

	log, err := repo.Log(&git.LogOptions{
		From:  ref.Hash(),
		Order: git.LogOrderCommitterTime,
	})
	if err != nil {
		err = fmt.Errorf("failed to compute git log: %w", err)
		return
	}

	commit := ref.String()[:7]
	describe = commit
	depth := 0

	err = log.ForEach(func(c *object.Commit) (err error) {
		tag := commitsTag[c.Hash]

		if tag == "" {
			depth += 1
			return nil
		}

		if depth == 0 {
			describe = tag
		} else {
			describe = fmt.Sprintf("%s-%d-g%s", tag, depth, commit)
		}

		return storer.ErrStop
	})

	if err == storer.ErrStop {
		err = nil
	}
	return
}
