package main

import (
	"log"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v2"
)

var (
	appsRepo     = RepoRef{}
	appsFilePath string

	appsCommit     string
	currentProject Project
)

func init() {
	pflag.StringVar(&appsRepo.Repo, "apps-repo", "", "Apps repository path")
	pflag.StringVar(&appsRepo.Branch, "apps-branch", "main", "Apps repository branch")
	pflag.StringVar(&appsFilePath, "apps-file", "apps.yaml", "Apps file path in repository")
}

func updateApps() {
	log := log.New(log.Writer(), "update apps: ", log.Flags()|log.Lmsgprefix)
	//customClient := &http.Client{
	//	// accept any certificate (might be useful for testing)
	//	Transport: &http.Transport{
	//		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	//	},

	//	// 15 second timeout
	//	Timeout: 15 * time.Second,
	//}

	//client.InstallProtocol("https", githttp.NewClient(customClient))

	storage := memory.NewStorage()
	r, err := git.Clone(storage, nil, &git.CloneOptions{
		URL:  appsRepo.URL(),
		Auth: gitAuth,
	})

	if err != nil {
		log.Printf("failed to clone repo %q: %v", appsRepo.Repo, err)
		return
	}

	h, err := r.ResolveRevision(plumbing.Revision(appsRepo.Branch))
	if err != nil {
		log.Printf("failed to resolve revision %q: %v: ", appsRepo.Branch, err)
		return
	}

	commit, err := r.CommitObject(*h)
	if err != nil {
		log.Print("failed to get commit: ", err)
		return
	}

	tree, err := commit.Tree()
	if err != nil {
		log.Print("failed to get commit tree: ", err)
		return
	}

	appsFile, err := tree.File(appsFilePath)
	if err != nil {
		log.Printf("failed to get file %q: %v", appsFilePath, err)
		return
	}

	f, err := appsFile.Reader()
	if err != nil {
		log.Print("failed to create file reader: ", err)
		return
	}

	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.SetStrict(true)

	project := Project{}
	err = dec.Decode(&project)
	if err != nil {
		log.Printf("failed to parse %q: %v", appsFilePath, err)
		return
	}

	project.apps = make([]App, 0, len(project.Apps))
	for idx, desc := range project.Apps {
		app, err := desc.GetApp(tree)

		if err != nil {
			log.Printf("failed to load apps[%d]: %v", idx, err)
			continue
		}

		project.apps = append(project.apps, app)
	}

	msg, _, _ := strings.Cut(commit.Message, "\n")
	msg = strings.TrimSpace(msg)
	log.Printf("loaded %d apps (commit %s: %s)", len(project.apps), commit.ID().String()[:7], msg)

	currentProject = project
}

type Project struct {
	Apps []*AppDesc
	apps []App
}

type App struct {
	Name       string
	Deploy     string
	Builds     []Build
	DockerArgs []string `yaml:"docker_args"`
}

type Build struct {
	Source        string
	Overlay       string
	Docker        string
	Branches      []*BranchInfo
	DeployUpdates []DeployUpdate `yaml:"deploy_updates"`
	DockerArgs    []string       `yaml:"docker_args"`
}

type BranchInfo struct {
	Source          string
	Overlay         string
	Deploy          string
	DockerTagSuffix string   `yaml:"docker_tag_suffix"`
	DockerArgs      []string `yaml:"docker_args"`
}

type DeployUpdate struct {
	Script  string
	YamlSet *YamlSet `yaml:"yaml_set"`
}
