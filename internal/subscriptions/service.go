package subscriptions

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/types"
)

// Service manages subscriptions.
type Service struct {
	db *sql.DB
}

// NewService creates a new subscription service.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// Subscription represents a subscription record.
type Subscription struct {
	ID          int
	SessionID   string
	ScopeType   *string // nil = wildcard
	ScopeValue  *string
	MentionRole *string
	CreatedAt   string
}

// Subscribe creates a new subscription for the given session.
// At least one of scope, mentionRole, or all must be specified.
func (s *Service) Subscribe(sessionID string, scope *types.Scope, mentionRole *string, all bool) (*Subscription, error) {
	// Validation: at least one of scope, mentionRole, or all must be specified
	if scope == nil && mentionRole == nil && !all {
		return nil, fmt.Errorf("at least one of scope, mention_role, or all must be specified")
	}

	// Prepare scope fields
	var scopeType, scopeValue *string
	if scope != nil {
		scopeType = &scope.Type
		scopeValue = &scope.Value
	}

	// For "all" subscriptions, both scope and mention_role should be NULL
	if all {
		scopeType = nil
		scopeValue = nil
		mentionRole = nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Check for existing subscription (SQLite UNIQUE constraint doesn't handle NULLs correctly)
	exists, err := s.subscriptionExists(sessionID, scopeType, scopeValue, mentionRole)
	if err != nil {
		return nil, fmt.Errorf("check subscription exists: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("subscription already exists")
	}

	// Insert subscription
	query := `INSERT INTO subscriptions (session_id, scope_type, scope_value, mention_role, created_at)
	          VALUES (?, ?, ?, ?, ?)`

	result, err := s.db.Exec(query, sessionID, scopeType, scopeValue, mentionRole, now)
	if err != nil {
		return nil, fmt.Errorf("insert subscription: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get subscription ID: %w", err)
	}

	return &Subscription{
		ID:          int(id),
		SessionID:   sessionID,
		ScopeType:   scopeType,
		ScopeValue:  scopeValue,
		MentionRole: mentionRole,
		CreatedAt:   now,
	}, nil
}

// Unsubscribe removes a subscription by ID, but only if it belongs to the given session.
func (s *Service) Unsubscribe(subscriptionID int, sessionID string) (bool, error) {
	query := `DELETE FROM subscriptions WHERE id = ? AND session_id = ?`
	result, err := s.db.Exec(query, subscriptionID, sessionID)
	if err != nil {
		return false, fmt.Errorf("delete subscription: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("get rows affected: %w", err)
	}

	return rowsAffected > 0, nil
}

// ClearBySession removes all subscriptions for the given session.
// Called when a WebSocket client disconnects to prevent "already exists" errors on reconnect.
func (s *Service) ClearBySession(sessionID string) (int, error) {
	query := `DELETE FROM subscriptions WHERE session_id = ?`
	result, err := s.db.Exec(query, sessionID)
	if err != nil {
		return 0, fmt.Errorf("delete subscriptions for session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("get rows affected: %w", err)
	}

	return int(rowsAffected), nil
}

// List returns all subscriptions for the given session.
func (s *Service) List(sessionID string) ([]Subscription, error) {
	query := `SELECT id, session_id, scope_type, scope_value, mention_role, created_at
	          FROM subscriptions
	          WHERE session_id = ?
	          ORDER BY created_at DESC`

	rows, err := s.db.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var subscriptions []Subscription
	for rows.Next() {
		var sub Subscription
		var scopeType, scopeValue, mentionRole sql.NullString

		err := rows.Scan(&sub.ID, &sub.SessionID, &scopeType, &scopeValue, &mentionRole, &sub.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}

		if scopeType.Valid {
			sub.ScopeType = &scopeType.String
		}
		if scopeValue.Valid {
			sub.ScopeValue = &scopeValue.String
		}
		if mentionRole.Valid {
			sub.MentionRole = &mentionRole.String
		}

		subscriptions = append(subscriptions, sub)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions: %w", err)
	}

	return subscriptions, nil
}

// subscriptionExists checks if a subscription with the exact same parameters already exists.
// This is needed because SQLite's UNIQUE constraint doesn't treat NULL values as equal.
func (s *Service) subscriptionExists(sessionID string, scopeType, scopeValue, mentionRole *string) (bool, error) {
	var query string
	var args []any

	// Build query based on which fields are NULL
	if scopeType == nil && scopeValue == nil && mentionRole == nil {
		// All NULL (wildcard/all subscription)
		query = `SELECT EXISTS(
			SELECT 1 FROM subscriptions
			WHERE session_id = ?
			  AND scope_type IS NULL
			  AND scope_value IS NULL
			  AND mention_role IS NULL
		)`
		args = []any{sessionID}
	} else if scopeType != nil && scopeValue != nil && mentionRole == nil {
		// Scope subscription without mention_role
		query = `SELECT EXISTS(
			SELECT 1 FROM subscriptions
			WHERE session_id = ?
			  AND scope_type = ?
			  AND scope_value = ?
			  AND mention_role IS NULL
		)`
		args = []any{sessionID, *scopeType, *scopeValue}
	} else if scopeType == nil && scopeValue == nil && mentionRole != nil {
		// Mention-only subscription
		query = `SELECT EXISTS(
			SELECT 1 FROM subscriptions
			WHERE session_id = ?
			  AND scope_type IS NULL
			  AND scope_value IS NULL
			  AND mention_role = ?
		)`
		args = []any{sessionID, *mentionRole}
	} else if scopeType != nil && scopeValue != nil && mentionRole != nil {
		// Both scope and mention_role
		query = `SELECT EXISTS(
			SELECT 1 FROM subscriptions
			WHERE session_id = ?
			  AND scope_type = ?
			  AND scope_value = ?
			  AND mention_role = ?
		)`
		args = []any{sessionID, *scopeType, *scopeValue, *mentionRole}
	} else {
		// Invalid combination (e.g., only scope_type without scope_value)
		return false, fmt.Errorf("invalid subscription parameter combination")
	}

	var exists bool
	err := s.db.QueryRow(query, args...).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query subscription exists: %w", err)
	}

	return exists, nil
}
