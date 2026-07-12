package adminv1

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/types"
)

// ListMyNotificationsInput holds the pagination query parameters for the
// current user's notification inbox.
type ListMyNotificationsInput struct {
	Page    int    `query:"page" default:"1" minimum:"1"`
	PerPage int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
	Sort    string `query:"sort" default:"created_at"`
	Order   string `query:"order" default:"desc" enum:"asc,desc"`
}

func (input *ListMyNotificationsInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

// ListMyNotificationsOutput is the response envelope for GET /api/v1/notifications.
type ListMyNotificationsOutput struct {
	Body ListResponse[types.Notification]
}

// unreadCountData is the inner data object for UnreadNotificationCountOutput.
type unreadCountData struct {
	Count int `json:"count"`
}

// unreadCountBody is the body shape for UnreadNotificationCountOutput.
type unreadCountBody struct {
	Data unreadCountData `json:"data"`
}

// UnreadNotificationCountOutput is the response envelope for GET /api/v1/notifications/unread_count.
type UnreadNotificationCountOutput struct {
	Body unreadCountBody
}

// markReadData is the inner data object for MarkNotificationReadOutput.
type markReadData struct {
	OK bool `json:"ok"`
}

// markReadBody is the body shape for MarkNotificationReadOutput.
type markReadBody struct {
	Data markReadData `json:"data"`
}

// MarkNotificationReadInput carries the path parameter for POST /api/v1/notifications/{id}/read.
type MarkNotificationReadInput struct {
	ID string `path:"id" doc:"Notification identifier."`
}

// MarkNotificationReadOutput is the response envelope for POST /api/v1/notifications/{id}/read.
type MarkNotificationReadOutput struct {
	Body markReadBody
}

// markAllReadData is the inner data object for MarkAllNotificationsReadOutput.
type markAllReadData struct {
	Marked int `json:"marked"`
}

// markAllReadBody is the body shape for MarkAllNotificationsReadOutput.
type markAllReadBody struct {
	Data markAllReadData `json:"data"`
}

// MarkAllNotificationsReadOutput is the response envelope for POST /api/v1/notifications/read_all.
type MarkAllNotificationsReadOutput struct {
	Body markAllReadBody
}

// ListAllNotificationsInput holds the pagination and filter query parameters for admin list.
type ListAllNotificationsInput struct {
	Page           int    `query:"page" default:"1" minimum:"1"`
	PerPage        int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
	Sort           string `query:"sort" default:"created_at"`
	Order          string `query:"order" default:"desc" enum:"asc,desc"`
	IncludeDeleted bool   `query:"include_deleted,omitempty"`
	AudienceType   string `query:"audience_type,omitempty"`
}

func (input *ListAllNotificationsInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

// ListAllNotificationsOutput is the response envelope for GET /api/v1/admin/notifications.
type ListAllNotificationsOutput struct {
	Body ListResponse[types.Notification]
}

// GetNotificationInput carries the path parameter for GET /api/v1/admin/notifications/{id}.
type GetNotificationInput struct {
	ID string `path:"id" doc:"Notification identifier."`
}

// GetNotificationOutput is the response envelope for GET /api/v1/admin/notifications/{id}.
type GetNotificationOutput struct {
	Body DataResponse[types.Notification]
}

func registerNotificationOperations(api huma.API, server *Server) {
	authenticated := authz.Authenticated()
	read := authz.System(authz.PermissionSystemNotificationsRead)

	contract.Register(api, contract.Operation{
		ID:            "listMyNotifications",
		Method:        http.MethodGet,
		Path:          "/api/v1/notifications",
		Summary:       "List my notifications",
		Description:   "Returns the paginated notification inbox for the current user.",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authenticated,
		Authorize:     server.authorizationMiddleware,
	}, server.listMyNotifications)

	contract.Register(api, contract.Operation{
		ID:            "unreadNotificationCount",
		Method:        http.MethodGet,
		Path:          "/api/v1/notifications/unread_count",
		Summary:       "Get unread notification count",
		Description:   "Returns the number of unread notifications for the current user.",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authenticated,
		Authorize:     server.authorizationMiddleware,
	}, server.unreadNotificationCount)

	contract.Register(api, contract.Operation{
		ID:            "markNotificationRead",
		Method:        http.MethodPost,
		Path:          "/api/v1/notifications/{id}/read",
		Summary:       "Mark notification as read",
		Description:   "Marks a single notification as read. Returns 200 even when the notification is unknown or deleted (silent 200 contract).",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authenticated,
		Authorize:     server.authorizationMiddleware,
	}, server.markNotificationRead)

	contract.Register(api, contract.Operation{
		ID:            "markAllNotificationsRead",
		Method:        http.MethodPost,
		Path:          "/api/v1/notifications/read_all",
		Summary:       "Mark all notifications as read",
		Description:   "Marks all visible notifications as read for the current user.",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authenticated,
		Authorize:     server.authorizationMiddleware,
	}, server.markAllNotificationsRead)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "listAllNotifications",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/notifications",
		Summary:       "List all notifications",
		Description:   "Returns paginated notifications with optional filtering by audience type and include_deleted flag.",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        read,
		Authorize:     server.authorizationMiddleware,
	}, server.listAllNotifications)

	contract.Register(api, contract.Operation{
		ID:            "getNotification",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/notifications/{id}",
		Summary:       "Get notification",
		Description:   "Returns a single notification by ID.",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		Access:        read,
		Authorize:     server.authorizationMiddleware,
	}, server.getNotification)
}

