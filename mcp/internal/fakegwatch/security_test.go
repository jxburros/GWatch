package fakegwatch

import (
	"bytes"
	"net/http"
	"testing"
)

func TestAPIKeysCannotExecuteCustomChecks(t *testing.T) {
	s := New()
	defer s.Close()
	s.AddKey("key", "test", ScopeReadWrite)
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/nodes", `{"name":"bad","host":"localhost","checks":[{"type":"custom","name":"script"}]}`},
		{"PUT", "/api/v1/nodes/1", `{"name":"bad","host":"localhost","checks":[{"type":"custom","name":"script"}]}`},
		{"POST", "/api/v1/checks/test", `{"nodeHost":"localhost","check":{"type":"custom","config":{"command":"echo bad"}}}`},
	} {
		req, err := http.NewRequest(tc.method, s.URL+tc.path, bytes.NewBufferString(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-API-Key", "key")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("%s %s: %d", tc.method, tc.path, res.StatusCode)
		}
	}
}
