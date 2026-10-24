package repository

import (
	"context"
	"testing"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
)

func TestAIMessagesListFiltersAndOrders(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	save := func(chat, sender, role, content string, at time.Time) {
		t.Helper()
		err := db.SaveAIMessage(ctx, model.AIMessage{ChatJID: chat, SenderJID: sender, Role: role, Content: content, CreatedAt: at})
		if err != nil {
			t.Fatalf("SaveAIMessage failed: %v", err)
		}
	}
	save("chat1", "alice", model.AIRoleUser, "old", base.Add(-48*time.Hour))
	save("chat1", "alice", model.AIRoleUser, "q1", base)
	save("chat1", "alice", model.AIRoleAssistant, "a1", base.Add(time.Second))
	save("chat1", "alice", model.AIRoleUser, "q2", base.Add(2*time.Second))
	save("chat1", "bob", model.AIRoleUser, "other sender", base)
	save("chat2", "alice", model.AIRoleUser, "other chat", base)

	tests := []struct {
		name         string
		chat, sender string
		since        time.Time
		limit        int
		want         []string
	}{
		{"since cuts off old rows, oldest first", "chat1", "alice", base.Add(-time.Hour), 10, []string{"q1", "a1", "q2"}},
		{"limit keeps the most recent rows", "chat1", "alice", base.Add(-time.Hour), 2, []string{"a1", "q2"}},
		{"no since returns everything", "chat1", "alice", time.Time{}, 10, []string{"old", "q1", "a1", "q2"}},
		{"sender isolation", "chat1", "bob", base.Add(-time.Hour), 10, []string{"other sender"}},
		{"chat isolation", "chat2", "alice", base.Add(-time.Hour), 10, []string{"other chat"}},
		{"unknown conversation", "chat9", "alice", base.Add(-time.Hour), 10, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs, err := db.ListAIMessages(ctx, tt.chat, tt.sender, tt.since, tt.limit)
			if err != nil {
				t.Fatalf("ListAIMessages failed: %v", err)
			}
			var got []string
			for _, m := range msgs {
				got = append(got, m.Content)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestAIMessagesSameInstantKeepInsertionOrder(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	at := time.Now()

	for _, c := range []string{"first", "second", "third"} {
		if err := db.SaveAIMessage(ctx, model.AIMessage{ChatJID: "c", SenderJID: "s", Role: model.AIRoleUser, Content: c, CreatedAt: at}); err != nil {
			t.Fatalf("SaveAIMessage failed: %v", err)
		}
	}
	msgs, err := db.ListAIMessages(ctx, "c", "s", at.Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("ListAIMessages failed: %v", err)
	}
	if len(msgs) != 3 || msgs[0].Content != "first" || msgs[1].Content != "second" || msgs[2].Content != "third" {
		t.Fatalf("unexpected order: %+v", msgs)
	}
	if msgs[0].Role != model.AIRoleUser || msgs[0].ChatJID != "c" || msgs[0].SenderJID != "s" {
		t.Errorf("fields not round-tripped: %+v", msgs[0])
	}
}

func TestSaveAIMessageDefaultsCreatedAt(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.SaveAIMessage(ctx, model.AIMessage{ChatJID: "c", SenderJID: "s", Role: model.AIRoleUser, Content: "hi"}); err != nil {
		t.Fatalf("SaveAIMessage failed: %v", err)
	}
	msgs, err := db.ListAIMessages(ctx, "c", "s", time.Now().Add(-time.Minute), 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListAIMessages = %v, %v; want 1 recent row", msgs, err)
	}
}

func TestPurgeAIMessages(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now()

	for _, m := range []struct {
		content string
		at      time.Time
	}{{"ancient", now.Add(-10 * 24 * time.Hour)}, {"stale", now.Add(-8 * 24 * time.Hour)}, {"fresh", now.Add(-time.Hour)}} {
		if err := db.SaveAIMessage(ctx, model.AIMessage{ChatJID: "c", SenderJID: "s", Role: model.AIRoleUser, Content: m.content, CreatedAt: m.at}); err != nil {
			t.Fatalf("SaveAIMessage failed: %v", err)
		}
	}

	if err := db.PurgeAIMessages(ctx, now.Add(-7*24*time.Hour)); err != nil {
		t.Fatalf("PurgeAIMessages failed: %v", err)
	}
	msgs, err := db.ListAIMessages(ctx, "c", "s", time.Time{}, 10)
	if err != nil {
		t.Fatalf("ListAIMessages failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "fresh" {
		t.Fatalf("after purge got %+v, want only the fresh row", msgs)
	}
}
