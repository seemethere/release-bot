package main

import (
	"context"
	"fmt"
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
	projectName            = kingpin.Arg("projectName", "Name of the project to create, eg: 17.06.1-ce-rc4").Required().String()
	repoName               = kingpin.Flag("repo", "Name of the repo to point to").Short('r').Default("staging-release-tracking").String()
	repoOwner              = kingpin.Flag("owner", "Owner of the repo to point to").Short('o').Default("docker").String()
)

func projectExists(owner, name, project string, client *github.Client, ctx context.Context) bool {
	existingProjects, _, err := client.Repositories.ListProjects(ctx, owner, name, nil)
	if err != nil {
		log.Errorf("Could not grab existing projects for %s/%s: %v", owner, name, err)
		os.Exit(1)
	}
	for _, existingProject := range existingProjects {
		if project == *existingProject.Name {
			return true
		}
	}
	return false
}

func createLabels(owner, name, project string, client *github.Client, ctx context.Context) {
	rc := regexp.MustCompile("-rc.*$")
	// Creates labels like 17.06.1-ee-1/triage from project names like 17.06.1-ee-1-rc3
	labelsToCreate := map[string]string{
		fmt.Sprintf("%s/triage", rc.ReplaceAllString(project, "")):        "eeeeee",
		fmt.Sprintf("%s/cherry-pick", rc.ReplaceAllString(project, "")):   "a98bf3",
		fmt.Sprintf("%s/cherry-picked", rc.ReplaceAllString(project, "")): "bfe5bf",
	}
	existingLabels, _, err := client.Issues.ListLabels(ctx, owner, name, nil)
	if err != nil {
		log.Errorf("Could not grab existing labels for %s/%s: %v", owner, name, err)
		os.Exit(1)
	}
	for _, label := range existingLabels {
		if labelsToCreate[*label.Name] != "" {
			delete(labelsToCreate, *label.Name)
		}
	}
	for labelName, color := range labelsToCreate {
		_, _, err = client.Issues.CreateLabel(ctx, owner, name, &github.Label{Name: &labelName, Color: &color})
		if err != nil {
			log.Errorf("Error creating label %s for repo %s/%s: %v", labelName, owner, name, err)
			os.Exit(1)
		}
		log.Infof("Created label %s", labelName)
	}
}

func createColumns(projectID int, client *github.Client, ctx context.Context) {
	columnsToCreate := []string{"Triage", "Cherry Pick", "Cherry Picked"}
	for _, column := range columnsToCreate {
		_, _, err := client.Projects.CreateProjectColumn(ctx, projectID, &github.ProjectColumnOptions{Name: column})
		if err != nil {
			log.Errorf("Error creating column %s: %v", column, err)
			os.Exit(1)
		}
		log.Infof("Created column %s", column)
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
	releaseRepo := Repository{Owner: *repoOwner, Name: *repoName}
	if projectExists(releaseRepo.Owner, releaseRepo.Name, *projectName, client, ctx) {
		log.Errorf("Project '%s' already exists for repo %s/%s", projectName, releaseRepo.Owner, releaseRepo.Name)
		os.Exit(1)
	}
	log.Infof("Attempting to create project '%s' at %s/%s", projectName, releaseRepo.Owner, releaseRepo.Name)
	//TODO: Add a body template for projects
	project, _, err := client.Repositories.CreateProject(
		ctx,
		releaseRepo.Owner,
		releaseRepo.Name,
		&github.ProjectOptions{
			Name: *projectName,
		},
	)
	if err != nil {
		log.Errorf("Error creating project: %v", err)
		os.Exit(1)
	}
	createLabels(releaseRepo.Owner, releaseRepo.Name, *projectName, client, ctx)
	createColumns(*project.ID, client, ctx)
}
