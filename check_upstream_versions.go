package main

import (
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"

	"github.com/google/go-github/v39/github"
)

type DockerStream struct {
	Image string `json:"image"`
	Tag   string `json:"tag"`
	Label string `json:"label"`
	Stream
}

type GitHubStream struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Stream
}

type Stream struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Project struct {
	Name            string                   `json:"name"`
	ProjectType     string                   `json:"type"`
	Branch          string                   `json:"branch"`
	BuildWorkflow   string                   `json:"build_workflow_filename"`
	JsonUpstreams   []map[string]interface{} `json:"upstreams"`
	JsonDownstreams []map[string]interface{} `json:"downstreams"`
	Upstreams       []interface{}
	Downstreams     []interface{}
}

type Projects struct {
	Projects []Project `json:"projects"`
}

func check(e error) {
	if e != nil {
		log.Fatal(e)
	}
}

func readStreamsJson(filename string) Projects {
	jsonFile, err := os.Open(filename)
	check(err)
	defer jsonFile.Close()

	byteValue, err := ioutil.ReadAll(jsonFile)
	check(err)

	var projects, parsedProjects Projects

	jsonErr := json.Unmarshal(byteValue, &projects)
	check(jsonErr)

	for _, project := range projects.Projects {

		var upstreams, downstreams []interface{}
		for _, upstream := range project.JsonUpstreams {
			if upstream["type"] == "docker" {
				var newUpstream DockerStream
				newJson, err := json.Marshal(upstream)
				check(err)
				json.Unmarshal(newJson, &newUpstream)
				upstreams = append(upstreams, newUpstream)
			} else if upstream["type"] == "github" {
				var newUpstream GitHubStream
				newJson, err := json.Marshal(upstream)
				check(err)
				json.Unmarshal(newJson, &newUpstream)
				upstreams = append(upstreams, newUpstream)
			}
		}
		project.Upstreams = upstreams

		for _, downstream := range project.JsonDownstreams {
			if downstream["type"] == "docker" {
				var newDownstream DockerStream
				newJson, err := json.Marshal(downstream)
				check(err)
				json.Unmarshal(newJson, &newDownstream)
				downstreams = append(downstreams, newDownstream)
			} else if downstream["type"] == "github" {
				var newDownstream GitHubStream
				newJson, err := json.Marshal(downstream)
				check(err)
				json.Unmarshal(newJson, &newDownstream)
				downstreams = append(downstreams, newDownstream)
			}
		}
		project.Downstreams = downstreams
		parsedProjects.Projects = append(parsedProjects.Projects, project)

	}

	return parsedProjects
}

func (image DockerStream) inspectDockerImageLabel() string {

	ctx := context.Background()
	imageString := image.Image + ":" + image.Tag

	cli, err := client.NewClientWithOpts()
	check(err)

	reader, err := cli.ImagePull(ctx, imageString, types.ImagePullOptions{})
	check(err)
	io.Copy(os.Stdout, reader)

	imageInspect, _, err := cli.ImageInspectWithRaw(ctx, imageString)
	check(err)

	if labelValue, ok := imageInspect.Config.Labels[image.Label]; ok {
		return labelValue
	}

	log.Printf("Label %s not found in image %s.", image.Label, imageString)
	return ""
}

func (repo GitHubStream) getLatestGitHubCommit() string {

	ctx := context.Background()

	client := github.NewClient(nil)
	repo_info := strings.Split(repo.Repo, "/")
	repo_owner := repo_info[0]
	repo_name := repo_info[1]
	branch, _, err := client.Repositories.GetBranch(ctx, repo_owner, repo_name, repo.Branch, true)
	check(err)
	return branch.GetCommit().GetSHA()
}

func main() {

	projects := readStreamsJson("streams.json")
	for _, project := range projects.Projects {
		upstreamRefs := make(map[string]string)
		for _, upstream := range project.Upstreams {
			switch stream := upstream.(type) {
			case DockerStream:
				upstreamRefs[stream.Name] = stream.inspectDockerImageLabel()
			case GitHubStream:
				upstreamRefs[stream.Name] = stream.getLatestGitHubCommit()
			}
		}
		downstreamRefs := make(map[string]string)
		for _, downstream := range project.Downstreams {
			switch stream := downstream.(type) {
			case DockerStream:
				downstreamRefs[stream.Name] = stream.inspectDockerImageLabel()
			case GitHubStream:
				downstreamRefs[stream.Name] = stream.getLatestGitHubCommit()
			}
		}

		updateNeeded := false
		for upstream, ref := range upstreamRefs {
			if downstreamRefs[upstream] != ref {
				updateNeeded = true
			}
		}
		if updateNeeded {
			projectInfo := strings.Split(project.Name, "/")
			owner := projectInfo[0]
			repo := projectInfo[1]
			client := github.NewClient(nil)
			workflowReq := github.CreateWorkflowDispatchEventRequest{
				Ref: project.Branch,
			}

			resp, err := client.Actions.CreateWorkflowDispatchEventByFileName(context.Background(), owner, repo, project.BuildWorkflow, workflowReq)
			check(err)
			log.Print(resp)
		} else {
			log.Print("No update needed!")
		}
	}
}
