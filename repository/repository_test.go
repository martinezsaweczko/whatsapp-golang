package repository

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/martinezsaweczko/whatsappBot-golang/model"
)

// newTestDB creates a repository backed by a temporary SQLite database
func newTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	db, err := New(Config{
		Driver:     "sqlite",
		DSN:        fmt.Sprintf("file:%s?_foreign_keys=on", dbPath),
		AutoCreate: true,
	}, log)
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSubscriptionsLifecycle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Empty initially
	subs, err := db.ReturnSubscriptions(ctx, "user1@c.us")
	if err != nil {
		t.Fatalf("ReturnSubscriptions failed: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected 0 subscriptions, got %d", len(subs))
	}

	// Save two
	if err := db.SaveSubscription(ctx, "Mundo", "user1@c.us"); err != nil {
		t.Fatalf("SaveSubscription failed: %v", err)
	}
	if err := db.SaveSubscription(ctx, "País", "user1@c.us"); err != nil {
		t.Fatalf("SaveSubscription failed: %v", err)
	}
	if err := db.SaveSubscription(ctx, "Mundo", "user2@c.us"); err != nil {
		t.Fatalf("SaveSubscription failed: %v", err)
	}

	subs, err = db.ReturnSubscriptions(ctx, "user1@c.us")
	if err != nil {
		t.Fatalf("ReturnSubscriptions failed: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions, got %d", len(subs))
	}

	// Delete only user1's
	if err := db.DeleteSubscription(ctx, "user1@c.us"); err != nil {
		t.Fatalf("DeleteSubscription failed: %v", err)
	}
	subs, _ = db.ReturnSubscriptions(ctx, "user1@c.us")
	if len(subs) != 0 {
		t.Fatalf("expected 0 subscriptions after delete, got %d", len(subs))
	}
	subs, _ = db.ReturnSubscriptions(ctx, "user2@c.us")
	if len(subs) != 1 {
		t.Fatalf("user2 subscriptions should be untouched, got %d", len(subs))
	}
}

func TestMatchSubscriptions(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	_ = db.SaveSubscription(ctx, "economist", "user1@c.us")
	_ = db.SaveSubscription(ctx, "MUNDO", "user2@c.us")
	_ = db.SaveSubscription(ctx, "deportes", "user3@c.us")
	// user4 subscribes twice to overlapping terms; should appear once
	_ = db.SaveSubscription(ctx, "el", "user4@c.us")
	_ = db.SaveSubscription(ctx, "mundo", "user4@c.us")

	matches, err := db.MatchSubscriptions(ctx, "ElMundo_2024-01-01.pdf")
	if err != nil {
		t.Fatalf("MatchSubscriptions failed: %v", err)
	}

	found := map[string]bool{}
	for _, m := range matches {
		found[m] = true
	}

	if !found["user2@c.us"] || !found["user4@c.us"] {
		t.Errorf("expected user2 and user4 to match, got %v", matches)
	}
	if found["user1@c.us"] || found["user3@c.us"] {
		t.Errorf("user1/user3 should not match, got %v", matches)
	}

	// Case-insensitive: file in upper case
	matches, err = db.MatchSubscriptions(ctx, "THEECONOMIST_WEEKLY.PDF")
	if err != nil {
		t.Fatalf("MatchSubscriptions failed: %v", err)
	}
	if len(matches) != 1 || matches[0] != "user1@c.us" {
		t.Errorf("expected only user1 to match, got %v", matches)
	}
}

func TestTokenLifecycle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	count, err := db.CountTokenUses(ctx, "token-abc")
	if err != nil {
		t.Fatalf("CountTokenUses failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 uses, got %d", count)
	}

	for i := 1; i <= 3; i++ {
		if err := db.SaveToken(ctx, "token-abc"); err != nil {
			t.Fatalf("SaveToken failed: %v", err)
		}
		count, _ = db.CountTokenUses(ctx, "token-abc")
		if count != i {
			t.Fatalf("expected %d uses, got %d", i, count)
		}
	}

	// CleanJWT should not delete fresh tokens
	if err := db.CleanJWT(ctx); err != nil {
		t.Fatalf("CleanJWT failed: %v", err)
	}
	count, _ = db.CountTokenUses(ctx, "token-abc")
	if count != 3 {
		t.Fatalf("fresh tokens should survive cleanup, got %d", count)
	}
}

func TestUsageReports(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.ReportFileUsage(ctx, "file.pdf", 200); err != nil {
		t.Fatalf("ReportFileUsage failed: %v", err)
	}
	if err := db.ReportUserUsage(ctx, "David", "file.pdf"); err != nil {
		t.Fatalf("ReportUserUsage failed: %v", err)
	}

	var fileCount, userCount int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM file_usage").Scan(&fileCount); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if err := db.db.QueryRow("SELECT COUNT(*) FROM user_usage").Scan(&userCount); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if fileCount != 1 || userCount != 1 {
		t.Fatalf("expected 1/1 usage rows, got %d/%d", fileCount, userCount)
	}
}

func TestSQLInjectionResistance(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Attempt SQL injection through subscription text and user
	malicious := "'); DROP TABLE subscriptions; --"
	if err := db.SaveSubscription(ctx, malicious, "user@c.us"); err != nil {
		t.Fatalf("SaveSubscription failed: %v", err)
	}

	// Table must still exist and contain the literal malicious string
	subs, err := db.ReturnSubscriptions(ctx, "user@c.us")
	if err != nil {
		t.Fatalf("table was dropped! %v", err)
	}
	if len(subs) != 1 || subs[0].SubscriptionText != malicious {
		t.Fatalf("injection string not stored literally: %+v", subs)
	}
}

func TestValidateDBName(t *testing.T) {
	tests := []struct {
		name    string
		dbName  string
		wantErr bool
	}{
		{"valid letters", "whatsappbot", false},
		{"valid underscore", "whatsapp_bot", false},
		{"valid hyphen", "whatsapp-bot", false},
		{"empty", "", true},
		{"invalid space", "whatsapp bot", true},
		{"invalid semicolon", "whatsapp;bot", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDBName(tc.dbName)
			if tc.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCreateMySQLDatabaseIfNeededInvalidDSN(t *testing.T) {
	err := createMySQLDatabaseIfNeeded("not-a-valid-dsn")
	if err == nil {
		t.Fatal("expected error for invalid DSN")
	}
}

func TestScheduledCommandsLifecycle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	cmd := model.ScheduledCommand{
		Name:     "morning electricity",
		Schedule: "0 8 * * *",
		Command:  "electricidad",
		GroupJID: "123456789@g.us",
		Enabled:  true,
	}

	id, err := db.CreateScheduledCommand(ctx, cmd)
	if err != nil {
		t.Fatalf("CreateScheduledCommand failed: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("expected non-nil id")
	}

	cmds, err := db.ListScheduledCommands(ctx)
	if err != nil {
		t.Fatalf("ListScheduledCommands failed: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 scheduled command, got %d", len(cmds))
	}
	if cmds[0].Name != cmd.Name {
		t.Fatalf("unexpected name: %s", cmds[0].Name)
	}
	if cmds[0].ID != id {
		t.Fatalf("unexpected id: got %s, want %s", cmds[0].ID, id)
	}

	if err := db.DeleteScheduledCommand(ctx, id); err != nil {
		t.Fatalf("DeleteScheduledCommand failed: %v", err)
	}

	cmds, err = db.ListScheduledCommands(ctx)
	if err != nil {
		t.Fatalf("ListScheduledCommands after delete failed: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("expected 0 scheduled commands after delete, got %d", len(cmds))
	}
}
