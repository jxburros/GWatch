// Package actions executes automation actions: HTTP requests (webhooks),
// git commands, custom scripts and "run this node now". Triggers and custom
// endpoints both use it.
//
// Every string field of an action may contain {{placeholders}} which are
// expanded from the Vars passed at run time, so a webhook body can carry the
// node name, the new status and the message of the event that fired it.
package actions

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Vars are the values substituted for {{name}} placeholders. Keys use dots
// (node.name, check.name, status, message, event ...).
type Vars map[string]string

// RunNodeFunc runs every check of a node; it is supplied by the engine.
type RunNodeFunc func(ctx context.Context, nodeID int64) error

// Runner executes actions.
type Runner struct {
	RunNode RunNodeFunc
	// HTTPClient is used for http actions. A default client is created when nil.
	HTTPClient *http.Client
	// TempDir receives script files (os.TempDir when empty).
	TempDir string
}

const (
	defaultTimeout = 30 * time.Second
	maxTimeout     = 10 * time.Minute
	maxOutput      = 64 << 10
)

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\-]+)\s*\}\}`)

// Expand substitutes {{placeholders}} in s. Unknown names expand to "".
func Expand(s string, vars Vars) string {
	if s == "" || !strings.Contains(s, "{{") {
		return s
	}
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.TrimSpace(m[2 : len(m)-2])
		if v, ok := vars[name]; ok {
			return v
		}
		return ""
	})
}

// hasPlaceholder reports whether s contains a {{placeholder}}.
func hasPlaceholder(s string) bool {
	return strings.Contains(s, "{{") && placeholder.MatchString(s)
}

// cmdUnsafe are the characters cmd.exe re-parses; they are dropped from values
// spliced into a cmd script (see expandScriptCode).
var cmdUnsafe = strings.NewReplacer("&", "", "|", "", "<", "", ">", "", "^", "", "%", "", "!", "", "\"", "", "\r", "", "\n", " ")

// expandScriptCode substitutes {{placeholders}} inside the code of a script
// action. Unlike Expand it never splices the raw value into the code: a value
// can be anything an HTTP caller or a monitored device sent, so it is replaced
// by a reference or a literal the interpreter cannot re-parse as code.
//
//	sh, bash     "${GWATCH_NODE_NAME}"   (a double-quoted expansion is never re-parsed)
//	powershell   ${env:GWATCH_NODE_NAME}
//	python       a Python string literal (strconv.Quote emits only escapes Python shares)
//	node         a JavaScript string literal (json.Marshal)
//	cmd          a sanitized literal - cmd.exe re-parses %VAR% and !VAR! expansions,
//	             so there is no safe reference to use; & | < > ^ % ! " are dropped
//	             from the value and newlines become spaces.
//
// Unknown names expand to an empty literal of the right kind. Note that a
// placeholder the author wrapped in their own quotes ("{{message}}") yields
// ""${GWATCH_MESSAGE}"" in sh: still a single expansion that is never re-parsed
// as code, but subject to word splitting. Write {{message}} without quotes.
func expandScriptCode(code string, vars Vars, interp string) string {
	if !strings.Contains(code, "{{") {
		return code
	}
	return placeholder.ReplaceAllStringFunc(code, func(m string) string {
		key := strings.TrimSpace(m[2 : len(m)-2])
		val, known := vars[key]
		switch interp {
		case "sh", "bash":
			if !known {
				return `""`
			}
			return "\"${" + EnvName(key) + "}\""
		case "powershell":
			if !known {
				return `""`
			}
			return "${env:" + EnvName(key) + "}"
		case "cmd":
			return cmdUnsafe.Replace(val)
		case "python":
			return strconv.Quote(val)
		case "node":
			b, err := json.Marshal(val)
			if err != nil {
				return `""`
			}
			return string(b)
		}
		return val
	})
}

// EnvName is the environment variable a placeholder name is exported as
// (node.name → GWATCH_NODE_NAME). The placeholder syntax only allows letters,
// digits, dots and dashes, so the result is always a valid variable name.
func EnvName(key string) string {
	return "GWATCH_" + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
}

// Env converts vars into GWATCH_* environment variables (node.name → GWATCH_NODE_NAME).
func Env(vars Vars) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, EnvName(k)+"="+vars[k])
	}
	return out
}

// Validate checks that an action is complete enough to run.
func Validate(a model.Action) error {
	if !a.Type.Valid() {
		return fmt.Errorf("unsupported action type %q", a.Type)
	}
	switch a.Type {
	case model.ActionHTTP:
		u := strings.TrimSpace(a.URL)
		if u == "" {
			return errors.New("a URL is required")
		}
		if !strings.Contains(u, "{{") && !strings.HasPrefix(strings.ToLower(u), "http://") && !strings.HasPrefix(strings.ToLower(u), "https://") {
			return errors.New("the URL must start with http:// or https://")
		}
	case model.ActionGit:
		if strings.TrimSpace(a.Repo) == "" {
			return errors.New("a repository directory is required")
		}
		if strings.TrimSpace(a.GitArgs) == "" {
			return errors.New("git arguments are required, e.g. pull --ff-only")
		}
	case model.ActionScript:
		if strings.TrimSpace(a.Code) == "" {
			return errors.New("the script code is empty")
		}
		if a.Interpreter == "custom" && strings.TrimSpace(a.Command) == "" {
			return errors.New("a command line is required for a custom interpreter")
		}
		if a.Interpreter == "custom" && !a.AllowUntrustedInput && hasPlaceholder(a.Code) {
			return errors.New("with a custom interpreter GWatch cannot quote placeholder values safely: use the GWATCH_* environment variables instead of {{placeholders}} in the code, or tick \"this script may run untrusted input\" to expand them as raw text")
		}
		if _, ok := interpreters[a.Interpreter]; !ok && a.Interpreter != "custom" && a.Interpreter != "" {
			return fmt.Errorf("unknown interpreter %q", a.Interpreter)
		}
	case model.ActionRunNode:
		if a.NodeID == nil || *a.NodeID <= 0 {
			return errors.New("a node is required")
		}
	case model.ActionSlack, model.ActionTeams, model.ActionNtfy, model.ActionPushover:
		if err := ValidateNotify(a); err != nil {
			return err
		}
	}
	if a.TimeoutSeconds < 0 || time.Duration(a.TimeoutSeconds)*time.Second > maxTimeout {
		return fmt.Errorf("timeout must be between 0 and %d seconds", int(maxTimeout.Seconds()))
	}
	return nil
}

// Run executes the action and never panics; failures are reported in the result.
func (r *Runner) Run(ctx context.Context, a model.Action, vars Vars) (res model.ActionResult) {
	start := time.Now()
	res.StartedAt = start
	defer func() {
		if p := recover(); p != nil {
			res.OK = false
			res.Error = fmt.Sprintf("internal error: %v", p)
		}
		res.DurationMS = time.Since(start).Milliseconds()
		if len(res.Output) > maxOutput {
			res.Output = res.Output[:maxOutput] + "\n… (truncated)"
		}
	}()
	if err := Validate(a); err != nil {
		res.Error = err.Error()
		return res
	}
	timeout := defaultTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if vars == nil {
		vars = Vars{}
	}
	switch a.Type {
	case model.ActionHTTP:
		return r.runHTTP(ctx, a, vars, res)
	case model.ActionGit:
		return r.runGit(ctx, a, vars, res)
	case model.ActionScript:
		return r.runScript(ctx, a, vars, res)
	case model.ActionRunNode:
		if r.RunNode == nil {
			res.Error = "running nodes is not available here"
			return res
		}
		if err := r.RunNode(ctx, *a.NodeID); err != nil {
			res.Error = err.Error()
			return res
		}
		res.OK = true
		res.Output = fmt.Sprintf("Ran the checks of node %d.", *a.NodeID)
		return res
	case model.ActionSlack:
		return r.runSlack(ctx, a, vars, res)
	case model.ActionTeams:
		return r.runTeams(ctx, a, vars, res)
	case model.ActionNtfy:
		return r.runNtfy(ctx, a, vars, res)
	case model.ActionPushover:
		return r.runPushover(ctx, a, vars, res)
	}
	res.Error = "unsupported action"
	return res
}

// ---- http ----

func (r *Runner) client(a model.Action) *http.Client {
	if r.HTTPClient != nil && !a.IgnoreTLSErrors {
		return r.HTTPClient
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: a.IgnoreTLSErrors}, //nolint:gosec // user opted in per action
	}
	return &http.Client{Transport: tr}
}

func (r *Runner) runHTTP(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	method := strings.ToUpper(strings.TrimSpace(Expand(a.Method, vars)))
	if method == "" {
		method = "POST"
		if strings.TrimSpace(a.Body) == "" {
			method = "GET"
		}
	}
	url := strings.TrimSpace(Expand(a.URL, vars))
	if !strings.HasPrefix(strings.ToLower(url), "http://") && !strings.HasPrefix(strings.ToLower(url), "https://") {
		res.Error = "the URL must start with http:// or https:// (after expanding placeholders): " + url
		return res
	}
	body := Expand(a.Body, vars)
	var rdr io.Reader
	if body != "" && method != "GET" && method != "HEAD" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "GWatch-automation")
	if rdr != nil && a.Headers["Content-Type"] == "" && a.Headers["content-type"] == "" {
		if strings.HasPrefix(strings.TrimSpace(body), "{") || strings.HasPrefix(strings.TrimSpace(body), "[") {
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	for k, v := range a.Headers {
		if k = strings.TrimSpace(k); k != "" {
			// Header values are data, but an expanded placeholder must not be
			// able to inject extra header lines.
			req.Header.Set(k, headerSafe(Expand(v, vars)))
		}
	}
	resp, err := r.client(a).Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxOutput))
	res.StatusCode = resp.StatusCode
	res.Output = fmt.Sprintf("%s %s → %s\n%s", method, url, resp.Status, strings.TrimSpace(string(data)))
	expected := strings.TrimSpace(a.ExpectedStatus)
	if expected == "" {
		expected = "200-399"
	}
	if !statusMatches(resp.StatusCode, expected) {
		res.Error = fmt.Sprintf("unexpected HTTP status %d (expected %s)", resp.StatusCode, expected)
		return res
	}
	res.OK = true
	return res
}

// headerSafe strips CR and LF from an expanded header value.
func headerSafe(v string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(v) }

// statusMatches implements "200", "200-299", "200,301,302" and combinations.
func statusMatches(code int, spec string) bool {
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			l, err1 := strconv.Atoi(strings.TrimSpace(lo))
			h, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 == nil && err2 == nil && code >= l && code <= h {
				return true
			}
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n == code {
			return true
		}
	}
	return false
}

// ---- git ----

func (r *Runner) runGit(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	dir := strings.TrimSpace(Expand(a.Repo, vars))
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		res.Error = fmt.Sprintf("repository directory %q does not exist", dir)
		return res
	}
	args, err := buildGitArgs(a.GitArgs, vars)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if len(args) == 0 {
		res.Error = "no git arguments"
		return res
	}
	git, err := exec.LookPath("git")
	if err != nil {
		res.Error = "git is not installed or not on PATH"
		return res
	}
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append(Env(vars), "GIT_TERMINAL_PROMPT=0")...)
	out, err := cmd.CombinedOutput()
	res.Output = fmt.Sprintf("$ git %s\n%s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	if err != nil {
		res.Error = describeExecError(ctx, err)
		return res
	}
	res.OK = true
	return res
}

// buildGitArgs splits the configured arguments first and expands placeholders
// per argument afterwards, so a value containing spaces or quotes can never add
// arguments of its own. A value that turns a plain argument into an option is
// refused rather than passed on.
func buildGitArgs(gitArgs string, vars Vars) ([]string, error) {
	raw := SplitArgs(gitArgs)
	out := make([]string, 0, len(raw))
	for _, arg := range raw {
		ex := Expand(arg, vars)
		if ex != arg && strings.HasPrefix(ex, "-") && !strings.HasPrefix(arg, "-") {
			return nil, errors.New("placeholder value would be interpreted as a git option: " + ex)
		}
		out = append(out, ex)
	}
	if len(out) > 0 && strings.EqualFold(out[0], "git") {
		out = out[1:]
	}
	return out, nil
}

// SplitArgs splits a command line into arguments honouring single and double quotes.
func SplitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := rune(0)
	has := false
	flush := func() {
		if has {
			out = append(out, cur.String())
			cur.Reset()
			has = false
		}
	}
	for _, r := range s {
		switch {
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			inQuote = r
			has = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return out
}

// ---- script ----

type interpreter struct {
	ext  string
	cmds [][]string // candidates tried in order; {{file}} marks the script path
}

var interpreters = map[string]interpreter{
	"sh":         {ext: ".sh", cmds: [][]string{{"sh", "{{file}}"}}},
	"bash":       {ext: ".sh", cmds: [][]string{{"bash", "{{file}}"}}},
	"powershell": {ext: ".ps1", cmds: [][]string{{"pwsh", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", "{{file}}"}, {"powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", "{{file}}"}}},
	"cmd":        {ext: ".cmd", cmds: [][]string{{"cmd", "/C", "{{file}}"}}},
	"python":     {ext: ".py", cmds: [][]string{{"python3", "{{file}}"}, {"python", "{{file}}"}, {"py", "{{file}}"}}},
	"node":       {ext: ".js", cmds: [][]string{{"node", "{{file}}"}}},
}

// DefaultInterpreter is used when none is given.
func DefaultInterpreter() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "sh"
}

// Interpreters lists the built-in interpreter names.
func Interpreters() []string {
	return []string{"sh", "bash", "powershell", "cmd", "python", "node", "custom"}
}

func (r *Runner) runScript(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	name := a.Interpreter
	if name == "" {
		name = DefaultInterpreter()
	}
	var candidates [][]string
	ext := ".txt"
	if name == "custom" {
		candidates = [][]string{SplitArgs(a.Command)}
		if !strings.Contains(a.Command, "{{file}}") {
			candidates[0] = append(candidates[0], "{{file}}")
		}
		if e := strings.TrimSpace(a.Repo); e != "" { // unused for scripts, kept for symmetry
			_ = e
		}
	} else {
		ip, ok := interpreters[name]
		if !ok {
			res.Error = fmt.Sprintf("unknown interpreter %q", name)
			return res
		}
		candidates, ext = ip.cmds, ip.ext
	}
	dir := r.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "gwatch-script-*"+ext)
	if err != nil {
		res.Error = "cannot create script file: " + err.Error()
		return res
	}
	path := f.Name()
	defer os.Remove(path)
	code := a.Code
	if name == "custom" {
		// The language is unknown, so no quoting rule applies. Raw expansion is
		// only reached when the author explicitly acknowledged that the script
		// may run untrusted input (Validate rejects placeholders otherwise).
		if a.AllowUntrustedInput {
			code = Expand(code, vars)
		}
	} else {
		code = expandScriptCode(code, vars, name)
	}
	if runtime.GOOS == "windows" && (ext == ".cmd" || ext == ".ps1") {
		code = strings.ReplaceAll(strings.ReplaceAll(code, "\r\n", "\n"), "\n", "\r\n")
	}
	if _, err := f.WriteString(code); err != nil {
		f.Close()
		res.Error = "cannot write script file: " + err.Error()
		return res
	}
	f.Close()
	_ = os.Chmod(path, 0o700)

	var exe string
	var args []string
	for _, cand := range candidates {
		if len(cand) == 0 {
			continue
		}
		if p, err := exec.LookPath(cand[0]); err == nil {
			exe = p
			for _, c := range cand[1:] {
				args = append(args, strings.ReplaceAll(c, "{{file}}", path))
			}
			break
		}
	}
	if exe == "" {
		res.Error = fmt.Sprintf("interpreter for %q is not installed or not on PATH", name)
		return res
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	if wd := strings.TrimSpace(Expand(a.WorkDir, vars)); wd != "" {
		cmd.Dir = wd
	} else {
		cmd.Dir = filepath.Dir(path)
	}
	cmd.Env = append(os.Environ(), Env(vars)...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Stdin = strings.NewReader(vars["body"])
	err = cmd.Run()
	res.Output = strings.TrimSpace(buf.String())
	if err != nil {
		res.Error = describeExecError(ctx, err)
		return res
	}
	res.OK = true
	return res
}

func describeExecError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timed out"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("exited with status %d", ee.ExitCode())
	}
	return err.Error()
}
