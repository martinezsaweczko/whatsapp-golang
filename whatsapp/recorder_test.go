package whatsapp

import (
	"context"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// fakeRecorder records every message it receives; it panics when told to.
type fakeRecorder struct {
	mu          sync.Mutex
	msgs        []IncomingMessage
	hadDeadline bool
	panics      bool
}

func (f *fakeRecorder) RecordMessage(ctx context.Context, msg IncomingMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, f.hadDeadline = ctx.Deadline()
	f.msgs = append(f.msgs, msg)
	if f.panics {
		panic("recorder exploded")
	}
}

func (f *fakeRecorder) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.msgs))
	for _, m := range f.msgs {
		out = append(out, m.Body)
	}
	return out
}

func groupEvent(body string) *events.Message {
	evt := messageEvent(body)
	evt.Info.IsGroup = true
	return evt
}

func TestRouterRecordsGroupTextMessages(t *testing.T) {
	privateEvt := messageEvent("hola en privado")
	privateEvt.Info.Chat = types.NewJID("34645568517", types.DefaultUserServer)
	ownEvt := groupEvent("mensaje propio")
	ownEvt.Info.IsFromMe = true
	emptyEvt := groupEvent("")
	emptyEvt.Message = &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("a.pdf")}}

	tests := []struct {
		name   string
		evt    *events.Message
		record bool
	}{
		{"plain group text", groupEvent("hola grupo"), true},
		{"group text matching a command", groupEvent("nacional"), true},
		{"own message", ownEvt, false},
		{"private chat", privateEvt, false},
		{"empty body", emptyEvt, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestRouter(t, nil)
			rec := &fakeRecorder{}
			router.SetRecorder(rec)
			router.Register("cmd", regexp.MustCompile(`^nacional`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
				return nil
			})

			router.dispatch(tt.evt)
			router.Wait()

			got := rec.bodies()
			if tt.record && (len(got) != 1 || got[0] != tt.evt.Message.GetConversation()) {
				t.Fatalf("recorded %v, want exactly the message body", got)
			}
			if !tt.record && len(got) != 0 {
				t.Fatalf("recorded %v, want nothing", got)
			}
			if tt.record && !rec.hadDeadline {
				t.Error("recorder context must be bounded by a deadline")
			}
		})
	}
}

func TestRouterRecordsBeforeCommandRuns(t *testing.T) {
	router := newTestRouter(t, nil)
	rec := &fakeRecorder{}
	router.SetRecorder(rec)

	var recordedFirst atomic.Bool
	router.Register("cmd", regexp.MustCompile(`^nacional`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		recordedFirst.Store(len(rec.bodies()) == 1)
		return nil
	})

	router.dispatch(groupEvent("nacional"))
	router.Wait()
	if !recordedFirst.Load() {
		t.Error("message must be recorded before the command handler runs")
	}
}

func TestRouterRecorderPanicDoesNotBreakDispatch(t *testing.T) {
	router := newTestRouter(t, nil)
	router.SetRecorder(&fakeRecorder{panics: true})

	var calls atomic.Int32
	router.Register("cmd", regexp.MustCompile(`^nacional`), func(_ context.Context, _ Sender, _ IncomingMessage) error {
		calls.Add(1)
		return nil
	})

	router.dispatch(groupEvent("nacional"))
	waitFor(t, func() bool { return calls.Load() == 1 })
}

func TestNewIncomingMessageTimestamp(t *testing.T) {
	at := time.Date(2026, 11, 19, 10, 30, 0, 0, time.UTC)
	evt := groupEvent("hola")
	evt.Info.Timestamp = at
	if got := NewIncomingMessage(evt).Timestamp; !got.Equal(at) {
		t.Errorf("Timestamp = %v, want %v", got, at)
	}

	before := time.Now()
	synthetic := NewSyntheticMessage(types.NewJID("1", types.GroupServer), types.NewJID("2", types.DefaultUserServer), "x")
	if synthetic.Timestamp.Before(before) || synthetic.Timestamp.After(time.Now()) {
		t.Errorf("synthetic Timestamp = %v, want now", synthetic.Timestamp)
	}
}
