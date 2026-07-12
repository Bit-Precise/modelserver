package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/types"
)

func TestAddMemberRejectsInvalidRole(t *testing.T) {
	router := chi.NewRouter()
	router.Post("/projects/{projectID}/members", handleAddMember(nil))

	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/members",
		bytes.NewBufferString(`{"email":"member@example.com","role":"administrator"}`))
	req = req.WithContext(context.WithValue(req.Context(), ctxMember, &types.ProjectMember{
		ProjectID: "project-1",
		UserID:    "owner-1",
		Role:      types.RoleOwner,
	}))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_role"`) {
		t.Fatalf("body = %s, want invalid_role error", rec.Body.String())
	}
}

func TestUpdateMemberRejectsInvalidRole(t *testing.T) {
	router := chi.NewRouter()
	router.Put("/projects/{projectID}/members/{userID}", handleUpdateMember(nil))

	req := httptest.NewRequest(http.MethodPut, "/projects/project-1/members/member-1",
		bytes.NewBufferString(`{"role":"OWNER"}`))
	req = req.WithContext(context.WithValue(req.Context(), ctxMember, &types.ProjectMember{
		ProjectID: "project-1",
		UserID:    "owner-1",
		Role:      types.RoleOwner,
	}))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_role"`) {
		t.Fatalf("body = %s, want invalid_role error", rec.Body.String())
	}
}
