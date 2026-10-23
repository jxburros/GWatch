package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/update"
)

func TestPackageUpdateCommandNamesTheReleaseFile(t *testing.T) {
	cases := []struct{ by, latest, arch, want string }{
		{"deb", "0.8.0", "amd64", "sudo apt install ./gwatch_0.8.0-1_amd64.deb"},
		{"deb", "0.8.0", "arm", "gwatch_0.8.0-1_armhf.deb"},
		{"rpm", "0.8.0", "arm64", "sudo dnf install ./gwatch-0.8.0-1.aarch64.rpm"},
		{"rpm", "", "amd64", "gwatch-<version>-1.x86_64.rpm"},
		{"docker", "", "amd64", "docker compose pull"},
		{"something-new", "", "amd64", "the same way it was installed"},
	}
	for _, c := range cases {
		if got := packageUpdateCommand(c.by, c.latest, c.arch); !strings.Contains(got, c.want) {
			t.Errorf("packageUpdateCommand(%q, %q, %q) = %q, want it to contain %q", c.by, c.latest, c.arch, got, c.want)
		}
	}
}

// A packaged build reports updates but never installs one: the status says
// who owns it and what to run, and Apply refuses before reaching GitHub.
func TestPackagedUpdaterStepsAside(t *testing.T) {
	asked := 0
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"tag_name":"v9.9.9","html_url":"https://example.com","assets":[]}]`)
	}))
	defer gh.Close()
	u := &Updater{PackagedBy: "deb", Version: "0.7.0", Client: &update.Client{APIBase: gh.URL, HTTP: gh.Client()}}

	st := u.Status()
	if st.CanApply || st.PackagedBy != "deb" || st.PackageManager != "apt" || !strings.Contains(st.PackageCommand, "sudo apt install ./gwatch_<version>-1_") {
		t.Fatalf("status before a check: %+v", st)
	}
	if _, err := u.Check(context.Background(), "jxburros/GWatch"); err != nil {
		t.Fatal(err)
	}
	st = u.Status()
	if !strings.Contains(st.PackageCommand, "gwatch_9.9.9-1_") {
		t.Fatalf("the command should name the release the check found: %q", st.PackageCommand)
	}
	before := asked
	_, err := u.Apply(context.Background(), "jxburros/GWatch", "")
	if !errors.Is(err, ErrPackaged) || !strings.Contains(err.Error(), "apt") || !strings.Contains(err.Error(), "gwatch_9.9.9-1_") {
		t.Fatalf("apply: %v", err)
	}
	if asked != before {
		t.Fatal("a packaged build asked GitHub for something to install")
	}

	// An ordinary build says nothing about packages.
	plain := (&Updater{Client: &update.Client{}}).Status()
	if plain.PackagedBy != "" || plain.PackageCommand != "" {
		t.Fatalf("unpackaged status: %+v", plain)
	}
}
