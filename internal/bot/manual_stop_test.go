package bot

import (
	"context"
	"testing"
	"time"
)

func TestIsAdminStopCommand(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "russian lower", text: "стоп", want: true},
		{name: "russian upper", text: "СТОП", want: true},
		{name: "russian mixed case", text: "Стоп", want: true},
		{name: "russian punctuation", text: "стоп.", want: true},
		{name: "russian bang", text: "стоп!", want: true},
		{name: "russian spaces", text: "  стоп  ", want: true},
		{name: "russian slash", text: "/стоп", want: true},
		{name: "russian stop bot", text: "Стоп, бот!", want: true},
		{name: "russian stop bot spaces", text: "стоп   бот", want: true},
		{name: "disable", text: "отключить", want: true},
		{name: "do not write", text: "не писать", want: true},
		{name: "enough", text: "хватит", want: true},
		{name: "stop verb", text: "остановить", want: true},
		{name: "english lower", text: "stop", want: true},
		{name: "english upper", text: "STOP", want: true},
		{name: "english punctuation", text: "stop.", want: true},
		{name: "english bang", text: "stop!", want: true},
		{name: "english capital bang", text: "Stop!", want: true},
		{name: "english spaces", text: " stop ", want: true},
		{name: "english slash", text: "/stop", want: true},
		{name: "english stop bot", text: "stop bot", want: true},

		// Mixed Cyrillic/Latin look-alikes typed on phone keyboards.
		// Latin c + Cyrillic т о п.
		{name: "cyrillic word with latin c and o", text: "cтoп", want: true},
		// Cyrillic ѕ т о р spelling "stop".
		{name: "latin word with cyrillic look-alikes", text: "ѕтор", want: true},

		// Must NOT trigger.
		{name: "empty", text: "", want: false},
		{name: "spaces only", text: "   ", want: false},
		{name: "punctuation only", text: "...", want: false},
		{name: "stop motion video", text: "stop motion video", want: false},
		{name: "russian sentence", text: "останови ролик", want: false},
		{name: "acknowledgement", text: "Хорошо", want: false},
		{name: "ok", text: "ок", want: false},
		{name: "repeated", text: "стоп стоп", want: false},
		{name: "top", text: "топ", want: false},
		{name: "stopped sentence", text: "stopped working", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAdminStopCommand(tt.text); got != tt.want {
				t.Fatalf("IsAdminStopCommand(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestNormalizeAdminStopCommandStaysReadable(t *testing.T) {
	if got := NormalizeAdminStopCommand("  СТОП!  "); got != "стоп" {
		t.Fatalf("NormalizeAdminStopCommand() = %q, want %q (readable, not folded)", got, "стоп")
	}
	if got := NormalizeAdminStopCommand("/StoP"); got != "stop" {
		t.Fatalf("NormalizeAdminStopCommand() = %q, want %q", got, "stop")
	}
	if got := NormalizeAdminStopCommand("Стоп, бот!"); got != "стоп бот" {
		t.Fatalf("NormalizeAdminStopCommand() = %q, want %q", got, "стоп бот")
	}
}

func TestIsAutomationAllowedReflectsManualStop(t *testing.T) {
	store := NewConversationStore()
	chatID := "77041110000@c.us"

	// Unknown chat: first-contact automation is allowed.
	allowed, err := store.IsAutomationAllowed(context.Background(), chatID)
	if err != nil {
		t.Fatalf("IsAutomationAllowed() error = %v", err)
	}
	if !allowed {
		t.Fatal("expected automation allowed for unknown chat")
	}

	// Active conversation: still allowed.
	if err := store.UpdateState(context.Background(), chatID, ClientStateAwaitingQualification, 0); err != nil {
		t.Fatalf("UpdateState() error = %v", err)
	}
	allowed, err = store.IsAutomationAllowed(context.Background(), chatID)
	if err != nil {
		t.Fatalf("IsAutomationAllowed() error = %v", err)
	}
	if !allowed {
		t.Fatal("expected automation allowed for active conversation")
	}

	// After a manual admin stop: blocked.
	if err := store.MarkManualStop(context.Background(), chatID, "stop-id", time.Now().UTC(), StoppedByManualAdmin); err != nil {
		t.Fatalf("MarkManualStop() error = %v", err)
	}
	allowed, err = store.IsAutomationAllowed(context.Background(), chatID)
	if err != nil {
		t.Fatalf("IsAutomationAllowed() error = %v", err)
	}
	if allowed {
		t.Fatal("expected automation blocked after manual stop")
	}
}

func TestIsAutomationAllowedBlocksHandoffAndOptOut(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*Conversation)
	}{
		{name: "handed_off", apply: func(c *Conversation) {
			c.HandedOffToOwner = true
			c.Stage = ClientStateHandedOff
		}},
		{name: "stopped", apply: func(c *Conversation) {
			c.Stopped = true
			c.Stage = ClientStateStopped
		}},
		{name: "opt_out", apply: func(c *Conversation) {
			c.OptOut = true
			c.Stopped = true
			c.Stage = ClientStateOptOut
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewConversationStore()
			chatID := "77042220000@c.us"
			store.Update(chatID, tt.apply)
			allowed, err := store.IsAutomationAllowed(context.Background(), chatID)
			if err != nil {
				t.Fatalf("IsAutomationAllowed() error = %v", err)
			}
			if allowed {
				t.Fatalf("expected automation blocked for state %q", tt.name)
			}
		})
	}
}
