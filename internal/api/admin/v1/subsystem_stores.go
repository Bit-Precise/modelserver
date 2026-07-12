package adminv1

import (
	"time"

	"github.com/modelserver/modelserver/internal/store"
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
// B — Users writes: PUT /users/{userID} is served by the typed handler
// via Server.Auth (see A — Auth), which already exposes UpdateUser +
// GetUserByID. No separate usersWriteStore interface is needed.

// C — Plans writes: READ and WRITE operations for plans. Consumed by
// Server.Plans; both read and write handlers.
type plansStore interface {
	ListPlansPaginated(types.PaginationParams) ([]types.Plan, int, error)
	GetPlanByID(string) (*types.Plan, error)
	CreatePlan(*types.Plan) error
	UpdatePlan(id string, updates map[string]any) error
	DeletePlan(id string) error
}
// D — Models catalog: READ and WRITE operations for the model catalog.
// Consumed by Server.Models; both read and write handlers.
type modelsStore interface {
	ListModels() ([]types.Model, error)
	ListModelsByStatus(status string) ([]types.Model, error)
	GetModelByName(name string) (*types.Model, error)
	CreateModel(*types.Model) error
	UpdateModel(name string, updates map[string]any) error
	DeleteModel(name string) error
	ModelReferenceCountsFor(name string) (store.ModelReferenceCounts, error)
}
// E — Admin global reads (superadmin)
type adminSuperStore interface {
	ListAllProjects(p types.PaginationParams, filters store.ProjectListFilters) ([]types.Project, int, error)
	GetActiveSubscriptionsByProjectIDs(projectIDs []string) (map[string]*types.Subscription, error)
	GetProjectOwnersByProjectIDs(projectIDs []string) (map[string]*types.User, error)
	SumCreditsSinceByProjects(periodStarts map[string]time.Time) (map[string]float64, error)
	SumCreditsInWindowByProjects(projectIDs []string, windowStart time.Time) (map[string]float64, error)
	ListPlans(activeOnly bool) ([]types.Plan, error)
	ListAllRequests(p types.PaginationParams, filters store.RequestFilters) ([]types.Request, int, error)
	GetRequest(id string) (*types.Request, error)
}

// F — Notifications (user + admin)
type notificationsStore interface {
	ListAllNotifications(includeDeleted bool, audienceType string, p types.PaginationParams) ([]types.Notification, int, error)
	GetNotification(id string) (*types.Notification, error)
	CreateNotification(*types.Notification) error
	UpdateNotification(id, title, body, audienceType string, audienceID *string) error
	SoftDeleteNotification(id string) error
	ListVisibleForUser(userID string, p types.PaginationParams) ([]types.Notification, int, error)
	CountUnreadForUser(userID string) (int, error)
	MarkNotificationRead(userID, notificationID string) error
	MarkAllNotificationsRead(userID string) (int, error)
	GetProjectByID(id string) (*types.Project, error)
	GetUserByID(id string) (*types.User, error)
}
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
