//go:build windows

package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/jxburros/GWatch/internal/model"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDownloadedWindowsExecutableRuns(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pinKeys(t, pub)
	digest := sha256.Sum256(binary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent.exe":
			w.Write(binary)
		case "/agent.exe.sig":
			fmt.Fprint(w, FormatSignature(pub, SignDigest(priv, digest[:])))
		case "/agent.exe.sha256":
			fmt.Fprintf(w, "%x", digest)
		}
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client()}
	path, err := client.Download(context.Background(), model.UpdateInfo{AssetName: "agent.exe", AssetURL: server.URL + "/agent.exe", AssetSize: int64(len(binary))}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".exe" {
		t.Fatalf("download cannot execute without extension: %s", path)
	}
	if output, err := exec.Command(path, "-test.run=^$").CombinedOutput(); err != nil {
		t.Fatalf("downloaded binary: %v: %s", err, output)
	}
}
