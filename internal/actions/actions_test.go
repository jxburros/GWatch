package actions

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

func TestExpandAndEnv(t *testing.T) {
	vars := Vars{"node.name": "Gateway", "status": "down", "message": `it's "quoted"`}
	got := Expand(`{{node.name}} is {{ status }}: {{message}} ({{missing}})`, vars)
	if got != `Gateway is down: it's "quoted" ()` {
		t.Fatalf("expand = %q", got)
	}
	if Expand("no placeholders", vars) != "no placeholders" {
		t.Fatal("plain string changed")
	}
	env := Env(Vars{"node.name": "Gateway", "check-id": "3"})
	want := []string{"GWATCH_CHECK_ID=3", "GWATCH_NODE_NAME=Gateway"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %v", env)
	}
}

func TestSplitArgs(t *testing.T) {
	got := SplitArgs(`pull --ff-only "origin main" 'a b'  c`)
	want := []string{"pull", "--ff-only", "origin main", "a b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitArgs = %q", got)
	}
	if len(SplitArgs("   ")) != 0 {
		t.Fatal("blank should yield no args")
	}
}

func TestStatusMatches(t *testing.T) {
	cases := []struct {
		code int
		spec string
		want bool
	}{
		{200, "200-399", true}, {404, "200-399", false}, {302, "200,301,302", true}, {500, "200,301,302", false}, {204, "204", true}, {200, " 200 - 299 ", true},
	}
	for _, c := range cases {
		if got := statusMatches(c.code, c.spec); got != c.want {
			t.Errorf("statusMatches(%d, %q) = %v", c.code, c.spec, got)
		}
	}
}

func TestValidate(t *testing.T) {
	bad := []model.Action{
		{Type: "nope"},
		{Type: model.ActionHTTP},
		{Type: model.ActionHTTP, URL: "ftp://x"},
		{Type: model.ActionGit, Repo: "/tmp"},
		{Type: model.ActionScript, Interpreter: "sh"},
		{Type: model.ActionScript, Interpreter: "custom", Code: "x"},
		{Type: model.ActionScript, Interpreter: "perl", Code: "x"},
		{Type: model.ActionRunNode},
		{Type: model.ActionHTTP, URL: "http://x", TimeoutSeconds: 100000},
	}
	for i, a := range bad {
		if err := Validate(a); err == nil {
			t.Errorf("case %d: expected error for %+v", i, a)
		}
	}
	id := int64(1)
	good := []model.Action{
		{Type: model.ActionHTTP, URL: "https://example.com/{{node.name}}"},
		{Type: model.ActionHTTP, URL: "{{query.url}}"},
		{Type: model.ActionGit, Repo: ".", GitArgs: "status"},
		{Type: model.ActionScript, Code: "echo hi"},
		{Type: model.ActionScript, Interpreter: "custom", Command: "cat", Code: "hi"},
		{Type: model.ActionRunNode, NodeID: &id},
	}
	for i, a := range good {
		if err := Validate(a); err != nil {
			t.Errorf("case %d: unexpected error %v", i, err)
		}
	}
}

func TestHTTPAction(t *testing.T) {
	var gotMethod, gotBody, gotHeader, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-Node")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if r.URL.Path == "/fail" {
			w.WriteHeader(500)
		}
		io.WriteString(w, "thanks")
	}))
	defer srv.Close()
	r := &Runner{}
	res := r.Run(context.Background(), model.Action{Type: model.ActionHTTP, URL: srv.URL + "/hook/{{node.name}}", Body: `{"status":"{{status}}"}`, Headers: map[string]string{"X-Node": "{{node.name}}"}}, Vars{"node.name": "gw", "status": "down"})
	if !res.OK || res.StatusCode != 200 || gotMethod != "POST" || gotPath != "/hook/gw" || gotBody != `{"status":"down"}` || gotHeader != "gw" || !strings.Contains(res.Output, "thanks") {
		t.Fatalf("http action: %+v method=%s path=%s body=%s header=%s", res, gotMethod, gotPath, gotBody, gotHeader)
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionHTTP, URL: srv.URL + "/x"}, nil)
	if !res.OK || gotMethod != "GET" {
		t.Fatalf("expected GET without body, got %s (%+v)", gotMethod, res)
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionHTTP, URL: srv.URL + "/fail", Method: "PUT", Body: "x"}, nil)
	if res.OK || res.StatusCode != 500 || !strings.Contains(res.Error, "unexpected HTTP status 500") {
		t.Fatalf("expected failure on 500: %+v", res)
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionHTTP, URL: srv.URL + "/fail", ExpectedStatus: "500"}, nil)
	if !res.OK {
		t.Fatalf("expected status override to accept 500: %+v", res)
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionHTTP, URL: "{{query.url}}"}, Vars{})
	if res.OK || !strings.Contains(res.Error, "http://") {
		t.Fatalf("expected bad URL error: %+v", res)
	}
}

