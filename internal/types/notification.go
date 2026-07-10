package types

import "time"

// Audience type constants.
const (
	AudienceTypeGlobal  = "global"
	AudienceTypeProject = "project"
	AudienceTypeUser    = "user"
)

// Notification is a platform-authored in-dashboard message.
//
// AudienceID is nil for global audience and points to a project or user
// row otherwise. AudienceName is populated only by ListVisibleForUser
// (display_name of the project, or "You" for user audience, or "" for
// global) — admin listings leave it nil.
//
// ReadAt is populated only by ListVisibleForUser (nil = unread for the
// requesting user). Admin listings leave it nil.
//
// ReadCount is populated only by admin listings (GetNotification and
// ListAllNotifications). User listings leave it 0.
//
// DeletedAt is nil for alive rows; set to the soft-delete timestamp
// otherwise.
type Notification struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	AudienceType string     `json:"audience_type"`
	AudienceID   *string    `json:"audience_id,omitempty"`
	AudienceName *string    `json:"audience_name,omitempty"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
	ReadCount    int        `json:"read_count,omitempty"`
}