func (s *Server) listMyNotifications(ctx context.Context, input *ListMyNotificationsInput) (*ListMyNotificationsOutput, error) {
	authorization, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}
	pagination := input.pagination()
	items, total, err := s.Notifications.ListVisibleForUser(authorization.Principal.UserID, pagination)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list notifications", nil)
	}
	return &ListMyNotificationsOutput{Body: ListResponse[types.Notification]{
		Data: items,
		Meta: paginationMeta(total, pagination),
	}}, nil
}

func (s *Server) unreadNotificationCount(ctx context.Context, _ *struct{}) (*UnreadNotificationCountOutput, error) {
	authorization, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}
	count, err := s.Notifications.CountUnreadForUser(authorization.Principal.UserID)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to count unread notifications", nil)
	}
	return &UnreadNotificationCountOutput{Body: unreadCountBody{Data: unreadCountData{Count: count}}}, nil
}

func (s *Server) markNotificationRead(ctx context.Context, input *MarkNotificationReadInput) (*MarkNotificationReadOutput, error) {
	authorization, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}
	if err := s.Notifications.MarkNotificationRead(authorization.Principal.UserID, input.ID); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to mark notification read", nil)
	}
	return &MarkNotificationReadOutput{Body: markReadBody{Data: markReadData{OK: true}}}, nil
}

func (s *Server) markAllNotificationsRead(ctx context.Context, _ *struct{}) (*MarkAllNotificationsReadOutput, error) {
	authorization, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}
	marked, err := s.Notifications.MarkAllNotificationsRead(authorization.Principal.UserID)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to mark all notifications read", nil)
	}
	return &MarkAllNotificationsReadOutput{Body: markAllReadBody{Data: markAllReadData{Marked: marked}}}, nil
}

func (s *Server) listAllNotifications(ctx context.Context, input *ListAllNotificationsInput) (*ListAllNotificationsOutput, error) {
	_, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}

	audienceType := strings.ToLower(input.AudienceType)
	if audienceType != "" &&
		audienceType != types.AudienceTypeGlobal &&
		audienceType != types.AudienceTypeProject &&
		audienceType != types.AudienceTypeUser {
		return nil, contract.NewError(http.StatusBadRequest, "invalid_input", "audience_type filter must be one of: global, project, user", nil)
	}

	pagination := input.pagination()
	items, total, err := s.Notifications.ListAllNotifications(input.IncludeDeleted, audienceType, pagination)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list notifications", nil)
	}

	return &ListAllNotificationsOutput{Body: ListResponse[types.Notification]{
		Data: items,
		Meta: paginationMeta(total, pagination),
	}}, nil
}

func (s *Server) getNotification(ctx context.Context, input *GetNotificationInput) (*GetNotificationOutput, error) {
	_, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}

	n, err := s.Notifications.GetNotification(input.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, contract.NewError(http.StatusNotFound, "not_found", "notification not found", nil)
		}
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to fetch notification", nil)
	}

	return &GetNotificationOutput{Body: DataResponse[types.Notification]{Data: *n}}, nil
}
