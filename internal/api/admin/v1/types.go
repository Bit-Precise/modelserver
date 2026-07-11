package adminv1

import (
	"encoding/json"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/types"
)

// DataResponse is the existing management API's single-resource envelope.
type DataResponse[T any] struct {
	Data T `json:"data"`
}

// ListResponse is the existing management API's paginated collection envelope.
type ListResponse[T any] struct {
	Data []T  `json:"data" nullable:"false"`
	Meta Meta `json:"meta"`
}

type Meta struct {
	Total      int `json:"total"`
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalPages int `json:"total_pages"`
}

func paginationMeta(total int, pagination types.PaginationParams) Meta {
	perPage := pagination.Limit()
	totalPages := 0
	if total > 0 {
		totalPages = (total + perPage - 1) / perPage
	}
	return Meta{
		Total:      total,
		Page:       pagination.Page,
		PerPage:    perPage,
		TotalPages: totalPages,
	}
}

// Permission is the API representation of a stable permission identifier.
// Its custom schema keeps generated TypeScript as a literal union rather than
// degrading capabilities to arbitrary strings.
type Permission authz.Permission

func (Permission) Schema(huma.Registry) *huma.Schema {
	permissions := authz.AllPermissions()
	enum := make([]any, len(permissions))
	for i, permission := range permissions {
		enum[i] = string(permission)
	}
	return &huma.Schema{
		Type:        "string",
		Title:       "Permission",
		Description: "Stable ModelServer management API permission identifier.",
		Enum:        enum,
	}
}

type ProjectRole authz.ProjectRole

func (ProjectRole) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:        "string",
		Title:       "ProjectRole",
		Description: "Built-in project membership role.",
		Enum:        []any{string(authz.RoleOwner), string(authz.RoleMaintainer), string(authz.RoleDeveloper)},
	}
}

// ProjectSettings preserves the existing json.RawMessage wire behavior while
// still documenting the intended settings shape as a JSON object. The store
// currently exposes settings as raw JSONB bytes; decoding through map[string]any
// here would reject arrays/scalars and round integers larger than 2^53.
type ProjectSettings json.RawMessage

func (ProjectSettings) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:                 "object",
		AdditionalProperties: &huma.Schema{},
	}
}

func (settings ProjectSettings) MarshalJSON() ([]byte, error) {
	return json.RawMessage(settings).MarshalJSON()
}

type User struct {
	ID           string    `json:"id" format:"uuid"`
	Email        string    `json:"email" format:"email"`
	Nickname     string    `json:"nickname"`
	Picture      string    `json:"picture,omitempty" format:"uri"`
	IsSuperadmin bool      `json:"is_superadmin"`
	MaxProjects  int       `json:"max_projects" minimum:"0"`
	Status       string    `json:"status" enum:"active,disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func userDTO(user *types.User) User {
	return User{
		ID:           user.ID,
		Email:        user.Email,
		Nickname:     user.Nickname,
		Picture:      user.Picture,
		IsSuperadmin: user.IsSuperadmin,
		MaxProjects:  user.MaxProjects,
		Status:       user.Status,
		CreatedAt:    user.CreatedAt,
		UpdatedAt:    user.UpdatedAt,
	}
}

type Project struct {
	ID          string          `json:"id" format:"uuid"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	CreatedBy   string          `json:"created_by" format:"uuid"`
	Status      string          `json:"status" enum:"active,suspended,archived"`
	Settings    ProjectSettings `json:"settings,omitempty"`
	BillingTags []string        `json:"billing_tags,omitempty" nullable:"false"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func projectDTO(project *types.Project) Project {
	return Project{
		ID:          project.ID,
		Name:        project.Name,
		Description: project.Description,
		CreatedBy:   project.CreatedBy,
		Status:      project.Status,
		Settings:    ProjectSettings(append(json.RawMessage(nil), project.Settings...)),
		BillingTags: append([]string(nil), project.BillingTags...),
		CreatedAt:   project.CreatedAt,
		UpdatedAt:   project.UpdatedAt,
	}
}

func permissionsDTO(capabilities authz.CapabilitySet) []Permission {
	permissions := capabilities.Permissions()
	result := make([]Permission, len(permissions))
	for i, permission := range permissions {
		result[i] = Permission(permission)
	}
	return result
}

type GlobalCapabilities struct {
	IsSuperadmin bool         `json:"is_superadmin"`
	Permissions  []Permission `json:"permissions" nullable:"false"`
}

type ProjectCapabilities struct {
	Role        ProjectRole  `json:"role,omitempty"`
	Permissions []Permission `json:"permissions" nullable:"false"`
}

type AuthConfig struct {
	OAuthProviders   []string          `json:"oauth_providers" nullable:"false"`
	LoginDescription string            `json:"login_description,omitempty"`
	OAuthLabels      map[string]string `json:"oauth_labels,omitempty"`
	LoginFooterHTML  string            `json:"login_footer_html,omitempty"`
	GitHubURL        string            `json:"github_url,omitempty" format:"uri"`
}
