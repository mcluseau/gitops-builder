package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func setupHTTP() {
	http.HandleFunc("/webhook", handleWebhook)
	http.HandleFunc("/build-logs/", handleBuildLog)
}

func handleWebhook(w http.ResponseWriter, req *http.Request) {
	data := &struct {
		Secret     string `json:"secret"`
		Ref        string `json:"ref"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
			SshUrl   string `json:"ssh_url"`
		} `json:"repository"`
	}{}

	err := json.NewDecoder(req.Body).Decode(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if webhookSecret != "" {
		if webhookSecret != data.Secret {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
	}

	_, branch, found := strings.Cut(data.Ref, "refs/heads/")

	if !found {
		log.Print("webhook ignored: ref is not a branch: ", data.Ref)
		return
	}

	go func() {
		for _, u := range []string{data.Repository.CloneURL, data.Repository.SshUrl} {
			if u == "" {
				continue
			}

			if triggerFromURL(u, branch) {
				break
			}
		}
	}()
}

func handleBuildLog(w http.ResponseWriter, req *http.Request) {
	buildID := path.Base(req.URL.Path)

	logsDir := filepath.Join(*workDir, "logs")
	logFile := filepath.Join(logsDir, buildID+".log")

	f, err := os.Open(logFile)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "text/plain")
	io.Copy(w, f)
}
