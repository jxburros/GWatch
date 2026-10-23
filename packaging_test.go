package main

import (
	"strings"
	"testing"
)

func TestPackagedBuildRefusesServiceRegistration(t *testing.T) {
	for _, by := range []string{"deb", "rpm"} {
		for _, cmd := range []string{"install", "uninstall"} {
			msg := packagedServiceRefusal(by, []string{cmd, "--listen", "0.0.0.0:7230"})
			if msg == "" || !strings.Contains(msg, "gwatch service") {
				t.Errorf("%s build, gwatch %s: %q", by, cmd, msg)
			}
		}
		for _, args := range [][]string{nil, {"run"}, {"version"}, {"status"}, {"restart"}, {"--listen", "x"}} {
			if msg := packagedServiceRefusal(by, args); msg != "" {
				t.Errorf("%s build, %v should be allowed: %q", by, args, msg)
			}
		}
	}
	if !strings.Contains(packagedServiceRefusal("deb", []string{"uninstall"}), "apt remove gwatch") {
		t.Error("the deb refusal should name apt")
	}
	// The release binary, the container image and the others keep their
	// own install and uninstall.
	for _, by := range []string{"", "docker", "homebrew", "winget"} {
		if msg := packagedServiceRefusal(by, []string{"install"}); msg != "" {
			t.Errorf("packagedBy=%q: %q", by, msg)
		}
	}
}
