package main

import (
	"context"
	"os"
	"regexp"

	"github.com/google/go-github/github"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"gopkg.in/alecthomas/kingpin.v2"
)

type Repository struct {
	Owner string
	Name  string
}

var (
	githubTokenEnvVariable = "GITHUB_TOKEN"
	projectName            = kingpin.Arg("projectName", "Name of the project to create, eg: 17.06.1-ce-rc4").String()
	releaseRepo            = Repository{Owner: "seemethere", Name: "test-repo"}
)

func main() {
	kingpin.Version("0.0.1")
	kingpin.Parse()
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv(githubTokenEnvVariable)},
	)
	rc := regexp.MustCompile("-rc.*$")
	client := github.NewClient(oauth2.NewClient(ctx, ts))
	log.Infof("Attempting to create project '%s' at %s/%s", projectName, releaseRepo.Owner, releaseRepo.Name)
	//TODO: Add a body template for projects
	client.Repositories.CreateProject(
		ctx,
		releaseRepo.Owner,
		releaseRepo.Name,
		&github.ProjectOptions{
			Name: *projectName,
		},
	)
}
