package adminv1

import (
	"context"
	"errors"
	"log"
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

// CreateNotificationInput is the typed request for POST /api/v1/admin/notifications.
// No Huma-level validators are set to preserve the legacy 400 invalid_input wire shape.
type CreateNotificationInput struct {
	Body struct {
		Title        string  `json:"title"`
		Body         string  `json:"body"`
		AudienceType string  `json:"audience_type"`
		AudienceID   *string `json:"audience_id,omitempty"`
	}
}

// CreateNotificationOutput is the response envelope for POST /api/v1/admin/notifications.
type CreateNotificationOutput struct {
	Body DataResponse[types.Notification]
}

// UpdateNotificationInput is the typed request for PUT /api/v1/admin/notifications/{id}.
type UpdateNotificationInput struct {
	ID   string `path:"id" doc:"Notification identifier."`
	Body struct {
		Title        string  `json:"title"`
		Body         string  `json:"body"`
		AudienceType string  `json:"audience_type"`
		AudienceID   *string `json:"audience_id,omitempty"`
	}
}

// UpdateNotificationOutput is the response envelope for PUT /api/v1/admin/notifications/{id}.
type UpdateNotificationOutput struct {
	Body DataResponse[types.Notification]
}

// DeleteNotificationInput carries the path parameter for DELETE /api/v1/admin/notifications/{id}.
type DeleteNotificationInput struct {
	ID string `path:"id" doc:"Notification identifier."`
}

// DeleteNotificationResponseData is the inner data for the delete response.
// The legacy wire shape returns {"data": {"deleted": true}} at status 200 (not 204).
type DeleteNotificationResponseData struct {
	Deleted bool `json:"deleted"`
}

// DeleteNotificationOutput preserves the legacy wire quirk of returning
// {"data": {"deleted": true}} at status 200 (not 204).
type DeleteNotificationOutput struct {
	Body DataResponse[DeleteNotificationResponseData]
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

	manage := authz.System(authz.PermissionSystemNotificationsManage)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "createNotification",
		Method:        http.MethodPost,
		Path:          "/api/v1/admin/notifications",
		Summary:       "Create notification",
		Description:   "Creates a new notification. Returns 200 (legacy wire quirk, not 201).",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        manage,
		Authorize:     server.authorizationMiddleware,
	}, server.createNotification)

	contract.Register(api, contract.Operation{
		ID:            "updateNotification",
		Method:        http.MethodPut,
		Path:          "/api/v1/admin/notifications/{id}",
		Summary:       "Update notification",
		Description:   "Updates an existing notification by ID.",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		Access:        manage,
		Authorize:     server.authorizationMiddleware,
	}, server.updateNotification)

	contract.Register(api, contract.Operation{
		ID:            "deleteNotification",
		Method:        http.MethodDelete,
		Path:          "/api/v1/admin/notifications/{id}",
		Summary:       "Delete notification",
		Description:   "Soft-deletes a notification. Returns 200 with {data:{deleted:true}} (legacy wire quirk, not 204).",
		Tags:          []string{"Notifications"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		Access:        manage,
		Authorize:     server.authorizationMiddleware,
	}, server.deleteNotification)
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

func (s *Server) createNotification(ctx context.Context, input *CreateNotificationInput) (*CreateNotificationOutput, error) {
	me, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}

	payload := notificationCreatePayload{
		Title:        input.Body.Title,
		Body:         input.Body.Body,
		AudienceType: input.Body.AudienceType,
		AudienceID:   input.Body.AudienceID,
	}
	if code, msg := validateNotificationPayload(payload); code != "" {
		return nil, contract.NewError(http.StatusBadRequest, code, msg, nil)
	}
	if payload.AudienceType != types.AudienceTypeGlobal {
		if code, msg := resolveAudience(s.Notifications, payload.AudienceType, *payload.AudienceID); code != "" {
			status := http.StatusBadRequest
			if code == "internal" {
				status = http.StatusInternalServerError
			}
			return nil, contract.NewError(status, code, msg, nil)
		}
	}

	n := &types.Notification{
		Title:        payload.Title,
		Body:         payload.Body,
		AudienceType: payload.AudienceType,
		AudienceID:   payload.AudienceID,
		CreatedBy:    me.Principal.UserID,
	}
	if err := s.Notifications.CreateNotification(n); err != nil {
		log.Printf("ERROR notifications: create by=%s audience_type=%s: %v", me.Principal.UserID, payload.AudienceType, err)
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to create notification", nil)
	}

	return &CreateNotificationOutput{Body: DataResponse[types.Notification]{Data: *n}}, nil
}

func (s *Server) updateNotification(ctx context.Context, input *UpdateNotificationInput) (*UpdateNotificationOutput, error) {
	_, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}

	payload := notificationCreatePayload{
		Title:        input.Body.Title,
		Body:         input.Body.Body,
		AudienceType: input.Body.AudienceType,
		AudienceID:   input.Body.AudienceID,
	}
	if code, msg := validateNotificationPayload(payload); code != "" {
		return nil, contract.NewError(http.StatusBadRequest, code, msg, nil)
	}
	if payload.AudienceType != types.AudienceTypeGlobal {
		if code, msg := resolveAudience(s.Notifications, payload.AudienceType, *payload.AudienceID); code != "" {
			status := http.StatusBadRequest
			if code == "internal" {
				status = http.StatusInternalServerError
			}
			return nil, contract.NewError(status, code, msg, nil)
		}
	}

	if err := s.Notifications.UpdateNotification(input.ID, payload.Title, payload.Body, payload.AudienceType, payload.AudienceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, contract.NewError(http.StatusNotFound, "not_found", "notification not found or already deleted", nil)
		}
		log.Printf("ERROR notifications: update id=%s: %v", input.ID, err)
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to update notification", nil)
	}

	n, err := s.Notifications.GetNotification(input.ID)
	if err != nil {
		log.Printf("ERROR notifications: reload_after_update id=%s: %v", input.ID, err)
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to reload notification", nil)
	}

	return &UpdateNotificationOutput{Body: DataResponse[types.Notification]{Data: *n}}, nil
}

func (s *Server) deleteNotification(ctx context.Context, input *DeleteNotificationInput) (*DeleteNotificationOutput, error) {
	_, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}

	if err := s.Notifications.SoftDeleteNotification(input.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, contract.NewError(http.StatusNotFound, "not_found", "notification not found or already deleted", nil)
		}
		log.Printf("ERROR notifications: soft_delete id=%s: %v", input.ID, err)
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to delete notification", nil)
	}

	return &DeleteNotificationOutput{Body: DataResponse[DeleteNotificationResponseData]{Data: DeleteNotificationResponseData{Deleted: true}}}, nil
}
