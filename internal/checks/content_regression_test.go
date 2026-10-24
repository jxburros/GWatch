package checks

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

func TestContentWatchEmptyBaseline(t *testing.T) {
	for _, mode := range []string{"header", "json"} {
		t.Run(mode, func(t *testing.T) {
			value := "v1"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if value != "" {
					w.Header().Set("X-Version", value)
				}
				if value == "" {
					w.Write([]byte(`{}`))
				} else {
					w.Write([]byte(`{"version":"` + value + `"}`))
				}
			}))
			defer srv.Close()
			c := httpCheck(model.CheckHTTP, srv.URL, model.CheckConfig{ContentWatch: mode, ContentHeader: "X-Version", JSONPath: "version"})
			opts := Options{}
			for i, step := range []struct {
				value   string
				changed bool
			}{{"v1", false}, {"", true}, {"", false}, {"v2", true}, {"v2", false}} {
				value = step.value
				r := run(t, c, opts)
				if !r.Success || r.Details.ContentChanged != step.changed || r.Details.ContentHash == "" {
					t.Fatalf("step %d: %+v", i, r)
				}
				opts.PreviousValue, opts.PreviousHash = r.Details.ContentValue, r.Details.ContentHash
			}
		})
	}
}

// A json check whose body is not JSON parses it once for the check and again
// for the content watch; the second look must not see a document either.
func TestContentWatchJSONOnInvalidBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>maintenance</html>`))
	}))
	defer srv.Close()
	c := httpCheck(model.CheckJSON, srv.URL, model.CheckConfig{ContentWatch: "json"})
	r := run(t, c, Options{})
	if r.Details.ContentValue != "" {
		t.Fatalf("watched value of a non-JSON body = %q, want empty", r.Details.ContentValue)
	}
}
