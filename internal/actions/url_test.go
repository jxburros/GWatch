package actions

import (
	"net/url"
	"testing"
)

func TestURLPlaceholdersAreComponentData(t *testing.T) {
	input := "https://example.com/{{x}}?value={{x}}"
	hostile := "a/b?other=1&admin=true#fragment"
	got, err := expandURL(input, Vars{"x": hostile})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "example.com" || u.Fragment != "" || len(u.Query()) != 1 || u.Query().Get("value") != hostile || u.EscapedPath() != "/"+url.PathEscape(hostile) {
		t.Fatalf("unsafe expansion: %s", got)
	}
	for _, input := range []string{"{{scheme}}://example.com/path", "https://{{host}}/path", "https://example.com{{x}}/path"} {
		if _, err := expandURL(input, Vars{"x": ".evil.com"}); err == nil {
			t.Fatalf("accepted authority placeholder: %s", input)
		}
	}
}
