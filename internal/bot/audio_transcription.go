package bot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	defaultAudioTranscriptionModel     = "gpt-4o-mini-transcribe"
	defaultAudioMaxDownloadMB          = 25
	defaultAudioDownloadTimeoutSeconds = 20
	defaultAudioConvertTimeoutSeconds  = 30
	defaultAudioTranscribeTimeout      = 60
	maxFFmpegErrorLogBytes             = 1200
)

type audioTranscriptionOptions struct {
	Enabled              bool
	FFmpegPath           string
	TranscriptionModel   string
	MaxDownloadBytes     int64
	DownloadTimeout      time.Duration
	ConvertTimeout       time.Duration
	TranscriptionTimeout time.Duration
}

type audioTranscriber interface {
	TranscribeAudio(ctx context.Context, filePath string, model string) (string, error)
}

func loadAudioTranscriptionOptionsFromEnv() audioTranscriptionOptions {
	maxDownloadMB := parsePositiveIntEnvAny([]string{"AUDIO_MAX_DOWNLOAD_MB"}, defaultAudioMaxDownloadMB)
	downloadSeconds := parsePositiveIntEnvAny([]string{"AUDIO_DOWNLOAD_TIMEOUT_SECONDS"}, defaultAudioDownloadTimeoutSeconds)
	convertSeconds := parsePositiveIntEnvAny([]string{"AUDIO_CONVERT_TIMEOUT_SECONDS"}, defaultAudioConvertTimeoutSeconds)
	transcribeSeconds := parsePositiveIntEnvAny([]string{"AUDIO_TRANSCRIPTION_TIMEOUT_SECONDS"}, defaultAudioTranscribeTimeout)
	ffmpegPath := strings.TrimSpace(os.Getenv("FFMPEG_PATH"))
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_TRANSCRIPTION_MODEL"))
	if model == "" {
		model = defaultAudioTranscriptionModel
	}
	return audioTranscriptionOptions{
		Enabled:              parseBoolEnv("AUDIO_TRANSCRIPTION_ENABLED", false),
		FFmpegPath:           ffmpegPath,
		TranscriptionModel:   model,
		MaxDownloadBytes:     int64(maxDownloadMB) * 1024 * 1024,
		DownloadTimeout:      time.Duration(downloadSeconds) * time.Second,
		ConvertTimeout:       time.Duration(convertSeconds) * time.Second,
		TranscriptionTimeout: time.Duration(transcribeSeconds) * time.Second,
	}
}

func (s *Service) maybeTranscribeIncomingAudio(ctx context.Context, chatID string, msg IncomingMessage, language string) (string, bool, error) {
	if !s.audio.Enabled {
		return "", false, nil
	}
	transcriber, ok := s.ai.(audioTranscriber)
	if !ok || transcriber == nil {
		s.warn("audio transcription unavailable; transcriber is not configured",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
		)
		return "", true, s.sendAudioTranscriptionFallback(ctx, chatID, language)
	}
	downloadURL := strings.TrimSpace(msg.DownloadURL)
	if downloadURL == "" {
		s.warn("audio transcription skipped; download url is missing",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
			zap.String("file_name", filepath.Base(strings.TrimSpace(msg.FileName))),
			zap.String("mime_type", strings.TrimSpace(msg.MimeType)),
		)
		return "", true, s.sendAudioTranscriptionFallback(ctx, chatID, language)
	}

	tempDir, err := os.MkdirTemp("", "stone-audio-*")
	if err != nil {
		return "", true, fmt.Errorf("create audio temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	inputPath := filepath.Join(tempDir, "input"+audioFileExtension(msg.FileName, msg.MimeType))
	outputPath := filepath.Join(tempDir, "output.mp3")
	if err := s.downloadAudioFile(ctx, downloadURL, inputPath); err != nil {
		s.warn("audio download failed; asking customer for text",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
			zap.String("file_name", filepath.Base(strings.TrimSpace(msg.FileName))),
			zap.String("mime_type", strings.TrimSpace(msg.MimeType)),
			zap.String("error", safeAudioError(err)),
		)
		return "", true, s.sendAudioTranscriptionFallback(ctx, chatID, language)
	}
	if err := s.convertAudioToMP3(ctx, inputPath, outputPath); err != nil {
		s.warn("audio conversion failed; asking customer for text",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
			zap.String("file_name", filepath.Base(strings.TrimSpace(msg.FileName))),
			zap.String("mime_type", strings.TrimSpace(msg.MimeType)),
			zap.String("ffmpeg_path", strings.TrimSpace(s.audio.FFmpegPath)),
			zap.String("error", safeAudioError(err)),
		)
		return "", true, s.sendAudioTranscriptionFallback(ctx, chatID, language)
	}

	timeout := s.audio.TranscriptionTimeout
	if timeout <= 0 {
		timeout = defaultAudioTranscribeTimeout * time.Second
	}
	transcribeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transcript, err := transcriber.TranscribeAudio(transcribeCtx, outputPath, s.audio.TranscriptionModel)
	if err != nil {
		s.warn("audio transcription failed; asking customer for text",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
			zap.String("model", strings.TrimSpace(s.audio.TranscriptionModel)),
			zap.String("error", safeAudioError(err)),
		)
		return "", true, s.sendAudioTranscriptionFallback(ctx, chatID, language)
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		s.warn("audio transcription returned empty text; asking customer for text",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
			zap.String("model", strings.TrimSpace(s.audio.TranscriptionModel)),
		)
		return "", true, s.sendAudioTranscriptionFallback(ctx, chatID, language)
	}
	s.info("audio transcription succeeded",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("message_id", strings.TrimSpace(msg.IDMessage)),
		zap.String("source", "audio_transcript"),
		zap.Int("transcript_len", len([]rune(transcript))),
	)
	return transcript, true, nil
}

func (s *Service) downloadAudioFile(ctx context.Context, downloadURL string, outputPath string) error {
	parsed, err := url.Parse(downloadURL)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("invalid audio download url")
	}
	timeout := s.audio.DownloadTimeout
	if timeout <= 0 {
		timeout = defaultAudioDownloadTimeoutSeconds * time.Second
	}
	downloadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("create audio download request: %w", err)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download audio: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("audio download returned status %d", resp.StatusCode)
	}
	limit := s.audio.MaxDownloadBytes
	if limit <= 0 {
		limit = int64(defaultAudioMaxDownloadMB) * 1024 * 1024
	}
	reader := io.LimitReader(resp.Body, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("read downloaded audio: %w", err)
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("audio file exceeds max download size %s bytes", strconv.FormatInt(limit, 10))
	}
	if err := os.WriteFile(outputPath, data, 0o600); err != nil {
		return fmt.Errorf("write downloaded audio temp file: %w", err)
	}
	return nil
}

