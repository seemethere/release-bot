package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
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
	rc                       = regexp.MustCompile("-rc.*$")
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
		switch *e.Action {
		case "labeled":
			go mon.handleLabelEvent(e, r)
		case "opened":
			go mon.handleIssueOpenedEvent(e, r)
		case "unlabeled":
			go mon.handleUnlabelEvent(e, r)
		}
	case *github.ProjectEvent:
		switch *e.Action {
		case "created":
			go mon.handleProjectCreatedEvent(e, r)
		}
	case *github.ProjectCardEvent:
		switch *e.Action {
		case "deleted":
			go mon.handleProjectCardDeletedEvent(e, r)
		case "created", "moved":
			go mon.handleProjectCardChangedEvent(e, r)
		}
	}
}

// Hey why is this one necessary?
// Github decided to paginate their results which leads us to having to do
// boilerplate code like the one below!
// Returns all labels associated with a repo. Maybe this could be optimized
// later by having a cache that expires?
func (mon *githubMonitor) allLabels(name, owner string) ([]*github.Label, error) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	opt := &github.ListOptions{}
	var labels []*github.Label
	for {
		labelsByPage, resp, err := mon.client.Issues.ListLabels(ctx, owner, name, opt)
		if err != nil {
			return nil, err
		}
		labels = append(labels, labelsByPage...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return labels, nil
}

// When a user submits an issue to docker/release-tracking we want that issue to
// automagically have a `triage` label for all open projects.
func (mon *githubMonitor) handleIssueOpenedEvent(e *github.IssuesEvent, r *http.Request) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	labels, err := mon.allLabels(*e.Repo.Name, *e.Repo.Owner.Login)
	if err != nil {
		log.Errorf("%q", err)
		return
	}
	appliedLabelsStructs, _, err := mon.client.Issues.ListLabelsByIssue(ctx, *e.Repo.Owner.Login, *e.Repo.Name, *e.Issue.Number, nil)
	appliedLabels := make(map[string]bool)
	if err != nil {
		log.Errorf("%q", err)
		return
	}
	for _, labelStruct := range appliedLabelsStructs {
		appliedLabels[*labelStruct.Name] = true
	}
	var labelsToApply []string
	for _, label := range labels {
		matched, err := regexp.MatchString(".*/triage", *label.Name)
		if err != nil {
			log.Errorf("%q", err)
			return
		}
		if matched {
			projectPrefix, _, err := splitLabel(*label.Name)
			if err != nil {
				log.Errorf("%q", err)
				return
			}
			// Only apply the label if there's a corresponding open project
			if _, err := mon.getProject(projectPrefix, e); err != nil {
				continue
			}
			if appliedLabels[*label.Name] == false {
				labelsToApply = append(labelsToApply, *label.Name)
			}
		}
	}
	// We have labels to apply
	if len(labelsToApply) > 0 {
		log.Infof("%v Adding labels %v to issue #%v", r.RequestURI, labelsToApply, *e.Issue.Number)
		_, _, err = mon.client.Issues.AddLabelsToIssue(
			ctx,
			*e.Repo.Owner.Login,
			*e.Repo.Name,
			*e.Issue.Number,
			labelsToApply,
		)
		if err != nil {
			log.Errorf("%q", err)
			return
		}
	}
}

// When a user adds a label matching {projectPrefix}/{action} it should move the
// issue in the corresponding open project to the correct column.
//
// Defined label -> column map:
//   * triage        -> Triage
//   * cherry-pick   -> Cherry Pick
//   * cherry-picked -> Cherry Picked
//
// NOTE: This should work even if an issue is not in a specified project board
//
// NOTE: This should work even for labels outside of the defined label map
//       For example a mapping of label `17.03.1-ee/bleh` should move that issue
//       to the bleh column of the open project of 17.03.1-ee-1-rc1 if that column
//       exists
func (mon *githubMonitor) handleLabelEvent(e *github.IssuesEvent, r *http.Request) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	var columnID, cardID int
	var sourceColumn, destColumn github.ProjectColumn
	projectPrefix, labelSuffix, err := splitLabel(*e.Label.Name)
	if err != nil {
		log.Errorf("%q", err)
		return
	}
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
		return
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
			*project.Name,
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
				*project.Name,
				*destColumn.Name,
				err,
			)
		}
	} else {
		if *sourceColumn.ID == *destColumn.ID {
			log.Debugf("%s Card for issue #%v is already where it needs to be", r.RequestURI, *e.Issue.Number)
			return
		}
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

func (mon *githubMonitor) handleUnlabelEvent(e *github.IssuesEvent, r *http.Request) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	var cardID int
	projectPrefix, labelSuffix, err := splitLabel(*e.Label.Name)
	if err != nil {
		log.Errorf("%q", err)
		return
	}
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
		cards, _, err := mon.client.Projects.ListProjectCards(ctx, *column.ID, nil)
		if err != nil {
			log.Errorf("%q", err)
			return
		}
		for _, card := range cards {
			if *card.ContentURL == *e.Issue.URL {
					cardID = *card.ID
					_, err := mon.client.Projects.DeleteProjectCard(ctx, cardID)
					if err != nil {
						log.Error("%q", err)
						return
					}
					return
			}
		}
	}
}

