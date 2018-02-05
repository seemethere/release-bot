package main

import (
	"context"
	"os"

	"github.com/google/go-github/github"
	"github.com/seemethere/release-bot/utilities/create-project/cmd"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	githubTokenEnvVariable = "GITHUB_TOKEN"
	projectName            = kingpin.Arg("source-project", "Name of the project to create").Required().String()
	repoName               = kingpin.Flag("repo-name", "Name of the repository to point to").Short('r').Default("staging-release-tracking").String()
	repoOwner              = kingpin.Flag("repo-owner", "Name of the owner of the repository to point to").Short('o').Default("docker").String()
	verbose                = kingpin.Flag("verbose", "See debug statements").Short('v').Bool()
)

func main() {
	kingpin.Version("0.0.1")
	kingpin.Parse()
	if *verbose {
		log.SetLevel(log.DebugLevel)
	}
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv(githubTokenEnvVariable)},
	)
	client := github.NewClient(oauth2.NewClient(ctx, ts))
	_, err := cmd.CreateProject(client, ctx, *projectName, *repoOwner, *repoName)

	if err != nil {
		log.Errorf("Source %v", err)
		os.Exit(1)
	}

	log.Infof("Project %s successfully created", *projectName)
}
