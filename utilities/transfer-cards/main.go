package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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
	columnsToMove          = kingpin.Flag("columns", "Columns to pull from, comma separated").Short('c').Default("Triage,Cherry Pick").String()
	repoName               = kingpin.Flag("repo-name", "Name of the repository to point to").Short('r').Default("staging-release-tracking").String()
	repoOwner              = kingpin.Flag("repo-owner", "Name of the owner of the repository to point to").Short('o').Default("docker").String()
	verbose                = kingpin.Flag("verbose", "See debug statements").Short('v').Bool()
)

func getProject(client *github.Client, ctx context.Context, projectName string, source bool) (*github.Project, error) {
	log.Debugf("Attempting to find project %s for repo %s/%s", projectName, *repoOwner, *repoName)

	var project *github.Project

	opt := &github.ProjectListOptions{State: "all"}
	for {
		projects, resp, getProjectErr := client.Repositories.ListProjects(ctx, *repoOwner, *repoName, opt)
		if getProjectErr != nil {
			log.Errorf("Could not grab existing projects for %s/%s: %v", *repoOwner, *repoName, getProjectErr)
			break
		}
		for _, project := range projects {
			if *project.Name == projectName {
				return project, nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	// don't want to create a project if the project is a source project
	if source {
		return nil, fmt.Errorf("project '%s' not found in repo %s/%s", projectName, *repoOwner, *repoName)
	}
	// if this is a dry run do not create the project
	if *dryrun {
		return nil, fmt.Errorf("project '%s' not found in repo %s/%s if you would like the utility to create the project rerun without the dryrun option", projectName, *repoOwner, *repoName)
	}

	// the project was not found so try creating the project
	log.Info("Project not found creating project")
	project, createProjectErr := createProject(client, ctx, projectName)
	if createProjectErr != nil {
		return nil, createProjectErr
	} else {
		log.Info("Project creation successful")
		return project, nil
	}

}

// When a project is created the release bot needs to add project columns (triage, cherry-pick, and
// cherry-picked) to the project. The transfer card utility should not progress until the release-bot is done
func releaseBotDone(client *github.Client, ctx context.Context, projectID int) (bool, error) {
	columnsLength := 0
	retries := 1

	for retries < 4 && columnsLength != 3 {
		log.Infof("Release bot progress: retries: %d, project columns:  %d", retries, columnsLength)

		columns, _, err := client.Projects.ListProjectColumns(context.Background(), projectID, &github.ListOptions{})
		if err != nil {
			return false, err
		}
		columnsLength = len(columns)
		retries += 1

		time.Sleep(5 * time.Second)
	}

	if columnsLength == 3 {
		return true, nil
	} else {
		return false, nil
	}
}

func createProject(client *github.Client, ctx context.Context, projectName string) (*github.Project, error) {
	opt := &github.ProjectOptions{State: "all", Name: projectName, Body: ""}
	info := strings.Split(projectName, "-") //ex. 18.02.0-ce-rc2 -> [18.02.0, ce, rc2]
	if len(info) == 3 {
		opt.Body = fmt.Sprintf(`Docker %s %s %s release`, info[0], strings.ToUpper(info[1]), strings.ToUpper(info[2]))
	}
	project, _, err := client.Repositories.CreateProject(ctx, *repoOwner, *repoName, opt)
	if err != nil {
		return nil, fmt.Errorf("Project '%s' failed to create project", projectName)
	}

	// Wait for release-bot to add triage, cherry-pick and cherry-picked labels
	log.Info("Checking that the release bot has created the necessary project cards")
	releaseBotFinished, err := releaseBotDone(client, ctx, *project.ID)
	if err != nil {
		return nil, err
	} else if releaseBotFinished {
		return project, nil
	} else {
		return nil, fmt.Errorf("The release bot did not create the expected project columns")
	}
}

func getColumnID(columnToFind string, columnHaystack []*github.ProjectColumn) (int, error) {
	for _, column := range columnHaystack {
		if *column.Name == columnToFind {
			return *column.ID, nil
		}
	}
	return 0, fmt.Errorf("column %s not found!", columnToFind)
}

func getRelatedIssue(card *github.ProjectCard, issues []*github.Issue) (*github.Issue, error) {
	for _, issue := range issues {
		if *card.ContentURL == *issue.URL {
			return issue, nil
		}
	}
	return nil, fmt.Errorf("card %s not related to an existing issue with content url %s", *card.URL, *card.ContentURL)
}

func allIssues(client *github.Client, ctx context.Context) ([]*github.Issue, error) {
	opt := &github.IssueListByRepoOptions{}
	var issues []*github.Issue
	for {
		issuesByPage, resp, err := client.Issues.ListByRepo(ctx, *repoOwner, *repoName, opt)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issuesByPage...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return issues, nil
}

func getCards(client *github.Client, ctx context.Context, sourceColumnID int) ([]*github.ProjectCard, error) {
	opt := &github.ListOptions{}
	var cards []*github.ProjectCard
	for {
		sourceCardsByPage, resp, err := client.Projects.ListProjectCards(ctx, sourceColumnID, opt)
		if err != nil {
			return nil, err
		}

		cards = append(cards, sourceCardsByPage...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return cards, nil
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
	issues, err := allIssues(client, ctx)
	if err != nil {
		log.Errorf("Error grabbing issues for repo: %v", err)
		os.Exit(1)
	}
	for _, column := range columns {
		var p0Cards, p1Cards, p2Cards, noPCards []*github.ProjectCard
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
		sourceCards, err := getCards(client, ctx, sourceColumnID)
		if err != nil {
			log.Errorf("Error retrieving source project cards")
			os.Exit(1)
		}
		for _, card := range sourceCards {
			relatedIssue, err := getRelatedIssue(card, issues)
			var priority string
			if err != nil {
				log.Errorf("%v", err)
				os.Exit(1)
			}
			for _, label := range relatedIssue.Labels {
				if strings.Contains(*label.Name, "priority") {
					priority_label := strings.Split(*label.Name, "/")
					priority = priority_label[1]
				}
			}
			switch priority {
			case "p0":
				p0Cards = append(p0Cards, card)
			case "p1":
				p1Cards = append(p1Cards, card)
			case "p2":
				p2Cards = append(p2Cards, card)
			default:
				noPCards = append(noPCards, card)
			}
		}
		deleteCards := func(cards []*github.ProjectCard) {
			for _, card := range cards {
				relatedIssue, err := getRelatedIssue(card, issues)
				if err != nil {
					log.Errorf("%v", err)
					os.Exit(1)
				}
				if !*dryrun {
					log.Debugf("Deleting project card for issue #%d from %s", *relatedIssue.Number, *sourceProjectName)
					_, err = client.Projects.DeleteProjectCard(ctx, *card.ID)
					if err != nil {
						log.Errorf("Error deleting project card %d: %v", *relatedIssue.Number, err)
						os.Exit(1)
					}
				}
			}
		}
		createCards := func(cards []*github.ProjectCard) {
			for _, card := range cards {
				relatedIssue, err := getRelatedIssue(card, issues)
				if err != nil {
					log.Errorf("%v", err)
					os.Exit(1)
				}
				prefix := "(dryrun)"
				if !*dryrun {
					prefix = ""
					log.Debugf("Creating new project card for issue #%d in %s", *relatedIssue.Number, *destProjectName)
					_, resp, err := client.Projects.CreateProjectCard(ctx, destColumnID, &github.ProjectCardOptions{ContentID: *relatedIssue.ID, ContentType: "Issue"})
					if err != nil {
						switch resp.StatusCode {
						case 402:
							break
						case 422:
							log.Warnf("Could not create project card for issue #%d: Issue already exists in project", *relatedIssue.Number)
						default:
							log.Errorf("Error creating project card for issue #%d: %v", *relatedIssue.Number, err)
						}
					}
				}
				log.Infof("%s%s/%s -> %s/%s: #%d", prefix, *sourceProject.Name, column, *destProject.Name, column, *relatedIssue.Number)
			}
		}
		deleteCards(p2Cards)
		deleteCards(p1Cards)
		deleteCards(p0Cards)
		deleteCards(noPCards)
		createCards(p2Cards)
		createCards(p1Cards)
		createCards(p0Cards)
		createCards(noPCards)
	}
}

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
	sourceProject, err := getProject(client, ctx, *sourceProjectName, true)
	if err != nil {
		log.Errorf("Source %v", err)
		os.Exit(1)
	}
	destProject, err := getProject(client, ctx, *destProjectName, false)
	if err != nil {
		log.Errorf("Destination %v", err)
		os.Exit(1)
	}
	log.Infof("Source project: %v, Dest Project: %v", *sourceProject.Name, *destProject.Name)
	if !*dryrun {
		client.Projects.UpdateProject(ctx, *sourceProject.ID, &github.ProjectOptions{State: "closed"})
	}
	moveIssues(client, ctx, sourceProject, destProject, strings.Split(*columnsToMove, ","))
}
