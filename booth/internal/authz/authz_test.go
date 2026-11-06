package authz

import "testing"

// TestAuthorizeMatrix is the table-driven authz matrix required by ISI-7149:
// {Admin, DevRel} × {owned, unowned, admin-only-class, empty-owner}.
// The verb dimension (read/list/write/approve) is intentionally NOT encoded
// here: the §6.3 predicate is verb-agnostic — the same decision gates every
// verb — and the verb-level enforcement is covered in the store tests. Keeping
// this table verb-free makes it the single source of truth for the predicate.
func TestAuthorizeMatrix(t *testing.T) {
	const (
		devrel      = "sub-devrel-alice"
		otherDevrel = "sub-devrel-bob"
	)

	cases := []struct {
		name         string
		role         string
		subject      string
		adminOnly    bool
		ownerSubject string
		want         bool
	}{
		// --- Admin: clause 1 admits everything ---
		{"admin / owned-by-self", RoleAdmin, "sub-admin", false, "sub-admin", true},
		{"admin / owned-by-other", RoleAdmin, "sub-admin", false, otherDevrel, true},
		{"admin / admin-only-class", RoleAdmin, "sub-admin", true, "", true},
		{"admin / empty-owner", RoleAdmin, "sub-admin", false, "", true},

		// --- DevRel on owner-scoped classes ---
		{"devrel / owned-by-self", RoleDevRel, devrel, false, devrel, true},
		{"devrel / owned-by-other", RoleDevRel, devrel, false, otherDevrel, false},
		{"devrel / unowned (empty-owner)", RoleDevRel, devrel, false, "", false},

		// --- DevRel on admin-only classes: always denied, even if owner matches ---
		{"devrel / admin-only-class empty-owner", RoleDevRel, devrel, true, "", false},
		{"devrel / admin-only-class owner-matches", RoleDevRel, devrel, true, devrel, false},

		// --- Degenerate: empty subject must never match an empty owner ---
		{"empty-subject devrel / empty-owner", RoleDevRel, "", false, "", false},
		{"empty-subject devrel / owner set", RoleDevRel, "", false, "sub-x", false},

		// --- Unknown role is treated as non-admin (deny-by-default) ---
		{"unknown-role / owned-match", "Viewer", devrel, false, devrel, true},
		{"unknown-role / admin-only", "Viewer", devrel, true, devrel, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Authorize(tc.role, tc.subject, tc.adminOnly, tc.ownerSubject)
			if got != tc.want {
				t.Errorf("Authorize(role=%q, subject=%q, adminOnly=%v, owner=%q) = %v, want %v",
					tc.role, tc.subject, tc.adminOnly, tc.ownerSubject, got, tc.want)
			}
		})
	}
}

func TestSessionIsAdmin(t *testing.T) {
	if !(Session{Role: RoleAdmin}).IsAdmin() {
		t.Error("Admin session should report IsAdmin() == true")
	}
	if (Session{Role: RoleDevRel}).IsAdmin() {
		t.Error("DevRel session should report IsAdmin() == false")
	}
}
