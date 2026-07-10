package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/modelserver/modelserver/internal/types"
)

// visibilityWhereClause returns the SQL fragment (with $1 bound to userID
// downstream) that filters `notifications n` rows visible to that user:
// global OR project-I'm-in OR addressed-to-me, and always alive. The
// fragment references `n.` — callers must alias the notifications table
// as `n`. It is deliberately parameter-index-independent: callers append
// their own indexed args and know that $1 is userID.
const visibilityWhereClause = `
	n.deleted_at IS NULL
	AND (
		n.audience_type = 'global'
		OR (n.audience_type = 'project' AND n.audience_id IN (
			SELECT project_id FROM project_members WHERE user_id = $1
		))
		OR (n.audience_type = 'user' AND n.audience_id = $1)
	)
`

// CreateNotification inserts a new notification and populates id + timestamps.
func (s *Store) CreateNotification(n *types.Notification) error {
	return s.pool.QueryRow(context.Background(), `
		INSERT INTO notifications (title, body, audience_type, audience_id, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`,
		n.Title, n.Body, n.AudienceType, n.AudienceID, n.CreatedBy,
	).Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt)
}

// GetNotification returns the notification regardless of deleted_at; ReadCount
// is populated, ReadAt is not.
func (s *Store) GetNotification(id string) (*types.Notification, error) {
	var n types.Notification
	err := s.pool.QueryRow(context.Background(), `
		SELECT n.id, n.title, n.body, n.audience_type, n.audience_id,
		       n.created_by, n.created_at, n.updated_at, n.deleted_at,
		       (SELECT COUNT(*) FROM notification_reads r WHERE r.notification_id = n.id) AS read_count
		FROM notifications n
		WHERE n.id = $1`, id).
		Scan(&n.ID, &n.Title, &n.Body, &n.AudienceType, &n.AudienceID,
			&n.CreatedBy, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt, &n.ReadCount)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// UpdateNotification bumps updated_at and applies the new values. Returns
// pgx.ErrNoRows if the id does not exist or is already soft-deleted.
func (s *Store) UpdateNotification(id, title, body, audienceType string, audienceID *string) error {
	ct, err := s.pool.Exec(context.Background(), `
		UPDATE notifications
		   SET title = $2, body = $3, audience_type = $4, audience_id = $5, updated_at = NOW()
		 WHERE id = $1 AND deleted_at IS NULL`,
		id, title, body, audienceType, audienceID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SoftDeleteNotification marks the row deleted. Returns pgx.ErrNoRows if
// the id is unknown or already deleted (idempotent from the caller's
// perspective would be a design change — the handler layer maps this to
// 404 for the admin UI).
func (s *Store) SoftDeleteNotification(id string) error {
	ct, err := s.pool.Exec(context.Background(),
		`UPDATE notifications SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListAllNotifications returns notifications for admin. If includeDeleted
// is false, soft-deleted rows are hidden. audienceType filters by type
// when non-empty.
func (s *Store) ListAllNotifications(includeDeleted bool, audienceType string, p types.PaginationParams) ([]types.Notification, int, error) {
	where := "1=1"
	args := []interface{}{}
	if !includeDeleted {
		where += " AND n.deleted_at IS NULL"
	}
	if audienceType != "" {
		args = append(args, audienceType)
		where += fmt.Sprintf(" AND n.audience_type = $%d", len(args))
	}

	var total int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM notifications n WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	perPage := p.Limit()
	offset := (p.Page - 1) * perPage
	args = append(args, perPage, offset)
	rows, err := s.pool.Query(context.Background(), fmt.Sprintf(`
		SELECT n.id, n.title, n.body, n.audience_type, n.audience_id,
		       n.created_by, n.created_at, n.updated_at, n.deleted_at,
		       (SELECT COUNT(*) FROM notification_reads r WHERE r.notification_id = n.id) AS read_count
		FROM notifications n
		WHERE %s
		ORDER BY n.created_at DESC
		LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]types.Notification, 0, perPage)
	for rows.Next() {
		var n types.Notification
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.AudienceType, &n.AudienceID,
			&n.CreatedBy, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt, &n.ReadCount); err != nil {
			return nil, 0, err
		}
		items = append(items, n)
	}
	return items, total, rows.Err()
}

// ListVisibleForUser returns notifications visible to the user (global +
// project-I'm-in + addressed-to-me), alive only, with ReadAt populated
// via LEFT JOIN and AudienceName populated for display.
func (s *Store) ListVisibleForUser(userID string, p types.PaginationParams) ([]types.Notification, int, error) {
	var total int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM notifications n WHERE "+visibilityWhereClause, userID).Scan(&total); err != nil {
		return nil, 0, err
	}

	perPage := p.Limit()
	offset := (p.Page - 1) * perPage
	rows, err := s.pool.Query(context.Background(), `
		SELECT n.id, n.title, n.body, n.audience_type, n.audience_id,
		       n.created_by, n.created_at, n.updated_at, n.deleted_at,
		       r.read_at,
		       CASE
		         WHEN n.audience_type = 'global'  THEN ''
		         WHEN n.audience_type = 'user'    THEN 'You'
		         WHEN n.audience_type = 'project' THEN COALESCE(p.name, '(deleted project)')
		         ELSE ''
		       END AS audience_name
		FROM notifications n
		LEFT JOIN notification_reads r
		       ON r.notification_id = n.id AND r.user_id = $1
		LEFT JOIN projects p
		       ON n.audience_type = 'project' AND n.audience_id = p.id
		WHERE `+visibilityWhereClause+`
		ORDER BY n.created_at DESC
		LIMIT $2 OFFSET $3`, userID, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]types.Notification, 0, perPage)
	for rows.Next() {
		var n types.Notification
		var audienceName string
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.AudienceType, &n.AudienceID,
			&n.CreatedBy, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt, &n.ReadAt, &audienceName); err != nil {
			return nil, 0, err
		}
		n.AudienceName = &audienceName
		items = append(items, n)
	}
	return items, total, rows.Err()
}

