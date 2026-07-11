package authz

// SystemOnProjectPath returns an AccessPolicy that requires an explicit
// superadmin (system scope) but is mounted at a route with a project path
// parameter. Runtime authorization is identical to System(); the project
// path parameter is used only for audit context and resource resolution
// on writes such as subscription overrides.
//
// The permission must be system-scoped. The project path parameter must
// be non-blank. Validate() enforces both.
func SystemOnProjectPath(permission Permission, projectIDPathParam string) AccessPolicy {
	access := System(permission)
	access.ProjectIDPathParam = projectIDPathParam
	access.systemOnProjectPath = true
	return access
}
