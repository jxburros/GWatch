package actions

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// hostileValue contains every metacharacter that matters to a shell, Python or
// JavaScript, plus a command substitution whose side effect the tests check for.
func hostileValue(marker string) string {
	return "a`touch " + marker + "`b$(touch " + marker + ")c; touch " + marker + " | touch " + marker + " 'q' \"d\" \\e"
}

func scriptOutput(t *testing.T, interp, code string, vars Vars) model.ActionResult {
	t.Helper()
	r := &Runner{TempDir: t.TempDir()}
	return r.Run(context.Background(), model.Action{Type: model.ActionScript, Interpreter: interp, Code: code}, vars)
}

// TestScriptPlaceholdersCannotInjectCommands feeds shell metacharacters through
// {{body}} and {{message}} into every built-in interpreter and asserts the value
// arrives as literal text and that its command substitution never ran.
func TestScriptPlaceholdersCannotInjectCommands(t *testing.T) {
	cases := []struct {
		interp string
		bin    []string
		code   string
	}{
		{"sh", []string{"sh"}, "printf '%s' {{body}}\n"},
		{"bash", []string{"bash"}, "printf '%s' {{body}}\n"},
		{"python", []string{"python3", "python"}, "import sys\nsys.stdout.write({{body}})\n"},
		{"node", []string{"node"}, "process.stdout.write({{body}});\n"},
	}
	for _, c := range cases {
		t.Run(c.interp, func(t *testing.T) {
			if runtime.GOOS == "windows" && (c.interp == "sh" || c.interp == "bash") {
				t.Skip("posix shells only")
			}
			found := false
			for _, b := range c.bin {
				if _, err := exec.LookPath(b); err == nil {
					found = true
				}
			}
			if !found {
				t.Skipf("%s not available", c.interp)
			}
			dir := t.TempDir()
			marker := filepath.Join(dir, "pwned")
			value := hostileValue(marker)
			res := scriptOutput(t, c.interp, c.code, Vars{"body": value})
			if !res.OK {
				t.Fatalf("script failed: %+v", res)
			}
			if res.Output != value {
				t.Fatalf("value was not passed through literally:\n got %q\nwant %q", res.Output, value)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the placeholder value executed a command")
			}
		})
	}
}

// TestScriptPlaceholdersAreReferencesNotText checks the substituted text itself,
// so the guarantee holds on platforms where the interpreter is not installed.
func TestScriptPlaceholdersAreReferencesNotText(t *testing.T) {
	vars := Vars{"message": "x\"; rm -rf /\n", "node.name": "gw"}
	cases := map[string]string{
		"sh":         `echo "${GWATCH_MESSAGE}"`,
		"bash":       `echo "${GWATCH_MESSAGE}"`,
		"powershell": `echo ${env:GWATCH_MESSAGE}`,
		"python":     `echo "x\"; rm -rf /\n"`,
		"node":       `echo "x\"; rm -rf /\n"`,
	}
	for interp, want := range cases {
		if got := expandScriptCode("echo {{message}}", vars, interp); got != want {
			t.Errorf("%s: got %q want %q", interp, got, want)
		}
	}
	// cmd has no safe reference, so the value is stripped of what cmd re-parses
	if got := expandScriptCode("@echo {{message}}", Vars{"message": `a&b|c<d>e^f%g!h"i`}, "cmd"); got != "@echo abcdefghi" {
		t.Errorf("cmd: got %q", got)
	}
	// unknown names become an empty literal, never a dangling reference
	for _, interp := range []string{"sh", "bash", "powershell", "python", "node"} {
		if got := expandScriptCode("echo {{nope}}", vars, interp); got != `echo ""` {
			t.Errorf("%s unknown placeholder: got %q", interp, got)
		}
	}
}

// TestCustomInterpreterRequiresAcknowledgement covers the case where GWatch
// cannot know how to quote a value: the language is whatever the user typed.
func TestCustomInterpreterRequiresAcknowledgement(t *testing.T) {
	a := model.Action{Type: model.ActionScript, Interpreter: "custom", Command: "cat", Code: "print({{body}})"}
	if err := Validate(a); err == nil || !strings.Contains(err.Error(), "GWATCH_") {
		t.Fatalf("expected the placeholder to be rejected, got %v", err)
	}
	a.AllowUntrustedInput = true
	if err := Validate(a); err != nil {
		t.Fatalf("acknowledged action should validate: %v", err)
	}
	a.AllowUntrustedInput = false
	a.Code = "print(os.environ['GWATCH_BODY'])"
	if err := Validate(a); err != nil {
		t.Fatalf("code without placeholders should validate: %v", err)
	}
	if _, err := exec.LookPath("cat"); err != nil {
		return
	}
	// with the acknowledgement the value is spliced in raw, as chosen
	r := &Runner{TempDir: t.TempDir()}
	res := r.Run(context.Background(), model.Action{Type: model.ActionScript, Interpreter: "custom", Command: "cat", Code: "{{body}}", AllowUntrustedInput: true}, Vars{"body": "raw $(x)"})
	if !res.OK || res.Output != "raw $(x)" {
		t.Fatalf("custom interpreter: %+v", res)
	}
}

// TestBuildGitArgs makes sure a placeholder value stays one argument and cannot
// turn into an option.
func TestBuildGitArgs(t *testing.T) {
	vars := Vars{"message": `oops" --upload-pack=touch /tmp/pwned "`, "branch": "main", "opt": "--exec=evil"}
	got, err := buildGitArgs(`git commit -am "{{message}}"`, vars)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"commit", "-am", vars["message"]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q want %q", got, want)
	}
	if got, err := buildGitArgs("push origin {{branch}}", vars); err != nil || !reflect.DeepEqual(got, []string{"push", "origin", "main"}) {
		t.Fatalf("args = %q err = %v", got, err)
	}
	if _, err := buildGitArgs("status {{opt}}", vars); err == nil || !strings.Contains(err.Error(), "git option") {
		t.Fatalf("expected an option to be refused, got %v", err)
	}
	if got, err := buildGitArgs("status --short {{missing}}", vars); err != nil || !reflect.DeepEqual(got, []string{"status", "--short", ""}) {
		t.Fatalf("args = %q err = %v", got, err)
	}
}

// TestHeaderValuesCannotInjectHeaders covers the HTTP action: header values are
// data, but must not be able to start a new header line.
func TestHeaderValuesCannotInjectHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	r := &Runner{}
	res := r.Run(context.Background(), model.Action{Type: model.ActionHTTP, URL: srv.URL, Headers: map[string]string{"X-Note": "{{message}}"}},
		Vars{"message": "hi\r\nX-Injected: yes"})
	if !res.OK {
		t.Fatalf("http action: %+v", res)
	}
	if got.Get("X-Injected") != "" {
		t.Fatal("a header value injected another header")
	}
	if got.Get("X-Note") != "hiX-Injected: yes" {
		t.Fatalf("X-Note = %q", got.Get("X-Note"))
	}
}
