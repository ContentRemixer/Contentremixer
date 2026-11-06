package session

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
)

func TestBcryptAuthenticate(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	auth := NewBcryptAuthenticator(map[string]User{
		"alice": {Subject: "sub-alice", Role: authz.RoleDevRel, PasswordHash: hash},
	})

	cases := []struct {
		name     string
		user     string
		pass     string
		setBasic bool
		wantErr  bool
		wantSub  string
		wantRole string
	}{
		{"valid", "alice", "correct horse", true, false, "sub-alice", authz.RoleDevRel},
		{"wrong password", "alice", "nope", true, true, "", ""},
		{"unknown user", "mallory", "whatever", true, true, "", ""},
		{"no basic auth header", "", "", false, true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.setBasic {
				r.SetBasicAuth(tc.user, tc.pass)
			}
			sess, err := auth.Authenticate(r)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got session %+v", sess)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sess.Subject != tc.wantSub || sess.Role != tc.wantRole {
				t.Fatalf("got %+v, want subject=%q role=%q", sess, tc.wantSub, tc.wantRole)
			}
		})
	}
}

func TestSetUser(t *testing.T) {
	auth := NewBcryptAuthenticator(nil)
	hash, _ := HashPassword("pw")
	auth.SetUser("bob", User{Subject: "sub-bob", Role: authz.RoleAdmin, PasswordHash: hash})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("bob", "pw")
	sess, err := auth.Authenticate(r)
	if err != nil {
		t.Fatalf("authenticate after SetUser: %v", err)
	}
	if !sess.IsAdmin() {
		t.Fatalf("bob should be admin, got role %q", sess.Role)
	}
}
