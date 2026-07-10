package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// notificationCreatePayload is the request body for POST /admin/notifications
// and PUT /admin/notifications/{id}.
type notificationCreatePayload struct {
	Title        string  `json:"title"`
	Body         string  `json:"body"`
	AudienceType string  `json:"audience_type"`
	AudienceID   *string `json:"audience_id"`
}

// validateNotificationPayload returns (errCode, errMessage) on failure or
// ("","") on success. It ONLY checks shape and field bounds; audience
// existence checks happen in the handler (which needs the Store).
func validateNotificationPayload(p notificationCreatePayload) (string, string) {
	if len(p.Title) < 1 || len(p.Title) > 200 {
		return "invalid_input", "title must be 1..200 characters"
	}
	if len(p.Body) < 1 || len(p.Body) > 20000 {
		return "invalid_input", "body must be 1..20000 characters"
	}
	switch p.AudienceType {
	case types.AudienceTypeGlobal:
		if p.AudienceID != nil && *p.AudienceID != "" {
			return "invalid_input", "audience_id must be omitted for global audience"
		}
	case types.AudienceTypeProject, types.AudienceTypeUser:
		if p.AudienceID == nil || *p.AudienceID == "" {
			return "invalid_input", "audience_id is required for project/user audience"
		}
	default:
		return "invalid_input", "audience_type must be one of: global, project, user"
	}
	return "", ""
}

// resolveAudience verifies the audience row exists and is usable.
// Returns ("","") on success or (code, message) on failure suitable for
// writeError. Never called for global audience.
// Code "internal" indicates a transient DB error (500); other codes are 400.
func resolveAudience(st *store.Store, audienceType, audienceID string) (string, string) {
	switch audienceType {
	case types.AudienceTypeProject:
		p, err := st.GetProjectByID(audienceID)
		if err != nil {
			return "internal", "failed to fetch project"
		}
		if p == nil {
			return "invalid_audience", "project not found"
		}
		if p.Status == types.ProjectStatusArchived {
			return "invalid_audience", "project is archived"
		}
	case types.AudienceTypeUser:
		u, err := st.GetUserByID(audienceID)
		if err != nil {
			return "internal", "failed to fetch user"
		}
		if u == nil {
			return "invalid_audience", "user not found"
		}
		if u.Status == types.UserStatusDisabled {
			return "invalid_audience", "user is disabled"
		}
	}
	return "", ""
}

func handleListAllNotifications(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parsePagination(r)
		includeDeleted := r.URL.Query().Get("include_deleted") == "1"
		audienceType := strings.ToLower(r.URL.Query().Get("audience_type"))
		if audienceType != "" &&
			audienceType != types.AudienceTypeGlobal &&
			audienceType != types.AudienceTypeProject &&
			audienceType != types.AudienceTypeUser {
			writeError(w, http.StatusBadRequest, "invalid_input", "audience_type filter must be one of: global, project, user")
			return
		}
		items, total, err := st.ListAllNotifications(includeDeleted, audienceType, p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to list notifications")
			return
		}
		writeList(w, items, total, p.Page, p.Limit())
	}
}

func handleGetNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		n, err := st.GetNotification(id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "notification not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "failed to fetch notification")
			return
		}
		writeData(w, http.StatusOK, n)
	}
}

func handleCreateNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me := UserFromContext(r.Context())
		var body notificationCreatePayload
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "invalid request body")
			return
		}
		if code, msg := validateNotificationPayload(body); code != "" {
			writeError(w, http.StatusBadRequest, code, msg)
			return
		}
		if body.AudienceType != types.AudienceTypeGlobal {
			if code, msg := resolveAudience(st, body.AudienceType, *body.AudienceID); code != "" {
				statusCode := http.StatusBadRequest
				if code == "internal" {
					statusCode = http.StatusInternalServerError
				}
				writeError(w, statusCode, code, msg)
				return
			}
		}
		n := &types.Notification{
			Title:        body.Title,
			Body:         body.Body,
			AudienceType: body.AudienceType,
			AudienceID:   body.AudienceID,
			CreatedBy:    me.ID,
		}
		if err := st.CreateNotification(n); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to create notification")
			return
		}
		writeData(w, http.StatusOK, n)
	}
}

func handleUpdateNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var body notificationCreatePayload
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "invalid request body")
			return
		}
		if code, msg := validateNotificationPayload(body); code != "" {
			writeError(w, http.StatusBadRequest, code, msg)
			return
		}
		if body.AudienceType != types.AudienceTypeGlobal {
			if code, msg := resolveAudience(st, body.AudienceType, *body.AudienceID); code != "" {
				statusCode := http.StatusBadRequest
				if code == "internal" {
					statusCode = http.StatusInternalServerError
				}
				writeError(w, statusCode, code, msg)
				return
			}
		}
		if err := st.UpdateNotification(id, body.Title, body.Body, body.AudienceType, body.AudienceID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "notification not found or already deleted")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "failed to update notification")
			return
		}
		n, err := st.GetNotification(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to reload notification")
			return
		}
		writeData(w, http.StatusOK, n)
	}
}

func handleDeleteNotification(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := st.SoftDeleteNotification(id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "notification not found or already deleted")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "failed to delete notification")
			return
		}
		writeData(w, http.StatusOK, map[string]any{"deleted": true})
	}
}