// CountUnreadForUser is a COUNT(*) version of ListVisibleForUser with
// the read-null filter, used by the sidebar badge poller.
func (s *Store) CountUnreadForUser(userID string) (int, error) {
	var n int
	err := s.pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM notifications n
		LEFT JOIN notification_reads r
		       ON r.notification_id = n.id AND r.user_id = $1
		WHERE `+visibilityWhereClause+` AND r.read_at IS NULL`, userID).Scan(&n)
	return n, err
}

// MarkNotificationRead upserts a read row for (userID, notificationID)
// only if the notification is currently visible to that user. Silent
// no-op (returns nil, no row inserted) otherwise — the caller's HTTP
// handler always returns 200.
func (s *Store) MarkNotificationRead(userID, notificationID string) error {
	// Visibility guard: reuse the exact same WHERE the list uses so
	// invisible/deleted notifications insert nothing.
	_, err := s.pool.Exec(context.Background(), `
		INSERT INTO notification_reads (notification_id, user_id)
		SELECT n.id, $1
		FROM notifications n
		WHERE n.id = $2 AND `+visibilityWhereClause+`
		ON CONFLICT DO NOTHING`, userID, notificationID)
	return err
}

// MarkAllNotificationsRead inserts a read row for every currently-
// visible unread notification. Returns the number of rows inserted.
func (s *Store) MarkAllNotificationsRead(userID string) (int, error) {
	ct, err := s.pool.Exec(context.Background(), `
		INSERT INTO notification_reads (notification_id, user_id)
		SELECT n.id, $1
		FROM notifications n
		LEFT JOIN notification_reads r
		       ON r.notification_id = n.id AND r.user_id = $1
		WHERE `+visibilityWhereClause+` AND r.read_at IS NULL
		ON CONFLICT DO NOTHING`, userID)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}
