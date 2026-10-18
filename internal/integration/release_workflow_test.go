package integration

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type publicationWorkflow struct {
	On          map[string]any            `yaml:"on"`
	Permissions map[string]string         `yaml:"permissions"`
	Jobs        map[string]publicationJob `yaml:"jobs"`
}

type publicationJob struct {
	Uses            string            `yaml:"uses"`
	Needs           any               `yaml:"needs"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	With            map[string]string `yaml:"with"`
	Outputs         map[string]string `yaml:"outputs"`
	Permissions     map[string]string `yaml:"permissions"`
	Steps           []publicationStep `yaml:"steps"`
}

type publicationStep struct {
	ID              string            `yaml:"id"`
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	With            map[string]string `yaml:"with"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
}

func readPublicationWorkflow(t *testing.T, name string) publicationWorkflow {
	t.Helper()
	body, err := os.ReadFile("../../.github/workflows/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var workflow publicationWorkflow
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func workflowNeeds(job publicationJob, required string) bool {
	if name, ok := job.Needs.(string); ok {
		return name == required
	}
	names, _ := job.Needs.([]any)
	return slices.Contains(names, any(required))
}

// Default job success semantics are intentional: overriding `if` or allowing
// check failures would change GitHub's failure/cancellation/skip propagation.
func publicationGateError(workflow publicationWorkflow, publishID, ref string) error {
	publish := workflow.Jobs[publishID]
	if publish.If != "" || publish.ContinueOnError {
		return fmt.Errorf("publication must retain default success dependency handling")
	}
	for name, path := range map[string]string{"ci": "ci.yml", "security": "security-scan.yml"} {
		job := workflow.Jobs[name]
		if !workflowNeeds(publish, name) || job.Uses != "./.github/workflows/"+path {
			return fmt.Errorf("publication must depend on the complete %s workflow", name)
		}
		if job.If != "" || job.ContinueOnError || job.With["ref"] != ref {
			return fmt.Errorf("%s must check the publication SHA without bypasses", name)
		}
	}
	found := false
	for _, step := range publish.Steps {
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			if step.With["ref"] != ref {
				return fmt.Errorf("publication checkout differs from checked SHA")
			}
			found = true
		}
	}
	if found {
		return nil
	}
	return fmt.Errorf("publication checkout is missing")
}

func TestPublicationWorkflowsGateTheExactCommit(t *testing.T) {
	for _, tc := range []struct{ file, publish, ref string }{
		{"release.yml", "build-and-release", "${{ github.sha }}"},
		{"npm-publish.yml", "publish", "${{ needs.resolve.outputs.sha }}"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			workflow := readPublicationWorkflow(t, tc.file)
			if err := publicationGateError(workflow, tc.publish, tc.ref); err != nil {
				t.Fatal(err)
			}
			if workflow.Permissions["contents"] != "read" || workflow.Permissions["id-token"] != "" {
				t.Fatal("check jobs must not inherit publication permissions")
			}
			if workflow.Jobs[tc.publish].Permissions["id-token"] != "write" {
				t.Fatal("npm provenance requires publication-only OIDC permission")
			}
			for id, job := range workflow.Jobs {
				for _, step := range job.Steps {
					if (strings.Contains(step.Run, "npm publish") || strings.Contains(step.Run, "gh release create")) && id != tc.publish {
						t.Fatalf("ungated publication job %s", id)
					}
				}
			}
			if tc.file == "npm-publish.yml" {
				for _, id := range []string{"ci", "security", "publish"} {
					if !workflowNeeds(workflow.Jobs[id], "resolve") {
						t.Fatalf("%s does not depend on tag resolution", id)
					}
				}
				resolve := workflow.Jobs["resolve"]
				if resolve.Outputs["sha"] != "${{ steps.commit.outputs.sha }}" || resolve.If != "" || resolve.ContinueOnError {
					t.Fatal("tag resolution must produce a required commit output")
				}
				found := false
				for _, step := range resolve.Steps {
					found = found || (step.ID == "commit" && strings.Contains(step.Run, "git rev-parse HEAD"))
				}
				if !found {
					t.Fatal("tag must resolve to the checked-out commit")
				}
			}
		})
	}
}

func TestPublicationGateRejectsBypasses(t *testing.T) {
	for _, mutation := range []string{"missing CI", "missing security", "always publish", "ignored check failure", "skipped security", "wrong checked commit", "moving publish ref"} {
		t.Run(mutation, func(t *testing.T) {
			workflow := readPublicationWorkflow(t, "release.yml")
			publish, ci, security := workflow.Jobs["build-and-release"], workflow.Jobs["ci"], workflow.Jobs["security"]
			switch mutation {
			case "missing CI":
				publish.Needs = "security"
			case "missing security":
				publish.Needs = "ci"
			case "always publish":
				publish.If = "always()"
			case "ignored check failure":
				ci.ContinueOnError = true
			case "skipped security":
				security.If = "false"
			case "wrong checked commit":
				ci.With["ref"] = "main"
			case "moving publish ref":
				publish.Steps[0].With["ref"] = "${{ github.ref }}"
			}
			workflow.Jobs["build-and-release"], workflow.Jobs["ci"], workflow.Jobs["security"] = publish, ci, security
			if err := publicationGateError(workflow, "build-and-release", "${{ github.sha }}"); err == nil {
				t.Fatal("accepted a publication gate bypass")
			}
		})
	}
}

func TestReusableChecksRunTheRequestedCommit(t *testing.T) {
	for _, tc := range []struct {
		file string
		jobs []string
	}{
		{"ci.yml", []string{"test", "golangci-lint", "smoke"}},
		{"security-scan.yml", []string{"govulncheck"}},
	} {
		workflow := readPublicationWorkflow(t, tc.file)
		if _, ok := workflow.On["workflow_call"]; !ok {
			t.Fatalf("%s is not reusable", tc.file)
		}
		for _, id := range tc.jobs {
			job, ok := workflow.Jobs[id]
			if !ok || job.If != "" || job.ContinueOnError {
				t.Fatalf("%s must run required job %s", tc.file, id)
			}
			found := false
			for _, step := range job.Steps {
				if step.If != "" || step.ContinueOnError {
					t.Fatalf("%s/%s may skip a check or ignore its failure", tc.file, id)
				}
				if strings.HasPrefix(step.Uses, "actions/checkout@") {
					found = step.With["ref"] == "${{ inputs.ref || github.sha }}"
				}
			}
			if !found {
				t.Fatalf("%s/%s does not check the requested SHA", tc.file, id)
			}
		}
	}
}
