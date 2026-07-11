package adminv1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

type userReadStore interface {
	GetUserByID(string) (*types.User, error)
	ListUsers(types.PaginationParams) ([]types.User, int, error)
	ListAllUsersCompact() ([]store.CompactUser, error)
}

type listUsersInput struct {
	Page    int    `query:"page" default:"1" minimum:"1" doc:"Page number, starting at one."`
	PerPage int    `query:"per_page" default:"20" minimum:"1" maximum:"100" doc:"Number of users returned per page."`
	Sort    string `query:"sort" default:"created_at" doc:"User field used for ordering."`
	Order   string `query:"order" default:"desc" enum:"asc,desc" doc:"Sort direction."`
}

func (input *listUsersInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

type listUsersOutput struct {
	Body ListResponse[User]
}

type UserCompact struct {
	ID       string `json:"id" format:"uuid"`
	Nickname string `json:"nickname,omitempty"`
	Email    string `json:"email,omitempty" format:"email"`
	Picture  string `json:"picture,omitempty" format:"uri"`
}

type UserCompactListResponse struct {
	Data []UserCompact `json:"data" nullable:"false"`
}

type listUsersCompactOutput struct {
	Body UserCompactListResponse
}

type userInput struct {
	// Keep this as an unconstrained string for v1 compatibility: the legacy
	// store maps malformed and missing IDs to the same 404 response.
	UserID string `path:"userID" doc:"User identifier."`
}

type userOutput struct {
	Body DataResponse[User]
}

func registerUserReadOperations(api huma.API, server *Server) {
	access := authz.System(authz.PermissionSystemUsersRead)
	listOperation := contract.Operation{
		ID:            "listUsers",
		Method:        http.MethodGet,
		Path:          "/api/v1/users",
		Summary:       "List users",
		Tags:          []string{"Users"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden},
		Access:        access,
		Authorize:     server.authorizationMiddleware,
	}
	registerWithLegacyTrailingSlash(api, listOperation, server.listUsers)

	contract.Register(api, contract.Operation{
		ID:            "listUsersCompact",
		Method:        http.MethodGet,
		Path:          "/api/v1/users/compact",
		Summary:       "List compact users",
		Description:   "Returns every user in a compact shape for management filters.",
		Tags:          []string{"Users"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden},
		Access:        access,
		Authorize:     server.authorizationMiddleware,
	}, server.listUsersCompact)

	contract.Register(api, contract.Operation{
		ID:            "getUser",
		Method:        http.MethodGet,
		Path:          "/api/v1/users/{userID}",
		Summary:       "Get user",
		Tags:          []string{"Users"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
		Access:        access,
		Authorize:     server.authorizationMiddleware,
	}, server.getUser)
}

func (s *Server) listUsers(_ context.Context, input *listUsersInput) (*listUsersOutput, error) {
	if s == nil || s.Users == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "user management store is not configured", nil)
	}
	pagination := input.pagination()
	users, total, err := s.Users.ListUsers(pagination)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list users", nil)
	}
	items := make([]User, len(users))
	for i := range users {
		items[i] = userDTO(&users[i])
	}
	return &listUsersOutput{Body: ListResponse[User]{
		Data: items,
		Meta: paginationMeta(total, pagination),
	}}, nil
}

func (s *Server) listUsersCompact(context.Context, *emptyInput) (*listUsersCompactOutput, error) {
	if s == nil || s.Users == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "user management store is not configured", nil)
	}
	users, err := s.Users.ListAllUsersCompact()
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list users", nil)
	}
	items := make([]UserCompact, len(users))
	for i, user := range users {
		items[i] = UserCompact{
			ID:       user.ID,
			Nickname: user.Nickname,
			Email:    user.Email,
			Picture:  user.Picture,
		}
	}
	return &listUsersCompactOutput{Body: UserCompactListResponse{Data: items}}, nil
}

func (s *Server) getUser(_ context.Context, input *userInput) (*userOutput, error) {
	if s == nil || s.Users == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "user management store is not configured", nil)
	}
	user, err := s.Users.GetUserByID(input.UserID)
	if err != nil || user == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "user not found", nil)
	}
	return &userOutput{Body: DataResponse[User]{Data: userDTO(user)}}, nil
}
