package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/github"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	githubTokenEnvVariable = "GITHUB_TOKEN"
	sourceProjectName      = kingpin.Arg("source-project", "Name of the project to pull cards from").Required().String()
	destProjectName        = kingpin.Arg("destination-project", "Name of the project to put cards into").Required().String()
	dryrun                 = kingpin.Flag("dry-run", "Don't make any changes upstream").Bool()
	columnsToMove          = kingpin.Flag("columns", "Columns to pull from, comma separated").Short('c').Default("Triage,Cherry Picked").String()
	repoName               = kingpin.Flag("repo-name", "Name of the repository to point to").Short('r').Default("staging-release-tracking").String()
	repoOwner              = kingpin.Flag("repo-owner", "Name of the owner of the repository to point to").Short('o').Default("docker").String()
)

func getProject(client *github.Client, ctx context.Context, projectName string) (*github.Project, error) {
	log.Debug("Attempting to find project %s for repo %s/%s", projectName, *repoOwner, *repoName)
	projects, _, err := client.Repositories.ListProjects(ctx, *repoOwner, *repoName, nil)
	if err != nil {
		log.Errorf("Could not grab existing projects for %s/%s: %v", *repoOwner, *repoName, err)
		os.Exit(1)
	}
	for _, project := range projects {
		if *project.Name == projectName {
			return project, nil
		}
	}
	return nil, fmt.Errorf("project '%s' not found in repo %s/%s", projectName, *repoOwner, *repoName)
}

func getColumnID(columnToFind string, columnHaystack []*github.ProjectColumn) (int, error) {
	for _, column := range columnHaystack {
		if *column.Name == columnToFind {
			return *column.ID, nil
		}
	}
	return 0, fmt.Errorf("column %s not found!", columnToFind)
}

func moveIssues(client *github.Client, ctx context.Context, sourceProject, destProject *github.Project, columns []string) {
	sourceColumns, _, err := client.Projects.ListProjectColumns(ctx, *sourceProject.ID, nil)
	if err != nil {
		log.Errorf("Error grabbing columns for project %s: %v", *sourceProject.Name, err)
		os.Exit(1)
	}
	destColumns, _, err := client.Projects.ListProjectColumns(ctx, *destProject.ID, nil)
	if err != nil {
		log.Errorf("Error grabbing columns for project %s: %v", *destProject.Name, err)
		os.Exit(1)
	}
	for _, column := range columns {
		sourceColumnID, err := getColumnID(column, sourceColumns)
		if err != nil {
			log.Errorf("Source %v", err)
			os.Exit(1)
		}
		destColumnID, err := getColumnID(column, destColumns)
		if err != nil {
			log.Errorf("Destination %v", err)
			os.Exit(1)
		}
		sourceCards, _, err := client.Projects.ListProjectCards(ctx, sourceColumnID, nil)
		if err != nil {
			log.Errorf("Error retrieving source project cards")
			os.Exit(1)
		}
	}
}

func main() {
	kingpin.Version("0.0.1")
	kingpin.Parse()
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv(githubTokenEnvVariable)},
	)
	client := github.NewClient(oauth2.NewClient(ctx, ts))
	sourceProject, err := getProject(client, ctx, *sourceProjectName)
	if err != nil {
		log.Errorf("Source %v", err)
		os.Exit(1)
	}
	destProject, err := getProject(client, ctx, *destProjectName)
	if err != nil {
		log.Errorf("Destination %v", err)
		os.Exit(1)
	}
	fmt.Printf("Source project: %v, Dest Project: %v\n", *sourceProject.Name, *destProject.Name)
	for ndx, arg := range strings.Split(*columnsToMove, ",") {
		fmt.Printf("%v: %s\n", ndx, arg)
	}
}
