package adminv1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/types"
)

func TestUnreadCountEnvelope(t *testing.T) {
	t.Parallel()
	output := &UnreadNotificationCountOutput{
		Body: unreadCountBody{
			Data: unreadCountData{Count: 42},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal UnreadNotificationCountOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify the count field
	if !strings.Contains(got, `"count":42`) {
		t.Errorf("JSON missing 'count' field: %s", got)
	}
}

func TestMarkReadEnvelope(t *testing.T) {
	t.Parallel()
	output := &MarkNotificationReadOutput{
		Body: markReadBody{
			Data: markReadData{OK: true},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal MarkNotificationReadOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify the ok field
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("JSON missing 'ok' field: %s", got)
	}
}

func TestMarkAllReadEnvelope(t *testing.T) {
	t.Parallel()
	output := &MarkAllNotificationsReadOutput{
		Body: markAllReadBody{
			Data: markAllReadData{Marked: 7},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal MarkAllNotificationsReadOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify the marked field
	if !strings.Contains(got, `"marked":7`) {
		t.Errorf("JSON missing 'marked' field: %s", got)
	}
}

func TestDeleteNotificationEnvelope(t *testing.T) {
	t.Parallel()
	output := &DeleteNotificationOutput{
		Body: DataResponse[DeleteNotificationResponseData]{
			Data: DeleteNotificationResponseData{Deleted: true},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal DeleteNotificationOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify the deleted field
	if !strings.Contains(got, `"deleted":true`) {
		t.Errorf("JSON missing 'deleted' field: %s", got)
	}
}

func TestCreateUpdateNotificationEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
	notification := types.Notification{
		ID:           "notif-123",
		Title:        "Test Notification",
		Body:         "This is a test notification",
		AudienceType: types.AudienceTypeGlobal,
		AudienceID:   nil,
		CreatedBy:    "user-456",
		CreatedAt:    now,
		UpdatedAt:    now,
		DeletedAt:    nil,
	}

	output := &CreateNotificationOutput{
		Body: DataResponse[types.Notification]{
			Data: notification,
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal CreateNotificationOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify expected notification fields
	expectedFields := []string{
		`"id":"notif-123"`,
		`"title":"Test Notification"`,
		`"body":"This is a test notification"`,
		`"audience_type":"global"`,
		`"created_by":"user-456"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(got, field) {
			t.Errorf("JSON missing field %s; got: %s", field, got)
		}
	}
}
