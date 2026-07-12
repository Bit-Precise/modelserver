package adminv1

import (
	"github.com/modelserver/modelserver/internal/types"
)

// Per-subsystem store interfaces. Each subsystem migration in Batches
// 2..14 grows its own interface with the exact methods it needs. Keep
// the interfaces empty until the corresponding batch starts so unused
// methods do not accumulate on *Server.
//
// The subsystem letters correspond to the table in
// docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md,
// section §2.

// A — Auth (public): user lookup / creation and OAuth-connection persistence
// used by POST /auth/refresh and POST /auth/oauth/{provider}.
type authStore interface {
	GetUserByID(id string) (*types.User, error)
	GetUserByEmail(email string) (*types.User, error)
	GetUserByOAuth(provider, providerID string) (*types.User, error)
	CreateUser(user *types.User) error
	UpdateUser(id string, updates map[string]any) error
	CreateOAuthConnection(userID, provider, providerID string) error
	UserExists() (bool, error)
	CreateProject(project *types.Project) error
}
type usersWriteStore interface{}    // B — Users writes
type plansWriteStore interface{}    // C — Plans writes
type modelsStore interface{}        // D — Models catalog
type adminSuperStore interface{}    // E — Admin (superadmin)
type notificationsStore interface{} // F — Notifications user + admin
type extraUsageStore interface{}    // G — Extra usage user + admin
type projectsStore interface{}      // H — Projects CRUD
type membersStore interface{}       // I — Project members
type keysStore interface{}          // J — API Keys
type oauthGrantsStore interface{}   // K — OAuth grants
type policiesStore interface{}      // L — Project policies
type subscriptionsStore interface{} // M — Subscriptions
type ordersStore interface{}        // N — Available plans + Orders
type tracesStore interface{}        // O — Traces
type requestsStore interface{}      // P — Requests + http-log
type usageStore interface{}         // Q — Usage
type upstreamsStore interface{}     // R — Upstreams
type upstreamGroupsStore interface{} // S — Upstream groups
type oauthClientsStore interface{}  // T — OAuth clients
type routingStore interface{}       // U — Routing
type selfProjectStore interface{}   // V — My-quota / my-membership
