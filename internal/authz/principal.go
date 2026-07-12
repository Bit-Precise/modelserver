package authz

import (
	"fmt"
	"strings"
)

// Principal is the authenticated identity considered by the admin API
// authorizer. Superadmin is deliberately an explicit property; callers must
// never infer it from a missing project membership.
type Principal struct {
	UserID     string `json:"userId"`
	Superadmin bool   `json:"superadmin"`
}

// Authenticated reports whether the principal identifies a user.
func (p Principal) Authenticated() bool {
	return strings.TrimSpace(p.UserID) != ""
}

// Validate rejects contradictory principal state.
func (p Principal) Validate() error {
	if !p.Authenticated() {
		if p.Superadmin {
			return fmt.Errorf("authz: anonymous principal cannot be superadmin")
		}
		return fmt.Errorf("authz: principal user ID is required")
	}
	return nil
}