func (mon *githubMonitor) handleProjectCreatedEvent(e *github.ProjectEvent, r *http.Request) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	projectID := *e.Project.ID
	projectName := *e.Project.Name
	owner := *e.Repo.Owner.Login
	name := *e.Repo.Name
	columnsToCreate := []string{"Triage", "Cherry Pick", "Cherry Picked"}
	for _, column := range columnsToCreate {
		_, _, err := mon.client.Projects.CreateProjectColumn(
			ctx,
			projectID,
			&github.ProjectColumnOptions{Name: column},
		)
		if err != nil {
			log.Errorf("Error creating column %s: %v", column, err)
			return
		}
		log.Infof("Created column %s", column)
	}
	// Creates labels like 17.06.1-ee-1/triage from project names like 17.06.1-ee-1-rc3
	labelsToCreate := map[string]string{
		fmt.Sprintf("%s/triage", rc.ReplaceAllString(projectName, "")):        "eeeeee",
		fmt.Sprintf("%s/cherry-pick", rc.ReplaceAllString(projectName, "")):   "a98bf3",
		fmt.Sprintf("%s/cherry-picked", rc.ReplaceAllString(projectName, "")): "bfe5bf",
	}
	// TODO: Add body for label filtering
	existingLabels, err := mon.allLabels(owner, name)
	if err != nil {
		log.Errorf("Could not grab existing labels for %s/%s: %v", owner, name, err)
		return
	}
	for _, label := range existingLabels {
		if labelsToCreate[*label.Name] != "" {
			delete(labelsToCreate, *label.Name)
		}
	}
	for labelName, color := range labelsToCreate {
		_, _, err = mon.client.Issues.CreateLabel(ctx, owner, name, &github.Label{Name: &labelName, Color: &color})
		if err != nil {
			log.Errorf("Error creating label %s for repo %s/%s: %v", labelName, owner, name, err)
			return
		}
		log.Infof("Created label %s", labelName)
	}
}

func (mon *githubMonitor) handleProjectCardDeletedEvent(e *github.ProjectCardEvent, r *http.Request) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	project, err := mon.getRelatedProject(ctx, e.ProjectCard)
	if err != nil {
		log.Errorf("Error getting project related to card: %v", err)
		return
	}
	labelPrefix := rc.ReplaceAllString(*project.Name, "")
	issueBits := strings.Split(*e.ProjectCard.ContentURL, "/")
	issueNum, err := strconv.Atoi(issueBits[len(issueBits)-1])
	if err != nil {
		log.Errorf("%v", err)
		return
	}
	// Creates labels like 17.06.1-ee-1/triage from project names like 17.06.1-ee-1-rc3
	labelsToDelete := []string{
		fmt.Sprintf("%s/triage", labelPrefix),
		fmt.Sprintf("%s/cherry-pick", labelPrefix),
		fmt.Sprintf("%s/cherry-picked", labelPrefix),
	}
	for _, label := range labelsToDelete {
		log.Infof("Deleting label %s for issue %s/%s#%d", label, *e.Repo.Owner.Login, *e.Repo.Name, issueNum)
		_, err = mon.client.Issues.RemoveLabelForIssue(ctx, *e.Repo.Owner.Login, *e.Repo.Name, issueNum, label)
	}
}

