package authz

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Scope describes the authorization boundary of an operation.
type Scope string

const (
	ScopeNone    Scope = "none"
	ScopeSystem  Scope = "system"
	ScopeProject Scope = "project"
)

// Valid reports whether s is a recognized scope.
func (s Scope) Valid() bool {
	switch s {
	case ScopeNone, ScopeSystem, ScopeProject:
		return true
	default:
		return false
	}
}

// AccessMode describes how an operation is authenticated and authorized.
type AccessMode string

const (
	AccessModePublic        AccessMode = "public"
	AccessModeAuthenticated AccessMode = "authenticated"
	AccessModeRBAC          AccessMode = "rbac"
	AccessModeHMAC          AccessMode = "hmac"
)

// Valid reports whether m is a recognized access mode.
func (m AccessMode) Valid() bool {
	switch m {
	case AccessModePublic, AccessModeAuthenticated, AccessModeRBAC, AccessModeHMAC:
		return true
	default:
		return false
	}
}

// SuperadminRule makes superadmin behavior explicit. In particular, a missing
// project membership must never be interpreted as superadmin access.
type SuperadminRule string

const (
	SuperadminNone     SuperadminRule = "none"
	SuperadminRequired SuperadminRule = "required"
	SuperadminBypass   SuperadminRule = "bypass"
)

// ResourceBinding tells an HTTP adapter how to locate a resource referenced
// by an operation. ResourceType selects a ResourceResolver; IDPathParam names
// the path parameter containing the resource ID.
type ResourceBinding struct {
	ResourceType string `json:"type"`
	IDPathParam  string `json:"idPathParam"`
}

// AccessPolicy is the single authorization descriptor attached to an API
// operation. It is suitable both for runtime authorization and for generating
// OpenAPI vendor-extension metadata.
type AccessPolicy struct {
	Mode               AccessMode       `json:"mode"`
	Scope              Scope            `json:"scope"`
	Permission         Permission       `json:"permission,omitempty"`
	ProjectIDPathParam string           `json:"projectIdPathParam,omitempty"`
	Resource           *ResourceBinding `json:"resource,omitempty"`
	Policies           []PolicyID       `json:"conditions,omitempty"`
	Superadmin         SuperadminRule   `json:"superadmin"`

	// systemOnProjectPath is set only by SystemOnProjectPath(). It permits a
	// non-empty ProjectIDPathParam under system scope for audit + resolver
	// hookup. Kept unexported so callers cannot forge the combination.
	systemOnProjectPath bool
}

// UnmarshalJSON implements json.Unmarshaler. It restores the unexported
// systemOnProjectPath flag when the decoded fields match the exact shape that
// SystemOnProjectPath produces, so that an AccessPolicy round-tripped through
// JSON (e.g. via the x-modelserver-authz OpenAPI extension) still passes
// Validate().
func (a *AccessPolicy) UnmarshalJSON(data []byte) error {
	type raw AccessPolicy // avoids infinite recursion
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*a = AccessPolicy(r)
	if a.Mode == AccessModeRBAC &&
		a.Scope == ScopeSystem &&
		a.Superadmin == SuperadminRequired &&
		strings.TrimSpace(a.ProjectIDPathParam) != "" {
		a.systemOnProjectPath = true
	}
	return nil
}

// Public declares an operation that needs no authentication.
func Public() AccessPolicy {
	return AccessPolicy{
		Mode:       AccessModePublic,
		Scope:      ScopeNone,
		Superadmin: SuperadminNone,
	}
}

// Authenticated declares an operation that accepts any authenticated user.
func Authenticated() AccessPolicy {
	return AccessPolicy{
		Mode:       AccessModeAuthenticated,
		Scope:      ScopeNone,
		Superadmin: SuperadminNone,
	}
}

// HMAC declares an operation authenticated by an HMAC middleware rather than
// an end-user Principal.
func HMAC() AccessPolicy {
	return AccessPolicy{
		Mode:       AccessModeHMAC,
		Scope:      ScopeNone,
		Superadmin: SuperadminNone,
	}
}

// System declares a system-scoped RBAC operation. Only a Principal whose
// Superadmin flag is explicitly true may satisfy this policy.
func System(permission Permission, options ...AccessOption) AccessPolicy {
	access := AccessPolicy{
		Mode:       AccessModeRBAC,
		Scope:      ScopeSystem,
		Permission: permission,
		Superadmin: SuperadminRequired,
	}
	for _, option := range options {
		option(&access)
	}
	return access
}

