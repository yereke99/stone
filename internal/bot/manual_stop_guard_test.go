package bot

import (
	"context"
	"testing"
	"time"
)

func TestCustomerOutboundGuardSkipsManualStoppedConversation(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := NewService(sender, &fakeAI{}, store, "./video", PortfolioLinks{}, "ru", nil)
	chatID := "77047770000@c.us"

	if err := store.MarkManualStop(context.Background(), chatID, "manual-stop-id", time.Now().UTC(), StoppedByManualAdmin); err != nil {
		t.Fatalf("MarkManualStop() error = %v", err)
	}
	if err := service.sendCustomerWhatsAppMessage(context.Background(), chatID, "Автоматический ответ"); err != nil {
		t.Fatalf("sendCustomerWhatsAppMessage() error = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("stopped conversation got outgoing message: %#v", sender.messages)
	}
}