func TestScriptAction(t *testing.T) {
	interp := "sh"
	if runtime.GOOS == "windows" {
		interp = "cmd"
	}
	if _, err := exec.LookPath(interp); err != nil {
		t.Skipf("%s not available", interp)
	}
	code := "echo node=$GWATCH_NODE_NAME status={{status}}\nexit 0"
	if interp == "cmd" {
		code = "@echo node=%GWATCH_NODE_NAME% status={{status}}"
	}
	r := &Runner{TempDir: t.TempDir()}
	res := r.Run(context.Background(), model.Action{Type: model.ActionScript, Interpreter: interp, Code: code}, Vars{"node.name": "nas", "status": "up"})
	if !res.OK || !strings.Contains(res.Output, "node=nas status=up") {
		t.Fatalf("script: %+v", res)
	}
	failing := "exit 3"
	if interp == "cmd" {
		failing = "@exit /b 3"
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionScript, Interpreter: interp, Code: failing}, nil)
	if res.OK || !strings.Contains(res.Error, "status 3") {
		t.Fatalf("expected exit status 3: %+v", res)
	}
	slow := "sleep 5"
	if interp == "cmd" {
		slow = "@ping -n 6 127.0.0.1 > nul"
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionScript, Interpreter: interp, Code: slow, TimeoutSeconds: 1}, nil)
	if res.OK || res.Error != "timed out" {
		t.Fatalf("expected timeout: %+v", res)
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionScript, Interpreter: "custom", Command: "definitely-not-a-program-gwatch", Code: "x"}, nil)
	if res.OK || !strings.Contains(res.Error, "not installed") {
		t.Fatalf("expected missing interpreter error: %+v", res)
	}
}

func TestGitAction(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if out, err := exec.Command(git, "init", "-q", dir).CombinedOutput(); err != nil {
		t.Skipf("git init failed: %s", out)
	}
	r := &Runner{}
	res := r.Run(context.Background(), model.Action{Type: model.ActionGit, Repo: dir, GitArgs: "git status --porcelain"}, nil)
	if !res.OK || !strings.HasPrefix(res.Output, "$ git status --porcelain") {
		t.Fatalf("git status: %+v", res)
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionGit, Repo: dir, GitArgs: "not-a-command"}, nil)
	if res.OK {
		t.Fatalf("expected failure: %+v", res)
	}
	res = r.Run(context.Background(), model.Action{Type: model.ActionGit, Repo: dir + "/missing", GitArgs: "status"}, nil)
	if res.OK || !strings.Contains(res.Error, "does not exist") {
		t.Fatalf("expected missing dir error: %+v", res)
	}
}

func TestRunNodeAction(t *testing.T) {
	var ran int64
	r := &Runner{RunNode: func(ctx context.Context, id int64) error { ran = id; return nil }}
	id := int64(7)
	res := r.Run(context.Background(), model.Action{Type: model.ActionRunNode, NodeID: &id}, nil)
	if !res.OK || ran != 7 || res.DurationMS < 0 || res.StartedAt.After(time.Now()) {
		t.Fatalf("run_node: %+v ran=%d", res, ran)
	}
	res = (&Runner{}).Run(context.Background(), model.Action{Type: model.ActionRunNode, NodeID: &id}, nil)
	if res.OK {
		t.Fatal("run_node without callback should fail")
	}
}
