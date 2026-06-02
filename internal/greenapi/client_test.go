package greenapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yereke99/stone/internal/whatsapp"
)

type countingTransport struct {
	calls int
	body  string
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	body := t.body
	if body == "" {
		body = `{"idMessage":"sent-id"}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestSendMessageBlocksCustomerGroupWithoutHTTPCall(t *testing.T) {
	transport := &countingTransport{}
	client := NewClient("https://api.example", "https://media.example", "123", "token", 1, &http.Client{Transport: transport})

	err := client.SendMessage(context.Background(), "120363123456789@g.us", "hello")

	if !errors.Is(err, ErrBlockedWhatsAppGroupChat) {
		t.Fatalf("SendMessage() error = %v, want ErrBlockedWhatsAppGroupChat", err)
	}
	if transport.calls != 0 {
		t.Fatalf("http calls = %d, want 0", transport.calls)
	}
}

func TestSendFileByUploadBlocksCustomerGroupWithoutHTTPCall(t *testing.T) {
	transport := &countingTransport{}
	client := NewClient("https://api.example", "https://media.example", "123", "token", 1, &http.Client{Transport: transport})
	filePath := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	_, err := client.SendFileByUpload(context.Background(), "120363123456789@g.us", filePath, "caption")

	if !errors.Is(err, ErrBlockedWhatsAppGroupChat) {
		t.Fatalf("SendFileByUpload() error = %v, want ErrBlockedWhatsAppGroupChat", err)
	}
	if transport.calls != 0 {
		t.Fatalf("http calls = %d, want 0", transport.calls)
	}
}

func TestSendMessageWithPurposeAllowsWhitelistedManagerGroup(t *testing.T) {
	transport := &countingTransport{}
	client := NewClient("https://api.example", "https://media.example", "123", "token", 1, &http.Client{Transport: transport})
	groupChatID := "120363123456789@g.us"

	err := client.SendMessageWithPurpose(context.Background(), groupChatID, "lead", whatsapp.PurposeManagerNotification, []string{groupChatID})

	if err != nil {
		t.Fatalf("SendMessageWithPurpose() error = %v", err)
	}
	if transport.calls != 1 {
		t.Fatalf("http calls = %d, want 1", transport.calls)
	}
}
