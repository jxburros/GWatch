package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnflaggedPrereleaseStaysOffStableChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tag_name":"v9.0.0-rc.1"},{"tag_name":"v2.0.0"},{"tag_name":"agent-v9.0.0-rc.1"},{"tag_name":"agent-v2.0.0"}]`))
	}))
	defer srv.Close()
	for _, prefix := range []string{"v", "agent-v"} {
		c := &Client{APIBase: srv.URL, TagPrefix: prefix}
		rels, err := c.Releases(context.Background(), "owner/repo", "1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		if len(rels) != 2 || !rels[0].Prerelease || rels[1].Prerelease {
			t.Fatalf("releases: %+v", rels)
		}
		if got := Newest(rels, false); got == nil || got.Version != "2.0.0" {
			t.Fatalf("stable: %+v", got)
		}
		if got := Newest(rels, true); got == nil || got.Version != "9.0.0-rc.1" {
			t.Fatalf("preview: %+v", got)
		}
	}
}
