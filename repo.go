package main

type RepoRef struct {
	Repo   string
	Branch string
}

func (ref RepoRef) URL() string { return gitURL(ref.Repo) }
