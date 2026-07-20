package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeAudioAI struct {
	fakeAI
	transcript string
	err        error
	called     bool
	model      string
}

func (ai *fakeAudioAI) TranscribeAudio(ctx context.Context, filePath string, model string) (string, error) {
	ai.called = true
	ai.model = model
	if ai.err != nil {
		return "", ai.err
	}
	return ai.transcript, nil
}

func TestAudioMessageAsksForTextWithoutTranscription(t *testing.T) {
	audioURL := testAudioDownloadURL(t, []byte("fake oga"))
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &fakeAudioAI{transcript: "барбершопта жұмыс істейміз"}
	service := newAudioTestService(t, sender, store, ai)
	chatID := "chat-audio-barbershop"

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		ChatID:      chatID,
		TypeMessage: "audioMessage",
		DownloadURL: audioURL,
		FileName:    "voice.oga",
		MimeType:    "audio/ogg; codecs=opus",
		Timestamp:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if ai.called || ai.analysisCalled {
		t.Fatalf("audio reached AI: transcriber=%v analysis=%v", ai.called, ai.analysisCalled)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" || conversation.Lead.Goal != "" {
		t.Fatalf("audio fallback polluted lead: %#v", conversation.Lead)
	}
	if len(conversation.Messages) == 0 || conversation.Messages[0].Content != "[audioMessage]" {
		t.Fatalf("audio placeholder was not saved: %#v", conversation.Messages)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want one fallback", sender.messages)
	}
	if sender.messages[0] != AudioTranscriptionFallbackText("ru") {
		t.Fatalf("audio fallback = %q", sender.messages[0])
	}
}

func TestAudioTranscriptionFailureAsksForTextWithoutStateOverwrite(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newAudioTestService(t, sender, store, &fakeAudioAI{})
	chatID := "chat-audio-failure"

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		ChatID:      chatID,
		TypeMessage: "audioMessage",
		FileName:    "voice.oga",
		MimeType:    "audio/ogg; codecs=opus",
		Timestamp:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" || conversation.Lead.Goal != "" || conversation.LastIncomingText != "[audioMessage]" {
		t.Fatalf("failed audio transcription polluted state: %#v", conversation.Lead)
	}
	if len(sender.messages) != 1 || sender.messages[0] != AudioTranscriptionFallbackText("ru") {
		t.Fatalf("fallback message = %#v", sender.messages)
	}
}

func TestAudioDoesNotUseTranscriptForStopCommand(t *testing.T) {
	audioURL := testAudioDownloadURL(t, []byte("fake oga"))
	sender := &fakeSender{}
	store, err := NewSQLiteConversationStore(context.Background(), filepath.Join(t.TempDir(), "stone.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	service := newAudioTestService(t, sender, store, &fakeAudioAI{transcript: "стоп бот"})
	chatID := "77045550000@c.us"

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		ChatID:      chatID,
		TypeMessage: "audioMessage",
		DownloadURL: audioURL,
		FileName:    "voice.oga",
		MimeType:    "audio/ogg; codecs=opus",
		Timestamp:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	if len(sender.messages) != 1 || sender.messages[0] != AudioTranscriptionFallbackText("ru") || len(sender.files) != 0 {
		t.Fatalf("audio fallback mismatch: messages=%#v files=%#v", sender.messages, sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage == ClientStateOptOut || conversation.Stopped || conversation.AutomationClosed || conversation.OptOut {
		t.Fatalf("audio transcript unexpectedly stopped automation: stage=%q stopped=%v closed=%v optout=%v", conversation.Stage, conversation.Stopped, conversation.AutomationClosed, conversation.OptOut)
	}
	if store.IsSuppressedPhoneOrChatID(chatID, NormalizePhone(chatID)) {
		t.Fatal("audio transcript should not persist automation suppression")
	}
}

func newAudioTestService(t *testing.T, sender *fakeSender, store *ConversationStore, ai *fakeAudioAI) *Service {
	t.Helper()
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	service.audio = audioTranscriptionOptions{
		Enabled:              true,
		FFmpegPath:           fakeFFmpegPath(t),
		TranscriptionModel:   "test-transcribe",
		MaxDownloadBytes:     1024 * 1024,
		DownloadTimeout:      5 * time.Second,
		ConvertTimeout:       5 * time.Second,
		TranscriptionTimeout: 5 * time.Second,
	}
	return service
}

func testAudioDownloadURL(t *testing.T, body []byte) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func fakeFFmpegPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nout=\"\"\nfor arg do out=\"$arg\"; done\ncp \"$3\" \"$out\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}
