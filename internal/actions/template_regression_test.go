package actions

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNodePlaceholderCannotExecuteInTemplateLiteral(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	for _, value := range []string{"${process.exit(42)}", "`;process.exit(42);//", "' + process.exit(42) + '", "a\\b\n雪"} {
		for _, template := range []string{"console.log({{message}})", "console.log(`{{message}}`)"} {
			code := expandScriptCode(template, Vars{"message": value}, "node")
			out, err := exec.Command(node, "-e", code).CombinedOutput()
			if err != nil {
				t.Fatalf("%q: %v: %s", code, err, out)
			}
			if !strings.Contains(string(out), value) {
				t.Fatalf("value changed: got %q want %q", out, value)
			}
		}
	}
}

// TestPythonPlaceholderCannotEscapeAuthorLiteral covers a placeholder the
// author wrapped in a single-quoted literal or an f-string: strconv.Quote
// leaves ' and { } bare, so a value could close the literal or be evaluated.
func TestPythonPlaceholderCannotEscapeAuthorLiteral(t *testing.T) {
	values := []string{"' + str(__import__('sys').exit(42)) + '", "{__import__('sys').exit(42)}", "a\\b\n雪\"'{}"}
	templates := []string{"print({{message}})", "print('{{message}}')", "print(f'{{message}}')"}
	for _, v := range values {
		for _, tpl := range templates {
			code := expandScriptCode(tpl, Vars{"message": v}, "python")
			inner := code[strings.Index(code, "\""):strings.LastIndex(code, "\"")]
			if strings.ContainsAny(inner, "'{}") {
				t.Fatalf("%q: value characters left bare in %q", tpl, code)
			}
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	for _, v := range values {
		for _, tpl := range templates {
			code := expandScriptCode(tpl, Vars{"message": v}, "python")
			cmd := exec.Command(python, "-c", code)
			// Windows Python writes to a pipe in the ANSI code page, which
			// cannot encode the test's non-ASCII value.
			cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%q: %v: %s", code, err, out)
			}
			if !strings.Contains(string(out), v) {
				t.Fatalf("%q: value changed: got %q want %q", code, out, v)
			}
		}
	}
}