func (mon *githubMonitor) handleProjectCardChangedEvent(e *github.ProjectCardEvent, r *http.Request) {
	ctx, cancel := context.WithTimeout(mon.ctx, 5*time.Minute)
	defer cancel()
	column, err := mon.getRelatedColumn(ctx, e.ProjectCard)
	if err != nil {
		log.Errorf("Error getting column related to card %s", *e.ProjectCard.URL)
		return
	}
	project, err := mon.getRelatedProject(ctx, e.ProjectCard)
	if err != nil {
		log.Errorf("Error getting project related to card %s", *e.ProjectCard.URL)
		return
	}
	labelPrefix := rc.ReplaceAllString(*project.Name, "")
	labelsToDelete := []string{
		fmt.Sprintf("%s/triage", labelPrefix),
		fmt.Sprintf("%s/cherry-pick", labelPrefix),
		fmt.Sprintf("%s/cherry-picked", labelPrefix),
	}
	columnName := map[string]string{
		"Triage":        "triage",
		"Cherry Pick":   "cherry-pick",
		"Cherry Picked": "cherry-picked",
	}[*column.Name]
	issueBits := strings.Split(*e.ProjectCard.ContentURL, "/")
	issueNum, err := strconv.Atoi(issueBits[len(issueBits)-1])
	if err != nil {
		log.Errorf("%v", err)
		return
	}
	appliedLabelsStructs, _, err := mon.client.Issues.ListLabelsByIssue(ctx, *e.Repo.Owner.Login, *e.Repo.Name, issueNum, nil)
	appliedLabels := make(map[string]bool)
	if err != nil {
		log.Errorf("%q", err)
		return
	}
	for _, labelStruct := range appliedLabelsStructs {
		appliedLabels[*labelStruct.Name] = true
	}
	for _, label := range labelsToDelete {
		// Only remove labels that don't relate to our column name
		if label == fmt.Sprintf("%s/%s", labelPrefix, columnName) {
			if !appliedLabels[label] {
				_, _, err := mon.client.Issues.AddLabelsToIssue(ctx, *e.Repo.Owner.Login, *e.Repo.Name, issueNum, []string{label})
				if err != nil {
					log.Errorf("Error applying label %s from %s/%s#%d: %v", label, *e.Repo.Owner.Login, *e.Repo.Name, issueNum, err)
				}
				log.Infof("Added label %s to %s/%s#%d", label, *e.Repo.Owner.Login, *e.Repo.Name, issueNum)
			}
		} else {
			if appliedLabels[label] {
				resp, err := mon.client.Issues.RemoveLabelForIssue(ctx, *e.Repo.Owner.Login, *e.Repo.Name, issueNum, label)
				// Most errors occur when label does not exist
				if resp.StatusCode == 404 && err != nil {
					log.Debugf("Label %s for %s/%s#%d not found moving on...", label, *e.Repo.Owner.Login, *e.Repo.Name, issueNum)
				} else if err != nil {
					log.Errorf("Error removing label %s from %s/%s#%d: %v", label, *e.Repo.Owner.Login, *e.Repo.Name, issueNum, err)
				}
				log.Infof("Removed label %s from %s/%s#%d", label, *e.Repo.Owner.Login, *e.Repo.Name, issueNum)
			}
		}
	}
}

func (mon *githubMonitor) getRelatedColumn(ctx context.Context, card *github.ProjectCard) (*github.ProjectColumn, error) {
	columnBits := strings.Split(*card.ColumnURL, "/")
	columnID, err := strconv.Atoi(columnBits[len(columnBits)-1])
	if err != nil {
		return nil, err
	}
	column, _, err := mon.client.Projects.GetProjectColumn(ctx, columnID)
	if err != nil {
		return nil, err
	}
	return column, nil
}

func (mon *githubMonitor) getRelatedProject(ctx context.Context, card *github.ProjectCard) (*github.Project, error) {
	column, err := mon.getRelatedColumn(ctx, card)
	if err != nil {
		return nil, err
	}
	projectBits := strings.Split(*column.ProjectURL, "/")
	projectID, err := strconv.Atoi(projectBits[len(projectBits)-1])
	if err != nil {
		return nil, err
	}
	project, _, err := mon.client.Projects.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return project, nil
}

func splitLabel(label string) (string, string, error) {
	splitResults := strings.Split(label, "/")
	if len(splitResults) != 2 {
		return "", "", fmt.Errorf("Label does not match pattern {release}/{action}")
	}
	return splitResults[0], splitResults[1], nil
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
