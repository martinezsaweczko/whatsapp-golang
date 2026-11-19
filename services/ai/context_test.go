package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/martinezsaweczko/whatsappBot-golang/whatsapp"
)

// 09:30 UTC is 10:30 in Madrid in November (UTC+1)
var now = time.Date(2026, 11, 19, 9, 30, 0, 0, time.UTC)

func TestFallbackGroupSendsTranscript(t *testing.T) {
	server := newFakeOpenAI(t, "respuesta")
	store := &fakeStore{stored: []model.ChatMessage{
		{ChatJID: group.String(), MessageID: "M1", SenderJID: alice.String(), PushName: "Alice", Body: "hola a todos", CreatedAt: now.Add(-25 * time.Hour)},
		{ChatJID: group.String(), MessageID: "M2", SenderJID: bob.String(), PushName: "", Body: "buenas", CreatedAt: now.Add(-2 * time.Hour)},
		{ChatJID: group.String(), MessageID: "M3", SenderJID: aliceLID.String(), PushName: "Alice", Body: "desde el LID", CreatedAt: now.Add(-90 * time.Minute)},
		{ChatJID: group.String(), MessageID: "M4", SenderJID: "999@lid", SenderAltJID: alice.String(), PushName: "Ali", Body: "por alt", CreatedAt: now.Add(-80 * time.Minute)},
		{ChatJID: group.String(), MessageID: "B1", SenderJID: botJID, PushName: "Bot", Body: "soy el bot", IsBot: true, CreatedAt: now.Add(-time.Hour)},
		{ChatJID: group.String(), MessageID: "Q1", SenderJID: alice.String(), PushName: "Alice", Body: "@bot qué dijo bob?", CreatedAt: now},
	}}
	svc := newTestService(t, server, nil, store)
	svc.now = func() time.Time { return now }

	msg := mention("@bot qué dijo bob?")
	msg.SenderAlt = aliceLID
	if err := svc.Fallback()(context.Background(), &fakeSender{}, msg); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(store.listCalls) != 1 {
		t.Fatalf("expected one context load, got %d", len(store.listCalls))
	}
	call := store.listCalls[0]
	if call.chat != group.String() || !call.since.Equal(now.Add(-180*24*time.Hour)) || call.maxChars != 1000 {
		t.Errorf("context requested as %+v", call)
	}

	transcript := "[2026-11-18 09:30] Alice (QUIEN PREGUNTA): hola a todos\n" +
		"[2026-11-19 08:30] 34600111222: buenas\n" +
		"[2026-11-19 09:00] Alice (QUIEN PREGUNTA): desde el LID\n" +
		"[2026-11-19 09:10] Ali (QUIEN PREGUNTA): por alt\n" +
		"[2026-11-19 09:30] Bot: soy el bot"
	want := []chatMessage{
		{"system", systemPrompt + "\n\n" + transcriptPrompt},
		{"user", transcript},
		{"user", "@bot qué dijo bob?"},
	}
	got := server.requests[0]
	if len(got) != len(want) {
		t.Fatalf("messages = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message %d = %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestFallbackGroupTrollKeepsTranscriptExplanation(t *testing.T) {
	server := newFakeOpenAI(t, "ok")
	store := &fakeStore{stored: []model.ChatMessage{
		{ChatJID: group.String(), MessageID: "M1", SenderJID: bob.String(), PushName: "Bob", Body: "hola", CreatedAt: now},
	}}
	svc := newTestService(t, server, []string{alice.String()}, store)

	if err := svc.Fallback()(context.Background(), &fakeSender{}, mention("pregunta")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	if got := server.requests[0][0]; got.Content != trollPrompt+"\n\n"+transcriptPrompt {
		t.Errorf("system message = %q", got.Content)
	}
}

func TestFallbackGroupStoresBotReply(t *testing.T) {
	server := newFakeOpenAI(t, "respuesta")
	store := &fakeStore{}
	svc := newTestService(t, server, nil, store)
	svc.now = func() time.Time { return now }

	if err := svc.Fallback()(context.Background(), &fakeSender{}, mention("pregunta")); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(store.stored) != 1 {
		t.Fatalf("stored %d messages, want only the bot reply: %+v", len(store.stored), store.stored)
	}
	want := model.ChatMessage{ChatJID: group.String(), SenderJID: botJID, PushName: "Bot", Body: "respuesta", IsBot: true, CreatedAt: now}
	if store.stored[0] != want {
		t.Errorf("stored %+v, want %+v", store.stored[0], want)
	}
}

func TestFallbackPrivateChatSendsNoHistory(t *testing.T) {
	server := newFakeOpenAI(t, "respuesta")
	store := &fakeStore{stored: []model.ChatMessage{
		{ChatJID: privateJID.String(), MessageID: "M1", SenderJID: alice.String(), Body: "antes"},
	}}
	svc := newTestService(t, server, nil, store)
	msg := whatsapp.IncomingMessage{ID: "Q1", Body: "hola", Sender: alice, Chat: privateJID}

	if err := svc.Fallback()(context.Background(), &fakeSender{}, msg); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if len(store.listCalls) != 0 || len(store.stored) != 1 {
		t.Errorf("private chats must not load or store history: lists=%d stored=%+v", len(store.listCalls), store.stored)
	}
	if got := server.requests[0]; len(got) != 2 || got[0] != (chatMessage{"system", systemPrompt}) || got[1] != (chatMessage{"user", "hola"}) {
		t.Errorf("unexpected messages: %+v", got)
	}
}

func TestRecordMessage(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, newFakeOpenAI(t, "x"), nil, store)
	svc.now = func() time.Time { return now }
	at := now.Add(-time.Minute)

	svc.RecordMessage(context.Background(), whatsapp.IncomingMessage{
		ID: "M1", Chat: group, Sender: aliceLID, SenderAlt: alice, PushName: "Alice", Body: "hola", IsGroup: true, Timestamp: at,
	})
	svc.RecordMessage(context.Background(), whatsapp.IncomingMessage{ID: "M2", Chat: group, Sender: bob, Body: "sin hora", IsGroup: true})
	// A device-specific sender is stored without its device suffix
	bobWeb := bob
	bobWeb.Device = 3
	svc.RecordMessage(context.Background(), whatsapp.IncomingMessage{ID: "M3", Chat: group, Sender: bobWeb, Body: "desde web", IsGroup: true, Timestamp: at})

	want := []model.ChatMessage{
		{ChatJID: group.String(), MessageID: "M1", SenderJID: aliceLID.String(), SenderAltJID: alice.String(), PushName: "Alice", Body: "hola", CreatedAt: at},
		{ChatJID: group.String(), MessageID: "M2", SenderJID: bob.String(), Body: "sin hora", CreatedAt: now},
		{ChatJID: group.String(), MessageID: "M3", SenderJID: bob.String(), Body: "desde web", CreatedAt: at},
	}
	if len(store.stored) != len(want) {
		t.Fatalf("stored %+v, want %+v", store.stored, want)
	}
	for i := range want {
		if store.stored[i] != want[i] {
			t.Errorf("stored[%d] = %+v, want %+v", i, store.stored[i], want[i])
		}
	}
}

func TestRecordMessageFailureIsSwallowed(t *testing.T) {
	store := &fakeStore{saveErr: errors.New("db down")}
	svc := newTestService(t, newFakeOpenAI(t, "x"), nil, store)
	// Must not panic nor surface the error
	svc.RecordMessage(context.Background(), mention("hola"))
}
