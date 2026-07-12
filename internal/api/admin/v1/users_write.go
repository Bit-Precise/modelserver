package adminv1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
)

func registerUserWriteOperations(api huma.API, server *Server) {
	contract.Register(api, contract.Operation{
		ID:            "updateUser",
		Method:        http.MethodPut,
		Path:          "/api/v1/users/{userID}",
		Summary:       "Update user",
		Tags:          []string{"Users"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authz.System(authz.PermissionSystemUsersWrite),
		Authorize:     server.authorizationMiddleware,
	}, server.updateUser)
}

type UpdateUserInput struct {
	UserID string `path:"userID" doc:"User identifier."`
	Body   struct {
		Nickname     *string `json:"nickname,omitempty"`
		Status       *string `json:"status,omitempty" enum:"active,disabled"`
		IsSuperadmin *bool   `json:"is_superadmin,omitempty"`
		MaxProjects  *int    `json:"max_projects,omitempty" minimum:"0"`
	}
}

type UpdateUserOutput struct {
	Body DataResponse[User]
}

func (s *Server) updateUser(ctx context.Context, input *UpdateUserInput) (*UpdateUserOutput, error) {
	if s == nil || s.Auth == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "user write handler is not configured", nil)
	}

	updates := map[string]any{}
	if input.Body.Nickname != nil {
		updates["nickname"] = *input.Body.Nickname
	}
	if input.Body.Status != nil {
		updates["status"] = *input.Body.Status
	}
	if input.Body.IsSuperadmin != nil {
		updates["is_superadmin"] = *input.Body.IsSuperadmin
	}
	if input.Body.MaxProjects != nil {
		updates["max_projects"] = *input.Body.MaxProjects
	}
	if len(updates) == 0 {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "no valid fields to update", nil)
	}

	if err := s.Auth.UpdateUser(input.UserID, updates); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to update user", nil)
	}
	user, err := s.Auth.GetUserByID(input.UserID)
	if err != nil || user == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to update user", nil)
	}
	return &UpdateUserOutput{Body: DataResponse[User]{Data: userDTO(user)}}, nil
}
