package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRolesAndScopes(t *testing.T) {
	if ParseRole("Admin") != RoleAdmin || ParseRole("viewer") != RoleViewer || ParseRole("root") != RoleViewer || ParseRole("") != RoleViewer {
		t.Fatal("ParseRole must fail closed to viewer")
	}
	if !RoleAdmin.Valid() || !RoleViewer.Valid() || Role("root").Valid() {
		t.Fatal("Valid")
	}
	if ParseScope("readwrite") != ScopeReadWrite || ParseScope("read-write") != ScopeReadWrite || ParseScope("") != ScopeRead || ParseScope("admin") != ScopeRead {
		t.Fatal("ParseScope must fail closed to read")
	}
}

func TestPrincipalPermissions(t *testing.T) {
	cases := []struct {
		p       Principal
		isAdmin bool
		write   bool
		label   string
	}{
		{Anonymous, false, false, ""},
		{Principal{Kind: KindLocal, Role: RoleAdmin}, true, true, "local"},
		{Principal{Kind: KindPassword, Role: RoleAdmin}, true, true, "password"},
		{Principal{Kind: KindUser, Name: "pat", Role: RoleAdmin}, true, true, "pat (admin)"},
		{Principal{Kind: KindUser, Name: "sam", Role: RoleViewer}, false, false, "sam (viewer)"},
		// An API key is never an administrator, whatever role is attached.
		{Principal{Kind: KindAPIKey, Name: "HA", Role: RoleAdmin, Scope: ScopeReadWrite}, false, true, "api key HA (read-write)"},
		{Principal{Kind: KindAPIKey, Name: "HA", Role: RoleAdmin, Scope: ScopeRead}, false, false, "api key HA (read-only)"},
	}
	for _, c := range cases {
		if c.p.IsAdmin() != c.isAdmin {
			t.Errorf("%+v IsAdmin = %v", c.p, !c.isAdmin)
		}
		if c.p.CanWrite() != c.write {
			t.Errorf("%+v CanWrite = %v", c.p, !c.write)
		}
		if got := c.p.Label(); got != c.label {
			t.Errorf("%+v Label = %q want %q", c.p, got, c.label)
		}
	}
	if Anonymous.Authenticated() {
		t.Fatal("anonymous must not be authenticated")
	}
	if !(Principal{Kind: KindUser}).Authenticated() {
		t.Fatal("user must be authenticated")
	}
}

func TestContextRoundTrip(t *testing.T) {
	if FromContext(nil).Authenticated() || FromContext(context.Background()).Authenticated() {
		t.Fatal("bare context must be anonymous")
	}
	p := Principal{Kind: KindUser, Name: "pat", Role: RoleAdmin, UserID: 7}
	if got := FromContext(WithPrincipal(context.Background(), p)); got != p {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestPasswordHashing(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("short password must be rejected")
	}
	if err := ValidatePassword("long enough password"); err != nil {
		t.Fatal(err)
	}
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Fatalf("encoded form: %s", h)
	}
	if err := VerifyPassword(h, "correct horse battery"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyPassword(h, "wrong horse battery"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	// Two hashes of the same password differ (random salt).
	h2, _ := HashPassword("correct horse battery")
	if h2 == h {
		t.Fatal("salt is not random")
	}
	for _, bad := range []string{"", "plaintext", "$argon2i$v=19$m=1,t=1,p=1$YWJj$YWJj", "$argon2id$v=1$m=1,t=1,p=1$YWJj$YWJj", "$argon2id$v=19$m=0,t=0,p=0$YWJj$YWJj", "$argon2id$v=19$m=65536,t=1,p=4$!!!$YWJj"} {
		if err := VerifyPassword(bad, "x"); err == nil || errors.Is(err, ErrBadPassword) {
			t.Errorf("malformed hash %q must be an error, got %v", bad, err)
		}
	}
}

func TestTokens(t *testing.T) {
	a, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewSessionToken()
	if a == b || len(a) != 64 {
		t.Fatalf("session tokens: %q %q", a, b)
	}
	if HashToken(a) == a || len(HashToken(a)) != 64 || HashToken(a) != HashToken(a) {
		t.Fatal("HashToken")
	}
	key, prefix, err := NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "gw_") || len(key) != 43 {
		t.Fatalf("api key shape: %q", key)
	}
	if prefix != key[:KeyPrefixLen] || len(prefix) != KeyPrefixLen {
		t.Fatalf("prefix: %q", prefix)
	}
	if !LooksLikeAPIKey(key) || LooksLikeAPIKey("gw_") || LooksLikeAPIKey("hunter2") {
		t.Fatal("LooksLikeAPIKey")
	}
	key2, _, _ := NewAPIKey()
	if key2 == key {
		t.Fatal("api keys must be unique")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	defer l.Close()
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	ok, wait := l.Allow("1.2.3.4")
	if ok || wait <= 0 {
		t.Fatalf("4th attempt: ok=%v wait=%v", ok, wait)
	}
	// A different client is unaffected.
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("other IP must have its own bucket")
	}
	// Refill after the window.
	now = now.Add(time.Minute)
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("bucket should have refilled")
	}
	l.Reset("1.2.3.4")
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("after reset, attempt %d should be allowed", i)
		}
	}
	// Idle buckets are swept.
	now = now.Add(10 * time.Minute)
	l.sweep()
	if l.Size() != 0 {
		t.Fatalf("expected buckets to be swept, %d left", l.Size())
	}
	// A disabled limiter allows everything.
	var off *Limiter
	if ok, _ := off.Allow("x"); !ok {
		t.Fatal("nil limiter must allow")
	}
	zero := NewLimiter(0, time.Minute)
	defer zero.Close()
	if ok, _ := zero.Allow("x"); !ok {
		t.Fatal("zero limiter must allow")
	}
}