func (s *Service) convertAudioToMP3(ctx context.Context, inputPath string, outputPath string) error {
	ffmpegPath := strings.TrimSpace(s.audio.FFmpegPath)
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	timeout := s.audio.ConvertTimeout
	if timeout <= 0 {
		timeout = defaultAudioConvertTimeoutSeconds * time.Second
	}
	convertCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(convertCtx, ffmpegPath, "-y", "-i", inputPath, "-vn", "-ac", "1", "-ar", "16000", "-b:a", "64k", outputPath)
	output, err := cmd.CombinedOutput()
	if convertCtx.Err() != nil {
		return fmt.Errorf("ffmpeg timed out: %w", convertCtx.Err())
	}
	if err != nil {
		return fmt.Errorf("ffmpeg conversion failed: %w: %s", err, truncateAudioLog(string(output), maxFFmpegErrorLogBytes))
	}
	return nil
}

func (s *Service) sendAudioTranscriptionFallback(ctx context.Context, chatID string, language string) error {
	return s.sendAndRemember(ctx, chatID, AudioTranscriptionFallbackText(language), StageDiagnosis, 0)
}

func (s *Service) handleIncomingAudioFallback(ctx context.Context, chatID string, msg IncomingMessage, language string) error {
	placeholder := mediaIncomingText(msg.TypeMessage, "")
	if err := s.store.AppendMessage(ctx, chatID, "user", placeholder); err != nil {
		return err
	}
	if err := s.store.MarkIncoming(ctx, chatID, placeholder); err != nil {
		return err
	}
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if isConversationClosedForAutomation(conversation) {
		s.info("incoming audio saved without automation reply",
			automationSilenceFields(chatID, conversation, "protected_conversation_state_audio")...,
		)
		return nil
	}
	if firstContactWelcomePackageRequired(conversation) {
		return s.sendFirstContactWelcomePackage(ctx, chatID, language, conversation, CustomerAnalysis{Intent: IntentGreeting})
	}
	stage := mediaFallbackStage(conversation)
	reply := AudioTranscriptionFallbackText(language)
	if strings.TrimSpace(conversation.LastReplyText) == reply {
		return nil
	}
	return s.sendAndRemember(ctx, chatID, reply, stage, selectedLevelFromConversation(conversation), qualificationMissingFields(conversation.Lead)...)
}

func AudioTranscriptionFallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Кешіріңіз, қазір дауыстық хабарламаларды өңдей алмаймын. Өтінемін, мәтінмен жазыңыз, мен бірден көмектесемін."
	default:
		return "Извините, сейчас я не могу обработать голосовые сообщения. Пожалуйста, напишите сообщение текстом, и я сразу помогу."
	}
}

func audioFileExtension(fileName string, mimeType string) string {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(fileName)))
	switch ext {
	case ".oga", ".ogg", ".opus", ".mp3", ".m4a", ".wav":
		return ext
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.Contains(mimeType, "mpeg"), strings.Contains(mimeType, "mp3"):
		return ".mp3"
	case strings.Contains(mimeType, "mp4"), strings.Contains(mimeType, "m4a"):
		return ".m4a"
	case strings.Contains(mimeType, "wav"):
		return ".wav"
	default:
		return ".oga"
	}
}

func safeAudioError(err error) string {
	if err == nil {
		return ""
	}
	return truncateAudioLog(err.Error(), maxFFmpegErrorLogBytes)
}

func truncateAudioLog(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}
