package actions

import (
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
