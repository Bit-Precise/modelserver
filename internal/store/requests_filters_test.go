package store

import (
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/types"
)

func TestNormalizeRequestRetryStatus(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{status: types.RequestStatusProcessing, want: types.RequestRetryStatusNormal},
		{status: types.RequestStatusSuccess, want: types.RequestRetryStatusNormal},
		{status: types.RequestStatusError, want: types.RequestRetryStatusNonRetryableError},
		{status: types.RequestStatusRateLimited, want: types.RequestRetryStatusNonRetryableError},
	}

	for _, tt := range tests {
		req := types.Request{Status: tt.status}
		normalizeRequestRetryStatus(&req)
		if req.RetryStatus != tt.want {
			t.Errorf("status %q: RetryStatus = %q, want %q", tt.status, req.RetryStatus, tt.want)
		}
	}
}

// TestBuildRequestFilters_RequestKindEmitsPredicate proves a non-empty
// RequestKind injects the right SQL fragment and arg. Without this,
// the dashboard kind dropdown can pass a value but the store would
// silently drop it.
func TestBuildRequestFilters_RequestKindEmitsPredicate(t *testing.T) {
	where, args, _ := buildRequestFilters("proj-1", RequestFilters{
		RequestKind: "openai_responses",
	})

	if !strings.Contains(where, "r.request_kind = $2") {
		t.Errorf("WHERE missing request_kind predicate: %q", where)
	}
	if len(args) != 2 || args[1] != "openai_responses" {
		t.Errorf("args = %v, want [proj-1, openai_responses]", args)
	}
}

// TestBuildRequestFilters_RequestKindEmptyIsNoop documents that an
// empty RequestKind produces no predicate — so the default "All kinds"
// state of the dropdown returns every row, not zero rows.
func TestBuildRequestFilters_RequestKindEmptyIsNoop(t *testing.T) {
	where, args, _ := buildRequestFilters("proj-1", RequestFilters{
		RequestKind: "",
	})

	if strings.Contains(where, "request_kind") {
		t.Errorf("WHERE should not mention request_kind when empty: %q", where)
	}
	if len(args) != 1 {
		t.Errorf("args len = %d, want 1 (just project id)", len(args))
	}
}

// TestBuildRequestFilters_ComposesWithOtherFilters guards against
// arg-numbering regressions when multiple optional predicates are set
// together — the order is Model → RequestKind → Status → RetryStatus →
// APIKeyID → CreatedBy → Since → Until.
func TestBuildRequestFilters_ComposesWithOtherFilters(t *testing.T) {
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	where, args, next := buildRequestFilters("proj-1", RequestFilters{
		Model:       "claude-opus-4-8",
		RequestKind: "anthropic_messages",
		Status:      "success",
		Since:       since,
	})

	// projectID is $1; the four optional filters take $2..$5 in declared order.
	for i, want := range []string{
		"r.project_id = $1",
		"r.model = $2",
		"r.request_kind = $3",
		"r.status = $4",
		"r.created_at >= $5",
	} {
		if !strings.Contains(where, want) {
			t.Errorf("[%d] WHERE missing %q in %q", i, want, where)
		}
	}
	if next != 6 {
		t.Errorf("next arg index = %d, want 6", next)
	}
	if len(args) != 5 {
		t.Errorf("args len = %d, want 5", len(args))
	}
}

func TestBuildRequestFilters_RetryStatus(t *testing.T) {
	where, args, next := buildRequestFilters("proj-1", RequestFilters{
		Status:      "success",
		RetryStatus: "retryable_error",
	})

	if !strings.Contains(where, "r.status = $2") ||
		!strings.Contains(where, "r.retry_status = $3") {
		t.Fatalf("WHERE missing retry status predicates: %q", where)
	}
	if next != 4 || len(args) != 3 || args[2] != "retryable_error" {
		t.Fatalf("args = %v, next = %d", args, next)
	}
}

// TestBuildGlobalRequestFilters_RequestKind ensures the admin/global
// builder honors RequestKind too — the global builder skips the
// project_id predicate, so this is a separate code path.
func TestBuildGlobalRequestFilters_RequestKind(t *testing.T) {
	where, args, _ := buildGlobalRequestFilters(RequestFilters{
		RequestKind: "openai_chat_completions",
	})

	if !strings.Contains(where, "r.request_kind = $1") {
		t.Errorf("global WHERE missing request_kind predicate: %q", where)
	}
	if len(args) != 1 || args[0] != "openai_chat_completions" {
		t.Errorf("args = %v, want [openai_chat_completions]", args)
	}
}

func TestBuildGlobalRequestFilters_RetryStatus(t *testing.T) {
	where, args, next := buildGlobalRequestFilters(RequestFilters{
		RetryStatus: "retry_exhausted",
	})

	if !strings.Contains(where, "r.retry_status = $1") {
		t.Fatalf("global WHERE missing retry_status predicate: %q", where)
	}
	if next != 2 || len(args) != 1 || args[0] != "retry_exhausted" {
		t.Fatalf("args = %v, next = %d", args, next)
	}
}
