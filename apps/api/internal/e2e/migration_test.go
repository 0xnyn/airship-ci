package e2e_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/github"
)

const workflowCI = `name: CI
on: [push]
jobs:
  test:
    runs-on: ubuntu-latest # hosted today
    steps:
      - run: make test
  matrix:
    strategy:
      matrix: {go: ["1.25", "1.26"]}
    runs-on: [ubuntu-24.04-arm]
    steps: []
  dynamic:
    runs-on: ${{ matrix.os }}
    steps: []
`

// The migration assistant reads a repository's workflows, previews a plan
// and opens a pull request with it, without the repository being
// activated first.
func TestMigrationAssistant(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gh.AddRepository(1, github.Repository{ID: 7, FullName: "acme/web", Private: true, DefaultBranch: "main"})
	e.gh.SetFile("acme/web", "main", ".github/workflows/ci.yml", []byte(workflowCI))
	e.gh.SetFile("acme/web", "main", ".github/workflows/notes.md", []byte("not a workflow"))
	e.gh.SetFile("acme/web", "main", ".github/workflows/broken.yaml", []byte("jobs: ["))
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)

	scan := e.call(http.MethodGet, "/v1/orgs/acme/repositories/7/workflows", nil, http.StatusOK)
	workflows := scan["workflows"].([]any)
	if len(workflows) != 2 || scan["config"] != nil || scan["defaultBranch"] != "main" {
		t.Fatalf("scan = %v", scan)
	}
	broken, ci := workflows[0].(map[string]any), workflows[1].(map[string]any)
	if broken["problem"] == nil || ci["name"] != "CI" {
		t.Fatalf("workflows = %v", workflows)
	}
	jobs := ci["jobs"].([]any)
	if len(jobs) != 3 || jobs[1].(map[string]any)["platform"] != "linux-arm64" || jobs[2].(map[string]any)["editable"] != false {
		t.Fatalf("jobs = %v", jobs)
	}
	e.call(http.MethodGet, "/v1/orgs/acme/repositories/99/workflows", nil, http.StatusNotFound)

	plan := map[string]any{
		"runners": []any{
			map[string]any{"name": "linux", "cloud": "aws", "instanceType": "c7i.xlarge", "machineImage": "ubuntu2404", "preemptible": true},
			map[string]any{"name": "arm", "cloud": "aws", "instanceType": "c7g.xlarge"},
		},
		"moves": []any{
			map[string]any{"workflow": ".github/workflows/ci.yml", "job": "test", "label": "airship-linux"},
			map[string]any{"workflow": ".github/workflows/ci.yml", "job": "matrix", "label": "airship-arm"},
		},
	}
	preview := e.call(http.MethodPost, "/v1/orgs/acme/repositories/7/migration/preview", plan, http.StatusOK)
	files := preview["files"].([]any)
	if len(files) != 2 || files[0].(map[string]any)["path"] != "airship.yaml" || files[0].(map[string]any)["created"] != true {
		t.Fatalf("preview = %v", preview)
	}
	after := files[1].(map[string]any)["after"].(string)
	if !strings.Contains(after, "runs-on: airship-linux--${{ github.run_id }} # hosted today") ||
		!strings.Contains(after, "runs-on: airship-arm--${{ github.run_id }}-${{ strategy.job-index }}") {
		t.Fatalf("ci.yml after:\n%s", after)
	}

	// A plan that cannot work is refused with a reason, before anything is written.
	bad := map[string]any{"moves": []any{map[string]any{"workflow": ".github/workflows/ci.yml", "job": "dynamic", "label": "airship-linux"}}}
	e.call(http.MethodPost, "/v1/orgs/acme/repositories/7/migration/preview", bad, http.StatusUnprocessableEntity)

	pr := e.call(http.MethodPost, "/v1/orgs/acme/repositories/7/migration/pull-request", plan, http.StatusCreated)
	if pr["number"] != float64(1) {
		t.Fatalf("pull request = %v", pr)
	}
	pulls := e.gh.PullRequests()
	if len(pulls) != 1 || pulls[0].Base != "main" || !strings.HasPrefix(pulls[0].Head, "airship/migrate-") || !strings.Contains(pulls[0].Body, "airship-arm") {
		t.Fatalf("pulls = %+v", pulls)
	}
	written, ok := e.gh.FileAt("acme/web", pulls[0].Head, ".github/workflows/ci.yml")
	if !ok || string(written) != after {
		t.Fatalf("written ci.yml:\n%s", written)
	}
	if _, ok := e.gh.FileAt("acme/web", pulls[0].Head, "airship.yaml"); !ok {
		t.Fatal("airship.yaml was not written")
	}
	if original, _ := e.gh.FileAt("acme/web", "main", ".github/workflows/ci.yml"); string(original) != workflowCI {
		t.Fatal("the default branch was changed")
	}

	// When GitHub refuses the write, the dashboard is told what to grant.
	e.gh.FailNext("PUT contents", http.StatusForbidden)
	refused := e.call(http.MethodPost, "/v1/orgs/acme/repositories/7/migration/pull-request", plan, http.StatusConflict)
	if !strings.Contains(refused["detail"].(string), "Workflows write") {
		t.Fatalf("refused = %v", refused)
	}
}
