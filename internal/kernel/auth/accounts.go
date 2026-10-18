package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// Password length limits, in characters. The API's schema repeats them in
// the struct tags of setupInput.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 1024
)

// ErrNoAccount is returned for a username no account has.
var ErrNoAccount = errors.New("no such account")

// Usernames lists the accounts in db.
func Usernames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT username FROM kernel_users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// SetPassword gives the account username a new password and ends all of
// its sessions.
func SetPassword(ctx context.Context, db *sql.DB, username, password string) error {
	if n := utf8.RuneCountInString(password); n < MinPasswordLength || n > MaxPasswordLength {
		return fmt.Errorf("a password has %d to %d characters", MinPasswordLength, MaxPasswordLength)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM kernel_users WHERE username = ?", username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrNoAccount, username)
	}
	if err != nil {
		return err
	}
	now := timestamp(time.Now())
	if _, err := tx.ExecContext(ctx, "UPDATE kernel_users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?",
		hash, now, now, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM kernel_sessions WHERE user_id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}
