package repository

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/pressly/goose/v3"
)

func chatBodies(msgs []model.ChatMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Body)
	}
	return out
}

func TestListChatContext(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	save := func(chat, id, body string, at time.Time) {
		t.Helper()
		if err := db.SaveChatMessage(ctx, model.ChatMessage{ChatJID: chat, MessageID: id, SenderJID: "alice", Body: body, CreatedAt: at}); err != nil {
			t.Fatalf("SaveChatMessage failed: %v", err)
		}
	}
	save("g1", "m0", "old", base.Add(-48*time.Hour))
	save("g1", "m1", "aaaa", base)
	save("g1", "m2", "bbbb", base.Add(time.Second))
	save("g1", "m3", "cccc", base.Add(2*time.Second))
	save("g2", "m4", "other chat", base)

	tests := []struct {
		name     string
		chat     string
		since    time.Time
		maxChars int
		want     []string
	}{
		{"since cuts off old rows, chronological", "g1", base.Add(-time.Hour), 1000, []string{"aaaa", "bbbb", "cccc"}},
		{"budget keeps the newest rows", "g1", base.Add(-time.Hour), 8, []string{"bbbb", "cccc"}},
		{"budget stops strictly before overflowing", "g1", base.Add(-time.Hour), 11, []string{"bbbb", "cccc"}},
		{"budget smaller than the newest row", "g1", base.Add(-time.Hour), 3, nil},
		{"no budget limit when zero", "g1", time.Time{}, 0, []string{"old", "aaaa", "bbbb", "cccc"}},
		{"chat isolation", "g2", base.Add(-time.Hour), 1000, []string{"other chat"}},
		{"unknown chat", "g9", base.Add(-time.Hour), 1000, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs, err := db.ListChatContext(ctx, tt.chat, tt.since, tt.maxChars)
			if err != nil {
				t.Fatalf("ListChatContext failed: %v", err)
			}
			if got := chatBodies(msgs); strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListChatContextCountsCharactersNotBytes(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.SaveChatMessage(ctx, model.ChatMessage{ChatJID: "g", MessageID: "m", Body: "ñññ"}); err != nil {
		t.Fatalf("SaveChatMessage failed: %v", err)
	}
	msgs, err := db.ListChatContext(ctx, "g", time.Time{}, 3)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListChatContext = %v, %v; want the 3-character row", msgs, err)
	}
}

func TestSaveChatMessageRoundTripAndDuplicates(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 10, 12, 0, 0, 123e6, time.UTC)

	in := model.ChatMessage{ChatJID: "g", MessageID: "m1", SenderJID: "lid", SenderAltJID: "phone", PushName: "Alice", Body: "hola", CreatedAt: at}
	for i := 0; i < 2; i++ { // the second insert is a duplicate event and is ignored
		if err := db.SaveChatMessage(ctx, in); err != nil {
			t.Fatalf("SaveChatMessage #%d failed: %v", i, err)
		}
	}
	// Same message ID in another chat is a different message
	if err := db.SaveChatMessage(ctx, model.ChatMessage{ChatJID: "g2", MessageID: "m1", Body: "x", CreatedAt: at}); err != nil {
		t.Fatalf("SaveChatMessage failed: %v", err)
	}
	// Bot replies without message ID get a generated, unique one
	for _, body := range []string{"r1", "r2"} {
		if err := db.SaveChatMessage(ctx, model.ChatMessage{ChatJID: "g", SenderJID: "bot", PushName: "Bot", Body: body, IsBot: true, CreatedAt: at.Add(time.Second)}); err != nil {
			t.Fatalf("SaveChatMessage bot failed: %v", err)
		}
	}

	msgs, err := db.ListChatContext(ctx, "g", time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListChatContext failed: %v", err)
	}
	if got := chatBodies(msgs); strings.Join(got, ",") != "hola,r1,r2" {
		t.Fatalf("got %v, want [hola r1 r2]", got)
	}
	got := msgs[0]
	got.ID = in.ID
	if got != in {
		t.Errorf("round trip = %+v, want %+v", got, in)
	}
	if msgs[0].ID.Version() != 7 {
		t.Errorf("id %s is not a UUID v7", msgs[0].ID)
	}
	if !msgs[1].IsBot || msgs[1].MessageID == "" || msgs[1].MessageID == msgs[2].MessageID {
		t.Errorf("bot rows = %+v / %+v, want is_bot and distinct generated message IDs", msgs[1], msgs[2])
	}
}

func TestSaveChatMessageDefaultsCreatedAt(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.SaveChatMessage(ctx, model.ChatMessage{ChatJID: "g", MessageID: "m", Body: "hi"}); err != nil {
		t.Fatalf("SaveChatMessage failed: %v", err)
	}
	msgs, err := db.ListChatContext(ctx, "g", time.Now().Add(-time.Minute), 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListChatContext = %v, %v; want 1 recent row", msgs, err)
	}
}

func TestPurgeChatMessages(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now()

	for i, m := range []struct {
		body string
		at   time.Time
	}{{"ancient", now.Add(-200 * 24 * time.Hour)}, {"stale", now.Add(-181 * 24 * time.Hour)}, {"fresh", now.Add(-time.Hour)}} {
		if err := db.SaveChatMessage(ctx, model.ChatMessage{ChatJID: "g", MessageID: fmt.Sprint(i), Body: m.body, CreatedAt: m.at}); err != nil {
			t.Fatalf("SaveChatMessage failed: %v", err)
		}
	}

	if err := db.PurgeChatMessages(ctx, now.Add(-180*24*time.Hour)); err != nil {
		t.Fatalf("PurgeChatMessages failed: %v", err)
	}
	msgs, err := db.ListChatContext(ctx, "g", time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListChatContext failed: %v", err)
	}
	if got := chatBodies(msgs); len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("after purge got %v, want only the fresh row", got)
	}
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return n == 1
}

// Migration 005 replaces ai_messages with chat_messages, and its Down restores
// the 004 schema.
func TestMigration005ReplacesAIMessages(t *testing.T) {
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "m.sqlite"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.UpTo(raw, "migrations/sqlite", 4); err != nil {
		t.Fatalf("migrate to 004: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO ai_messages (id, chat_jid, sender_jid, role, content, created_at) VALUES ('x', 'g', 's', 'user', 'hi', 1)`); err != nil {
		t.Fatalf("seed ai_messages: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	d := &DB{db: raw, dialect: "sqlite", log: log}
	if err := d.migrate(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if tableExists(t, raw, "ai_messages") || !tableExists(t, raw, "chat_messages") {
		t.Fatal("after 005 ai_messages must be gone and chat_messages present")
	}

	if err := goose.DownTo(raw, "migrations/sqlite", 4); err != nil {
		t.Fatalf("migrate down to 004: %v", err)
	}
	if !tableExists(t, raw, "ai_messages") || tableExists(t, raw, "chat_messages") {
		t.Fatal("after 005 down ai_messages must be back and chat_messages gone")
	}
	if _, err := raw.Exec(`INSERT INTO ai_messages (id, chat_jid, sender_jid, role, content, created_at) VALUES ('y', 'g', 's', 'user', 'hi', 1)`); err != nil {
		t.Fatalf("restored ai_messages schema is not usable: %v", err)
	}
}
