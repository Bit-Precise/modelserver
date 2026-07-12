package adminv1

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/modelserver/modelserver/internal/api/admin/v1/resolvers"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/config"
	"github.com/modelserver/modelserver/internal/types"
)

type managementStore interface {
	GetUserByID(string) (*types.User, error)
	GetProjectMember(projectID, userID string) (*types.ProjectMember, error)
	ListUserProjects(userID string, pagination types.PaginationParams) ([]types.Project, int, error)
	GetProjectByID(id string) (*types.Project, error)
}

type tokenValidator interface {
	ValidateToken(string) (*auth.Claims, error)
}

// Server contains runtime dependencies for typed management operations. Nil
// dependencies are valid during offline contract generation because handlers
// and middleware are registered but never executed.
type Server struct {
	Store     managementStore
	Users     userReadStore
	Plans     planReadStore
	Tokens    tokenValidator
	Auth      authStore
	JWT       *auth.JWTManager
	EncKey    []byte
	Config    *config.Config
	Resolvers map[string]authz.ResourceResolver
	Policies  map[authz.PolicyID]authz.Policy
}

// effectivePolicies returns the caller-supplied policies map when set,
// falling back to DefaultPolicies(). Callers must not mutate the result.
func (s *Server) effectivePolicies() map[authz.PolicyID]authz.Policy {
	if s.Policies != nil {
		return s.Policies
	}
	return DefaultPolicies()
}

// effectiveResolvers returns the caller-supplied resolver registry when
// set, falling back to the shared default registry. Callers must not
// mutate the result.
func (s *Server) effectiveResolvers() map[string]authz.ResourceResolver {
	if s.Resolvers != nil {
		return s.Resolvers
	}
	return resolvers.Default()
}

type requestAuthorization struct {
	Principal authz.Principal
	User      *types.User
	Role      authz.ProjectRole
	ProjectID string
	Resource  *authz.Resource
}

type requestAuthorizationKey struct{}

func authorizationFromContext(ctx context.Context) (requestAuthorization, bool) {
	value, ok := ctx.Value(requestAuthorizationKey{}).(requestAuthorization)
	return value, ok
}

func (s *Server) authorizationMiddleware(access authz.AccessPolicy) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if s == nil || s.Store == nil || s.Tokens == nil {
			contract.WriteError(ctx, contract.NewError(http.StatusInternalServerError, "internal", "management API authentication is not configured", nil))
			return
		}

		token := bearerToken(ctx.Header("Authorization"))
		if token == "" {
			contract.WriteError(ctx, contract.NewError(http.StatusUnauthorized, "unauthorized", "missing or invalid authorization header", nil))
			return
		}
		claims, err := s.Tokens.ValidateToken(token)
		if err != nil || claims == nil {
			contract.WriteError(ctx, contract.NewError(http.StatusUnauthorized, "unauthorized", "invalid or expired token", nil))
			return
		}
		if claims.TokenType != "access" {
			contract.WriteError(ctx, contract.NewError(http.StatusUnauthorized, "unauthorized", "expected access token", nil))
			return
		}
		user, err := s.Store.GetUserByID(claims.UserID)
		if err != nil || user == nil {
			contract.WriteError(ctx, contract.NewError(http.StatusUnauthorized, "unauthorized", "user not found", nil))
			return
		}
		if user.Status != types.UserStatusActive {
			contract.WriteError(ctx, contract.NewError(http.StatusForbidden, "forbidden", "user account is disabled", nil))
			return
		}

		authorization := requestAuthorization{
			Principal: authz.Principal{UserID: user.ID, Superadmin: user.IsSuperadmin},
			User:      user,
		}
		if access.Mode == authz.AccessModeRBAC {
			if !s.authorizeRBAC(ctx, access, &authorization) {
				return
			}
		}

		next(huma.WithValue(ctx, requestAuthorizationKey{}, authorization))
	}
}

func (s *Server) authorizeRBAC(ctx huma.Context, access authz.AccessPolicy, authorization *requestAuthorization) bool {
	switch access.Scope {
	case authz.ScopeSystem:
		if !authz.HasSystemPermission(authorization.Principal, access.Permission) {
			// Preserve the legacy RequireSuperadmin error message while the
			// system-scoped routes move to the central permission catalog.
			contract.WriteError(ctx, contract.NewError(http.StatusForbidden, "forbidden", "superadmin access required", nil))
			return false
		}
		return true
	case authz.ScopeProject:
		projectID := ctx.Param(access.ProjectIDPathParam)
		if projectID == "" {
			contract.WriteError(ctx, contract.NewError(http.StatusBadRequest, "bad_request", "missing project ID", nil))
			return false
		}
		if _, err := uuid.Parse(projectID); err != nil {
			contract.WriteError(ctx, contract.NewError(http.StatusUnprocessableEntity, "bad_request", "invalid project ID", nil))
			return false
		}
		authorization.ProjectID = projectID

		if authorization.Principal.Superadmin && access.Superadmin == authz.SuperadminBypass {
			return s.resolveAndEvaluatePolicies(ctx, access, authorization)
		}

		member, err := s.Store.GetProjectMember(projectID, authorization.Principal.UserID)
		if err != nil {
			contract.WriteError(ctx, contract.NewError(http.StatusInternalServerError, "internal", "failed to check project membership", nil))
			return false
		}
		if member == nil {
			contract.WriteError(ctx, contract.NewError(http.StatusForbidden, "forbidden", "you are not a member of this project", nil))
			return false
		}
		role := authz.ProjectRole(member.Role)
		if !role.Valid() || !authz.HasProjectPermission(authorization.Principal, role, access.Permission) {
			contract.WriteError(ctx, contract.NewError(http.StatusForbidden, "forbidden", "insufficient permissions", nil))
			return false
		}
		authorization.Role = role
		return s.resolveAndEvaluatePolicies(ctx, access, authorization)
	default:
		contract.WriteError(ctx, contract.NewError(http.StatusInternalServerError, "internal", "invalid authorization scope", nil))
		return false
	}
}

func (s *Server) resolveAndEvaluatePolicies(ctx huma.Context, access authz.AccessPolicy, authorization *requestAuthorization) bool {
	if access.Resource != nil {
		resolver := s.effectiveResolvers()[access.Resource.ResourceType]
		if resolver == nil {
			contract.WriteError(ctx, contract.NewError(http.StatusInternalServerError, "internal", "resource authorization is not configured", nil))
			return false
		}
		resource, err := resolver.Resolve(ctx.Context(), authz.ResourceReference{
			Type:      access.Resource.ResourceType,
			ID:        ctx.Param(access.Resource.IDPathParam),
			ProjectID: authorization.ProjectID,
		})
		if err != nil || resource.ID == "" || resource.ProjectID != authorization.ProjectID {
			contract.WriteError(ctx, contract.NewError(http.StatusNotFound, "not_found", "resource not found", nil))
			return false
		}
		authorization.Resource = &resource
	}

	for _, policyID := range access.Policies {
		policy := s.effectivePolicies()[policyID]
		if policy == nil {
			contract.WriteError(ctx, contract.NewError(http.StatusInternalServerError, "internal", "authorization policy is not configured", nil))
			return false
		}
		allowed, err := policy.Evaluate(ctx.Context(), authz.PolicyInput{
			Principal: authorization.Principal,
			Access:    access,
			Role:      authorization.Role,
			ProjectID: authorization.ProjectID,
			Resource:  authorization.Resource,
		})
		if err != nil || !allowed {
			contract.WriteError(ctx, contract.NewError(http.StatusForbidden, "forbidden", "insufficient permissions", nil))
			return false
		}
	}
	return true
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
