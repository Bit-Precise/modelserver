package admin

import "github.com/google/uuid"

// sameProjectID compares project identifiers using UUID semantics. PostgreSQL
// accepts equivalent UUID spellings (for example upper-case hex), while
// values scanned from a UUID column are canonical lower-case strings. Falling
// back to exact equality keeps the helper safe for non-UUID identifiers used
// by tests and any future non-UUID storage backend.
func sameProjectID(left, right string) bool {
	if left == right {
		return true
	}
	leftUUID, leftErr := uuid.Parse(left)
	rightUUID, rightErr := uuid.Parse(right)
	return leftErr == nil && rightErr == nil && leftUUID == rightUUID
}
