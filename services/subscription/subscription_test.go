package subscription

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/martinezsaweczko/whatsappBot-golang/whatsapp"
	"go.mau.fi/whatsmeow/types"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type fakeStore struct {
	saved   [][2]string
	deleted []string
	subs    []model.Subscription
	matches []string
	err     error
}

func (f *fakeStore) SaveSubscription(_ context.Context, text, user string) error {
	f.saved = append(f.saved, [2]string{text, user})
	return f.err
}

func (f *fakeStore) DeleteSubscription(_ context.Context, user string) error {
	f.deleted = append(f.deleted, user)
	return f.err
}

func (f *fakeStore) ReturnSubscriptions(_ context.Context, _ string) ([]model.Subscription, error) {
	return f.subs, f.err
}

func (f *fakeStore) MatchSubscriptions(_ context.Context, _ string) ([]string, error) {
	return f.matches, f.err
}

type fakeLinkBuilder struct {
	expiry time.Duration
}

func (f *fakeLinkBuilder) GenerateSubscriptionToken(_ string, expiry time.Duration) (string, error) {
	f.expiry = expiry
	return "sub-token", nil
}

type fakeSender struct {
	sentTexts []struct {
		to   types.JID
		text string
	}
}

func (f *fakeSender) ReplyText(_ context.Context, _ whatsapp.IncomingMessage, _ string) error {
	return nil
}

func (f *fakeSender) SendText(_ context.Context, to types.JID, text string) error {
	f.sentTexts = append(f.sentTexts, struct {
		to   types.JID
		text string
	}{to, text})
	return nil
}

func (f *fakeSender) ReplyMedia(_ context.Context, _ whatsapp.IncomingMessage, _ []byte, _, _ string) error {
	return nil
}

func (f *fakeSender) SendMedia(_ context.Context, _ types.JID, _ []byte, _, _ string) error {
	return nil
}

func (f *fakeSender) DeleteMessage(_ context.Context, _ whatsapp.IncomingMessage) error {
	return nil
}

func newTestService(store *fakeStore) (*Service, *fakeLinkBuilder) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	jwt := &fakeLinkBuilder{}
	svc, err := New(store, jwt, "files.example.com:443",
		map[string]string{"nacional": "nacional_folder"},
		log, tracenoop.NewTracerProvider(), noop.NewMeterProvider())
	if err != nil {
		panic(err)
	}
	return svc, jwt
}

func testMessage() whatsapp.IncomingMessage {
	return whatsapp.IncomingMessage{
		Sender:   types.NewJID("34645568517", types.DefaultUserServer),
		PushName: "David",
	}
}

func TestSubscribe(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newTestService(store)

	if err := svc.Subscribe(context.Background(), testMessage(), "  El Mundo  "); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	if len(store.saved) != 1 || store.saved[0] != [2]string{"El Mundo", testMessage().Sender.String()} {
		t.Errorf("unexpected saved subscription: %+v", store.saved)
	}
}

func TestSubscribeEmptyRejected(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newTestService(store)
	if err := svc.Subscribe(context.Background(), testMessage(), "   "); err == nil {
		t.Fatal("expected error for empty subscription")
	}
}

func TestDelete(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newTestService(store)

	if err := svc.Delete(context.Background(), testMessage()); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != testMessage().Sender.String() {
		t.Errorf("unexpected deleted user: %+v", store.deleted)
	}
}

func TestList(t *testing.T) {
	store := &fakeStore{subs: []model.Subscription{
		{ID: 1, SubscriptionText: "Mundo", User: "u"},
		{ID: 2, SubscriptionText: "Pais", User: "u"},
	}}
	svc, _ := newTestService(store)

	list, err := svc.List(context.Background(), testMessage())
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if !strings.Contains(list, "*Mundo*") || !strings.Contains(list, "*Pais*") {
		t.Errorf("list missing subscriptions: %s", list)
	}

	store.subs = nil
	list, _ = svc.List(context.Background(), testMessage())
	if list != "No tienes subscripciones activas" {
		t.Errorf("unexpected empty list message: %s", list)
	}
}

func TestNotify(t *testing.T) {
	store := &fakeStore{matches: []string{testMessage().Sender.String(), "34999999999@c.us"}}
	svc, jwt := newTestService(store)
	sender := &fakeSender{}

	err := svc.Notify(context.Background(), sender, "nacional", "ElMundo_hoy.pdf")
	if err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	if len(sender.sentTexts) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(sender.sentTexts))
	}

	msg := sender.sentTexts[0]
	if msg.to.String() != testMessage().Sender.String() {
		t.Errorf("unexpected recipient: %s", msg.to)
	}
	if !strings.Contains(msg.text, "Documento:*ElMundo_hoy.pdf*") {
		t.Errorf("missing document name: %s", msg.text)
	}
	if !strings.Contains(msg.text, "https://files.example.com:443/nacional_folder/ElMundo_hoy.pdf?access_token=sub-token") {
		t.Errorf("missing link: %s", msg.text)
	}
	if jwt.expiry != subscriptionTokenTTL {
		t.Errorf("expected 48h token, got %v", jwt.expiry)
	}
}

func TestNotifyUnknownCategory(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newTestService(store)
	if err := svc.Notify(context.Background(), &fakeSender{}, "unknown", "f.pdf"); err == nil {
		t.Fatal("expected error for unknown category")
	}
}

func TestNotifySkipsInvalidJIDs(t *testing.T) {
	store := &fakeStore{matches: []string{"not-a-jid", testMessage().Sender.String()}}
	svc, _ := newTestService(store)
	sender := &fakeSender{}

	err := svc.Notify(context.Background(), sender, "nacional", "file.pdf")
	if err == nil {
		t.Fatal("expected partial failure error")
	}
	if len(sender.sentTexts) != 1 {
		t.Fatalf("expected 1 successful notification, got %d", len(sender.sentTexts))
	}
}
