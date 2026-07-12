package adminv1

import (
	"log"

	"github.com/modelserver/modelserver/internal/types"
)

// notificationCreatePayload is the shared request body shape for create and update.
type notificationCreatePayload struct {
	Title        string  `json:"title"`
	Body         string  `json:"body"`
	AudienceType string  `json:"audience_type"`
	AudienceID   *string `json:"audience_id"`
}

// validateNotificationPayload returns (errCode, errMessage) on failure or
// ("","") on success. It only checks field bounds and audience-type shape;
// audience row existence is checked separately via resolveAudience.
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
// Returns ("","") on success or (code, message) on failure.
// Code "internal" indicates a transient DB error (500); other codes are 400.
// Never called for global audience.
func resolveAudience(store notificationsStore, audienceType, audienceID string) (string, string) {
	switch audienceType {
	case types.AudienceTypeProject:
		p, err := store.GetProjectByID(audienceID)
		if err != nil {
			log.Printf("ERROR notifications: resolve_audience project=%s: %v", audienceID, err)
			return "internal", "failed to fetch project"
		}
		if p == nil {
			return "invalid_audience", "project not found"
		}
		if p.Status == types.ProjectStatusArchived {
			return "invalid_audience", "project is archived"
		}
	case types.AudienceTypeUser:
		u, err := store.GetUserByID(audienceID)
		if err != nil {
			log.Printf("ERROR notifications: resolve_audience user=%s: %v", audienceID, err)
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
