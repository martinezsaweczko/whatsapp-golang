// Package repository contains the data access layer.
// All queries use parameterized statements.
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	_ "modernc.org/sqlite"
)

// DB provides access to the application database.
// It implements the store interfaces consumed by the services and HTTP layers.
type DB struct {
	db  *sql.DB
	log *slog.Logger
}

// New opens (creating if necessary) the SQLite database at dbPath and initializes the schema
func New(dbPath string, log *slog.Logger) (*DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database %s: %w", dbPath, err)
	}

	// SQLite does not support concurrent writers; a single connection avoids SQLITE_BUSY errors
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to connect to database %s: %w", dbPath, err)
	}

	d := &DB{db: db, log: log}
	if err := d.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	log.Info("Connected successfully to DB", "path", dbPath)
	return d, nil
}

// Close closes the underlying database
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) initSchema() error {
	statements := []struct {
		name string
		stmt string
	}{
		{"subscriptions", `CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subscription_text TEXT,
			user TEXT
		)`},
		{"jwt_used", `CREATE TABLE IF NOT EXISTS jwt_used (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			jwt TEXT,
			created_date DATETIME DEFAULT CURRENT_TIMESTAMP
		)`},
		{"jwt_index", `CREATE INDEX IF NOT EXISTS jwt_index ON jwt_used (jwt)`},
		{"file_usage", `CREATE TABLE IF NOT EXISTS file_usage (
			file TEXT,
			result TEXT,
			created_date DATETIME DEFAULT CURRENT_TIMESTAMP
		)`},
		{"user_usage", `CREATE TABLE IF NOT EXISTS user_usage (
			user TEXT,
			file TEXT,
			created_date DATETIME DEFAULT CURRENT_TIMESTAMP
		)`},
	}

	for _, s := range statements {
		if _, err := d.db.Exec(s.stmt); err != nil {
			return fmt.Errorf("error creating %s: %w", s.name, err)
		}
	}
	d.log.Debug("Database schema initialized")
	return nil
}

// SaveSubscription stores a new keyword subscription for a user
func (d *DB) SaveSubscription(ctx context.Context, subscriptionText, user string) error {
	_, err := d.db.ExecContext(ctx,
		"INSERT INTO subscriptions (subscription_text, user) VALUES (?, ?)",
		subscriptionText, user)
	if err != nil {
		return fmt.Errorf("failed to save subscription: %w", err)
	}
	return nil
}

// DeleteSubscription removes all subscriptions of a user
func (d *DB) DeleteSubscription(ctx context.Context, user string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM subscriptions WHERE user = ?", user)
	if err != nil {
		return fmt.Errorf("failed to delete subscriptions: %w", err)
	}
	return nil
}

// ReturnSubscriptions returns all subscriptions of a user
func (d *DB) ReturnSubscriptions(ctx context.Context, user string) ([]model.Subscription, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT id, subscription_text, user FROM subscriptions WHERE user = ?", user)
	if err != nil {
		return nil, fmt.Errorf("failed to query subscriptions: %w", err)
	}
	defer rows.Close()

	var subs []model.Subscription
	for rows.Next() {
		var s model.Subscription
		if err := rows.Scan(&s.ID, &s.SubscriptionText, &s.User); err != nil {
			return nil, fmt.Errorf("failed to scan subscription: %w", err)
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// MatchSubscriptions returns the distinct users whose subscription text is contained in the given file name (case-insensitive)
func (d *DB) MatchSubscriptions(ctx context.Context, file string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT DISTINCT user FROM subscriptions WHERE INSTR(UPPER(?), UPPER(subscription_text)) > 0", file)
	if err != nil {
		return nil, fmt.Errorf("failed to match subscriptions: %w", err)
	}
	defer rows.Close()

	var users []string
	for rows.Next() {
		var user string
		if err := rows.Scan(&user); err != nil {
			return nil, fmt.Errorf("failed to scan matched user: %w", err)
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// CountTokenUses returns how many times a JWT has been used (for replay protection)
func (d *DB) CountTokenUses(ctx context.Context, jwt string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM jwt_used WHERE jwt = ?", jwt).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count token uses: %w", err)
	}
	return count, nil
}

// SaveToken records a JWT usage
func (d *DB) SaveToken(ctx context.Context, jwt string) error {
	_, err := d.db.ExecContext(ctx, "INSERT INTO jwt_used (jwt) VALUES (?)", jwt)
	if err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}
	return nil
}

// CleanJWT removes JWT records older than 5 days
func (d *DB) CleanJWT(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx,
		"DELETE FROM jwt_used WHERE created_date < datetime('now', '-5 days')")
	if err != nil {
		return fmt.Errorf("failed to clean old JWTs: %w", err)
	}
	return nil
}

// ReportFileUsage records an access attempt to a file with its HTTP result code
func (d *DB) ReportFileUsage(ctx context.Context, file string, result int) error {
	_, err := d.db.ExecContext(ctx,
		"INSERT INTO file_usage (file, result) VALUES (?, ?)", file, result)
	if err != nil {
		return fmt.Errorf("failed to report file usage: %w", err)
	}
	return nil
}

// ReportUserUsage records that a user requested a download link for a file
func (d *DB) ReportUserUsage(ctx context.Context, user, file string) error {
	_, err := d.db.ExecContext(ctx,
		"INSERT INTO user_usage (user, file) VALUES (?, ?)", user, file)
	if err != nil {
		return fmt.Errorf("failed to report user usage: %w", err)
	}
	return nil
}
