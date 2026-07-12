package adminv1

import (
	"context"
	"errors"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// recordingAuthStore extends fakeAuthStore to capture UpdateUser calls
type recordingAuthStore struct {
	*fakeAuthStore
	recordedUpdates map[string]any
	updateError     error
}

func (s *recordingAuthStore) UpdateUser(id string, updates map[string]any) error {
	s.recordedUpdates = updates
	return s.updateError
}

func TestUpdateUserEmptyBody(t *testing.T) {
	t.Parallel()
	store := &recordingAuthStore{
		fakeAuthStore:   &fakeAuthStore{},
		recordedUpdates: nil,
		updateError:     nil,
	}
	server := &Server{Auth: store}

	input := &UpdateUserInput{
		UserID: "user-1",
	}
	input.Body.Nickname = nil
	input.Body.Status = nil
	input.Body.IsSuperadmin = nil
	input.Body.MaxProjects = nil

	_, err := server.updateUser(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")
}

func TestUpdateUserUpdateUserError(t *testing.T) {
	t.Parallel()
	store := &recordingAuthStore{
		fakeAuthStore:   &fakeAuthStore{},
		recordedUpdates: nil,
		updateError:     errors.New("store error"),
	}
	server := &Server{Auth: store}

	input := &UpdateUserInput{
		UserID: "user-1",
	}
	nickname := "new-nickname"
	input.Body.Nickname = &nickname

	_, err := server.updateUser(context.Background(), input)
	assertStatusError(t, err, 500, "internal")
}

func TestUpdateUserGetUserByIDReturnsNil(t *testing.T) {
	t.Parallel()
	store := &recordingAuthStore{
		fakeAuthStore: &fakeAuthStore{
			usersByID: map[string]*types.User{},
		},
		recordedUpdates: nil,
		updateError:     nil,
	}
	server := &Server{Auth: store}

	input := &UpdateUserInput{
		UserID: "user-1",
	}
	nickname := "new-nickname"
	input.Body.Nickname = &nickname

	_, err := server.updateUser(context.Background(), input)
	assertStatusError(t, err, 500, "internal")
}

func TestUpdateUserHappyPathAllFields(t *testing.T) {
	t.Parallel()
	existingUser := &types.User{
		ID:           "user-1",
		Email:        "test@example.com",
		Nickname:     "updated-nickname",
		Status:       "active",
		IsSuperadmin: false,
		MaxProjects:  10,
	}
	store := &recordingAuthStore{
		fakeAuthStore: &fakeAuthStore{
			usersByID: map[string]*types.User{
				"user-1": existingUser,
			},
		},
		recordedUpdates: nil,
		updateError:     nil,
	}
	server := &Server{Auth: store}

	input := &UpdateUserInput{
		UserID: "user-1",
	}
	nickname := "updated-nickname"
	status := "active"
	isSuperadmin := false
	maxProjects := 10
	input.Body.Nickname = &nickname
	input.Body.Status = &status
	input.Body.IsSuperadmin = &isSuperadmin
	input.Body.MaxProjects = &maxProjects

	out, err := server.updateUser(context.Background(), input)
	if err != nil {
		t.Fatalf("updateUser() error = %v", err)
	}

	if out == nil || out.Body.Data.ID != "user-1" {
		t.Fatalf("expected user with ID user-1 in response, got %+v", out)
	}

	// Assert all 4 keys are in the updates map
	if len(store.recordedUpdates) != 4 {
		t.Errorf("expected 4 fields in updates, got %d: %v", len(store.recordedUpdates), store.recordedUpdates)
	}
	if _, ok := store.recordedUpdates["nickname"]; !ok {
		t.Error("nickname not in updates map")
	}
	if _, ok := store.recordedUpdates["status"]; !ok {
		t.Error("status not in updates map")
	}
	if _, ok := store.recordedUpdates["is_superadmin"]; !ok {
		t.Error("is_superadmin not in updates map")
	}
	if _, ok := store.recordedUpdates["max_projects"]; !ok {
		t.Error("max_projects not in updates map")
	}
}

func TestUpdateUserHappyPathNicknameOnly(t *testing.T) {
	t.Parallel()
	existingUser := &types.User{
		ID:       "user-1",
		Email:    "test@example.com",
		Nickname: "updated-nickname",
	}
	store := &recordingAuthStore{
		fakeAuthStore: &fakeAuthStore{
			usersByID: map[string]*types.User{
				"user-1": existingUser,
			},
		},
		recordedUpdates: nil,
		updateError:     nil,
	}
	server := &Server{Auth: store}

	input := &UpdateUserInput{
		UserID: "user-1",
	}
	nickname := "updated-nickname"
	input.Body.Nickname = &nickname
	input.Body.Status = nil
	input.Body.IsSuperadmin = nil
	input.Body.MaxProjects = nil

	out, err := server.updateUser(context.Background(), input)
	if err != nil {
		t.Fatalf("updateUser() error = %v", err)
	}

	if out == nil || out.Body.Data.ID != "user-1" {
		t.Fatalf("expected user with ID user-1 in response, got %+v", out)
	}

	// Assert only nickname is in the updates map
	if len(store.recordedUpdates) != 1 {
		t.Errorf("expected 1 field in updates, got %d: %v", len(store.recordedUpdates), store.recordedUpdates)
	}
	if _, ok := store.recordedUpdates["nickname"]; !ok {
		t.Error("nickname not in updates map")
	}
}
