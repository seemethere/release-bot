package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/github"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

var (
	webhookSecretEnvVariable = "RELEASE_BOT_WEBHOOK_SECRET"
	githubTokenEnvVariable   = "RELEASE_BOT_GITHUB_TOKEN"
	debugModeEnvVariable     = "RELEASE_BOT_DEBUG"
)

type githubMonitor struct {
	ctx    context.Context
	secret []byte
	client *github.Client
}

func (mon *githubMonitor) handleGithubWebhook(w http.ResponseWriter, r *http.Request) {
	log.Debugf("%s Recieved webhook", r.RequestURI)
	payload, err := github.ValidatePayload(r, mon.secret)
	if err != nil {
		log.Errorf("%s Failed to validate secret, %v", r.RequestURI, err)
		http.Error(w, "Secret did not match", http.StatusUnauthorized)
		return
	}
	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		log.Errorf("%s Failed to parse webhook, %v", r.RequestURI, err)
		http.Error(w, "Bad webhook payload", http.StatusBadRequest)
		return
	}
	switch e := event.(type) {
	case *github.IssuesEvent:
		if *e.Action == "labeled" {
			go mon.handleLabelEvent(e, r)
		}
	}
}

func (mon *githubMonitor) handleLabelEvent(e *github.IssuesEvent, r *http.Request) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	var columnID, cardID int
	var sourceColumn, destColumn github.ProjectColumn
	splitResults := strings.Split(*e.Label.Name, "/")
	// This is not the label we are looking for
	if len(splitResults) != 2 {
		return
	}
	projectPrefix := splitResults[0]
	labelSuffix := splitResults[1]
	project, err := mon.getProject(projectPrefix, e)
	if err != nil {
		log.Errorf("%q", err)
		return
	}
	columns, _, err := mon.client.Projects.ListProjectColumns(ctx, *project.ID, nil)
	if err != nil {
		log.Errorf("%q", err)
		return
	}
	columnName := map[string]string{
		"triage":        "Triage",
		"cherry-pick":   "Cherry Pick",
		"cherry-picked": "Cherry Picked",
	}[labelSuffix]
	if columnName == "" {
		columnName = labelSuffix
	}
	for _, column := range columns {
		// Found our column to move into
		if *column.Name == columnName {
			destColumn = *column
			columnID = *column.ID
		}
		cards, _, err := mon.client.Projects.ListProjectCards(ctx, *column.ID, nil)
		if err != nil {
			log.Errorf("%q", err)
			return
		}
		for _, card := range cards {
			if *card.ContentURL == *e.Issue.URL {
				sourceColumn = *column
				cardID = *card.ID
			}
		}
	}

	// destination column doesn't exist
	if destColumn == (github.ProjectColumn{}) {
		log.Infof(
			"%s Requested destination column '%v' does not exist for project '%v'",
			columnName,
			*project.Name,
		)
	}

	// card does not exist
	if cardID == 0 {
		contentType := "Issue"
		if e.Issue.PullRequestLinks != nil {
			contentType = "PullRequest"
		}
		log.Infof(
			"%s Creating card for issue #%v in project %v in column '%v'",
			r.RequestURI,
			*e.Issue.Number,
			*destColumn.Name,
		)
		_, _, err := mon.client.Projects.CreateProjectCard(
			ctx,
			columnID,
			&github.ProjectCardOptions{
				ContentID:   *e.Issue.ID,
				ContentType: contentType,
			},
		)
		if err != nil {
			log.Errorf(
				"%s Failed creating card for issue #%v in project %v in column '%v':\n%v",
				r.RequestURI,
				*e.Issue.Number,
				*destColumn.Name,
				err,
			)
		}
	} else {
		log.Infof(
			"%s Moving issue #%v in project %v from '%v' to '%v'",
			r.RequestURI,
			*e.Issue.Number,
			*project.Name,
			*sourceColumn.Name,
			*destColumn.Name,
		)
		_, err = mon.client.Projects.MoveProjectCard(
			ctx,
			cardID,
			&github.ProjectCardMoveOptions{
				Position: "top",
				ColumnID: columnID,
			},
		)

		if err != nil {
			log.Errorf(
				"%s Move failed for issue #%v in project %v from '%v' to '%v':\n%v",
				r.RequestURI,
				*e.Issue.Number,
				*project.Name,
				*sourceColumn.Name,
				*destColumn.Name,
				err,
			)
		}
	}
}

func (mon *githubMonitor) getProject(projectPrefix string, e *github.IssuesEvent) (*github.Project, error) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	projects, _, err := mon.client.Repositories.ListProjects(
		ctx,
		*e.Repo.Owner.Login,
		*e.Repo.Name,
		&github.ProjectListOptions{State: "open"},
	)
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		if strings.HasPrefix(*project.Name, projectPrefix) {
			return project, nil
		}
	}
	return nil, fmt.Errorf("No project found with prefix %s", projectPrefix)
}

func main() {
	debug := flag.Bool("debug", false, "Toggle debug mode")
	port := flag.String("port", "8080", "Port to bind release-bot to")
	flag.Parse()
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv(githubTokenEnvVariable)},
	)
	client := github.NewClient(oauth2.NewClient(ctx, ts))
	if *debug || os.Getenv(debugModeEnvVariable) != "" {
		log.SetLevel(log.DebugLevel)
		log.Debug("Log level set to debug")
	}
	monitor := githubMonitor{
		ctx:    ctx,
		secret: []byte(os.Getenv(webhookSecretEnvVariable)),
		client: client,
	}
	router := mux.NewRouter()
	router.Handle("/{user:.*}/{name:.*}", http.HandlerFunc(monitor.handleGithubWebhook)).Methods("POST")
	log.Infof("Starting release-bot on port %s", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", *port), router))
}
