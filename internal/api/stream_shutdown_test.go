package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An open event stream must not hold a graceful shutdown up until its
// timeout: Shutdown waits for every response to finish, and a stream only
// finishes when the server tells it to.
func TestShutdownEndsOpenStreams(t *testing.T) {
	_, srv := newTestServer(t)
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.Config.RegisterOnShutdown(srv.CloseStreams)
	ts.Start()
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := ts.Config.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown waited on the open stream: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
}