// Project declares a project-scoped RBAC operation. Superadmins bypass project
// role grants by default, but the bypass can be disabled with
// RequireProjectMembership.
func Project(permission Permission, projectIDPathParam string, options ...AccessOption) AccessPolicy {
	access := AccessPolicy{
		Mode:               AccessModeRBAC,
		Scope:              ScopeProject,
		Permission:         permission,
		ProjectIDPathParam: projectIDPathParam,
		Superadmin:         SuperadminBypass,
	}
	for _, option := range options {
		option(&access)
	}
	return access
}

// AccessOption customizes an RBAC access policy.
type AccessOption func(*AccessPolicy)

// WithResource associates a resource resolver with an operation.
func WithResource(resourceType, idPathParam string) AccessOption {
	return func(access *AccessPolicy) {
		access.Resource = &ResourceBinding{
			ResourceType: resourceType,
			IDPathParam:  idPathParam,
		}
	}
}

// WithPolicies attaches resource or relationship policies to an operation.
func WithPolicies(policies ...PolicyID) AccessOption {
	return func(access *AccessPolicy) {
		access.Policies = append([]PolicyID(nil), policies...)
	}
}

// RequireProjectMembership disables the usual superadmin bypass. Use it for
// operations whose invariant requires a real project membership.
func RequireProjectMembership() AccessOption {
	return func(access *AccessPolicy) {
		access.Superadmin = SuperadminNone
	}
}

// Validate rejects incomplete or contradictory descriptors. Registration code
// should validate every operation during process startup and spec generation.
func (a AccessPolicy) Validate() error {
	if !a.Mode.Valid() {
		return fmt.Errorf("authz: invalid access mode %q", a.Mode)
	}
	if !a.Scope.Valid() {
		return fmt.Errorf("authz: invalid scope %q", a.Scope)
	}

	switch a.Mode {
	case AccessModePublic, AccessModeAuthenticated, AccessModeHMAC:
		if a.Scope != ScopeNone {
			return fmt.Errorf("authz: %s access must use scope %q", a.Mode, ScopeNone)
		}
		if a.Permission != "" || a.ProjectIDPathParam != "" || a.Resource != nil || len(a.Policies) != 0 {
			return fmt.Errorf("authz: %s access cannot declare RBAC metadata", a.Mode)
		}
		if a.Superadmin != SuperadminNone {
			return fmt.Errorf("authz: %s access cannot grant special superadmin behavior", a.Mode)
		}
		return nil
	case AccessModeRBAC:
		return a.validateRBAC()
	default:
		panic("unreachable")
	}
}

func (a AccessPolicy) validateRBAC() error {
	permissionScope, ok := a.Permission.Scope()
	if !ok {
		return fmt.Errorf("authz: unknown permission %q", a.Permission)
	}
	if permissionScope != a.Scope {
		return fmt.Errorf("authz: permission %q has scope %q, not %q", a.Permission, permissionScope, a.Scope)
	}

	switch a.Scope {
	case ScopeSystem:
		if a.Superadmin != SuperadminRequired {
			return fmt.Errorf("authz: system access must explicitly require superadmin")
		}
		if a.systemOnProjectPath {
			if strings.TrimSpace(a.ProjectIDPathParam) == "" {
				return fmt.Errorf("authz: SystemOnProjectPath requires a project path parameter")
			}
		} else if a.ProjectIDPathParam != "" {
			return fmt.Errorf("authz: system access cannot declare a project path parameter")
		}
	case ScopeProject:
		if strings.TrimSpace(a.ProjectIDPathParam) == "" {
			return fmt.Errorf("authz: project access requires a project path parameter")
		}
		if a.Superadmin != SuperadminBypass && a.Superadmin != SuperadminNone {
			return fmt.Errorf("authz: project access has invalid superadmin rule %q", a.Superadmin)
		}
	default:
		return fmt.Errorf("authz: RBAC access cannot use scope %q", a.Scope)
	}

	if a.Resource != nil {
		if strings.TrimSpace(a.Resource.ResourceType) == "" {
			return fmt.Errorf("authz: resource type is required")
		}
		if strings.TrimSpace(a.Resource.IDPathParam) == "" {
			return fmt.Errorf("authz: resource ID path parameter is required")
		}
	}

	seen := make(map[PolicyID]struct{}, len(a.Policies))
	for _, policy := range a.Policies {
		if strings.TrimSpace(string(policy)) == "" {
			return fmt.Errorf("authz: policy ID is required")
		}
		if _, exists := seen[policy]; exists {
			return fmt.Errorf("authz: duplicate policy %q", policy)
		}
		seen[policy] = struct{}{}
	}

	return nil
}
