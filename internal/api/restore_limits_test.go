package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
)

// An upload past the restore limit is refused while it is read, before it is
// copied anywhere or decrypted, and answered 413 rather than 400 or 500.
func TestRestoreUploadIsBounded(t *testing.T) {
	ts, _ := newTestServer(t)
	old := restoreUploadLimit
	restoreUploadLimit = 64 << 10
	t.Cleanup(func() { restoreUploadLimit = old })

	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	mp.WriteField("password", "pw")
	fw, _ := mp.CreateFormFile("file", "huge.gwbackup")
	fw.Write(bytes.Repeat([]byte{0x5a}, 256<<10))
	mp.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/backups/restore", &buf)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload: %d %s", resp.StatusCode, body)
	}
}
