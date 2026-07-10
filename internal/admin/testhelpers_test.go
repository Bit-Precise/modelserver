package admin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

type adminTestUser struct {
	user  *types.User
	token string
}

// openTestAdminStore opens a store connected to TEST_DATABASE_URL, runs
// migrations, and registers a cleanup. Skips the test if TEST_DATABASE_URL is
// unset so `go test ./...` stays green without a database.
func openTestAdminStore(t *testing.T) *store.Store {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set TEST_DATABASE_URL to run (e.g. postgres://user:pass@localhost:5432/testdb?sslmode=disable)")
	}
	st, err := store.New(dbURL, slog.Default())
	if err != nil {
		t.Fatalf("openTestAdminStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedAdminTestUser inserts a users row (with is_superadmin flag set as
// requested), signs an access token, and returns an adminTestUser.
func seedAdminTestUser(t *testing.T, st *store.Store, superadmin bool) adminTestUser {
	t.Helper()

	u := &types.User{
		Email:        "testuser-" + randomSuffix() + "@test.local",
		IsSuperadmin: superadmin,
		MaxProjects:  10,
		Status:       types.UserStatusActive,
	}
	if err := st.CreateUser(u); err != nil {
		t.Fatalf("seedAdminTestUser CreateUser: %v", err)
	}

	// Ensure is_superadmin is set correctly via direct SQL (CreateUser may not
	// honour the field if the column has a DB default).
	if _, err := st.Pool().Exec(context.Background(),
		`UPDATE users SET is_superadmin = $1 WHERE id = $2`, superadmin, u.ID,
	); err != nil {
		t.Fatalf("seedAdminTestUser update is_superadmin: %v", err)
	}
	// Re-fetch so the returned user has the correct flag.
	fetched, err := st.GetUserByID(u.ID)
	if err != nil || fetched == nil {
		t.Fatalf("seedAdminTestUser GetUserByID: %v", err)
	}

	jwtMgr := auth.NewJWTManager("test-jwt-secret-key-at-least-32-bytes", 15*time.Minute, 168*time.Hour)
	access, _, err := jwtMgr.GenerateTokenPair(fetched.ID, fetched.Email, fetched.IsSuperadmin)
	if err != nil {
		t.Fatalf("seedAdminTestUser GenerateTokenPair: %v", err)
	}
	return adminTestUser{user: fetched, token: access}
}

// newAdminTestRouter builds a minimal chi.Router that mounts:
//   - /admin/notifications (RequireSuperadmin-gated) for Task 3 tests
//   - /notifications placeholder for Task 4 user-facing tests
//
// Both JWTAuthMiddleware and RequireSuperadmin use the same test JWT secret as
// seedAdminTestUser so tokens validate correctly.
func newAdminTestRouter(t *testing.T, st *store.Store) chi.Router {
	t.Helper()

	jwtMgr := auth.NewJWTManager("test-jwt-secret-key-at-least-32-bytes", 15*time.Minute, 168*time.Hour)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(JWTAuthMiddleware(jwtMgr, st))

		// Admin notifications CRUD (superadmin only).
		r.Route("/admin/notifications", func(r chi.Router) {
			r.Use(RequireSuperadmin)
			r.Get("/", handleListAllNotifications(st))
			r.Post("/", handleCreateNotification(st))
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", handleGetNotification(st))
				r.Put("/", handleUpdateNotification(st))
				r.Delete("/", handleDeleteNotification(st))
			})
		})

		// User-facing notifications (Task 4 will fill this in).
		r.Route("/notifications", func(r chi.Router) {
			// placeholder — Task 4 handlers will be mounted here
		})
	})
	return r
}

// randomSuffix returns a short random hex string for unique test emails.
func randomSuffix() string {
	b := make([]byte, 4)
	// Use time-based suffix to avoid import of crypto/rand.
	now := time.Now().UnixNano()
	b[0] = byte(now >> 24)
	b[1] = byte(now >> 16)
	b[2] = byte(now >> 8)
	b[3] = byte(now)
	return fmt.Sprintf("%x", b)
}
