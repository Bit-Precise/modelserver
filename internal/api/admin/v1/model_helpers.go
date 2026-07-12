package adminv1

// model_helpers.go duplicates the validation helpers from
// internal/admin/handle_models.go. Batch 14 cleanup will remove the
// legacy copies once all routes are migrated.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/modelserver/modelserver/internal/types"
)

// modelLegalChars is the superset of characters allowed in canonical names
// and aliases: lowercase ASCII, digits, dot, underscore, dash.
const modelLegalChars = "abcdefghijklmnopqrstuvwxyz0123456789._-"

// validateModelPayload runs the create-time invariant checks that are
// cheap to express in Go. The trigger enforces the same rules at the DB
// level as a second line of defence.
func validateModelPayload(name string, aliases []string, status string, rate *types.CreditRate, imageRate *types.ImageCreditRate) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if err := validateModelName(name); err != nil {
		return err
	}
	if err := validateAliases(name, aliases); err != nil {
		return err
	}
	if status != "" && status != types.ModelStatusActive && status != types.ModelStatusDisabled {
		return fmt.Errorf("status must be active or disabled")
	}
	if err := validateCreditRate(rate); err != nil {
		return err
	}
	return validateImageCreditRate(imageRate)
}

func validateModelName(s string) error {
	if s != strings.ToLower(s) {
		return fmt.Errorf("name must be lowercase: %q", s)
	}
	for _, r := range s {
		if !strings.ContainsRune(modelLegalChars, r) {
			return fmt.Errorf("illegal character %q in name %q; allowed: %s", string(r), s, modelLegalChars)
		}
	}
	return nil
}

func validateAliases(canonical string, aliases []string) error {
	seen := make(map[string]struct{}, len(aliases))
	for _, a := range aliases {
		if err := validateModelName(a); err != nil {
			return fmt.Errorf("alias: %w", err)
		}
		if a == canonical {
			return fmt.Errorf("alias %q cannot equal canonical name", a)
		}
		if _, dup := seen[a]; dup {
			return fmt.Errorf("duplicate alias %q", a)
		}
		seen[a] = struct{}{}
	}
	return nil
}

// validatePublisher rejects empty strings. The controlled vocabulary is
// intentionally not enforced here so new publishers can be rolled out
// without a code change — admins just enter the string. Subscription
// eligibility switches on known values; anything unrecognised is treated as
// "not anthropic" = eligible.
func validatePublisher(p string) error {
	if p == "" {
		return fmt.Errorf("publisher is required (e.g. anthropic, openai, google)")
	}
	return nil
}

func validateCreditRate(r *types.CreditRate) error {
	if r == nil {
		return nil
	}
	if r.InputRate < 0 || r.OutputRate < 0 || r.CacheCreationRate < 0 || r.CacheReadRate < 0 {
		return fmt.Errorf("credit rates must be non-negative")
	}
	if r.LongContext != nil {
		if r.LongContext.ThresholdInputTokens <= 0 {
			return fmt.Errorf("long_context.threshold_input_tokens must be positive")
		}
		if r.LongContext.InputMultiplier <= 0 || r.LongContext.OutputMultiplier <= 0 {
			return fmt.Errorf("long_context multipliers must be positive")
		}
	}
	return nil
}

func validateImageCreditRate(r *types.ImageCreditRate) error {
	if r == nil {
		return nil
	}
	if r.TextInputRate < 0 || r.TextCachedInputRate < 0 || r.TextOutputRate < 0 ||
		r.ImageInputRate < 0 || r.ImageCachedInputRate < 0 || r.ImageOutputRate < 0 {
		return fmt.Errorf("image credit rates must be non-negative")
	}
	return nil
}

// isUniqueViolation reports whether err wraps a PostgreSQL unique-violation
// (pk collision on an existing canonical name).
func isUniqueViolation(err error) bool {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		return pgerr.Code == "23505"
	}
	return false
}
