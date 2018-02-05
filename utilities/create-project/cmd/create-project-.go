package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/github"
)

func CreateProject(client *github.Client, ctx context.Context, projectName, repoOwner, repoName string) (*github.Project, error) {
	opt := &github.ProjectOptions{Name: projectName, Body: ""}
	info := strings.Split(projectName, "-") //ex. 18.02.0-ce-rc2 -> [18.02.0, ce, rc2]
	if len(info) == 3 {
		opt.Body = fmt.Sprintf(`Docker %s %s %s release`, info[0], strings.ToUpper(info[1]), strings.ToUpper(info[2]))
	}
	project, _, err := client.Repositories.CreateProject(ctx, repoOwner, repoName, opt)
	if err != nil {
		return nil, fmt.Errorf("Project '%s' failed to create project", projectName)
	} else {
		return project, nil
	}
}
