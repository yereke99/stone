package greenapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const maxErrorBodyBytes = 4096

type Client struct {
	apiURL                string
	mediaAPIURL           string
	idInstance            string
	apiToken              string
	receiveTimeoutSeconds int
	httpClient            *http.Client
}

func NewClient(apiURL, mediaAPIURL, idInstance, apiToken string, receiveTimeoutSeconds int, httpClient *http.Client) *Client {
	return &Client{
		apiURL:                strings.TrimRight(apiURL, "/"),
		mediaAPIURL:           strings.TrimRight(mediaAPIURL, "/"),
		idInstance:            strings.TrimSpace(idInstance),
		apiToken:              strings.TrimSpace(apiToken),
		receiveTimeoutSeconds: receiveTimeoutSeconds,
		httpClient:            httpClient,
	}
}

func (c *Client) ReceiveNotification(ctx context.Context) (*Notification, bool, error) {
	url := fmt.Sprintf(
		"%s/waInstance%s/receiveNotification/%s?receiveTimeout=%d",
		c.apiURL,
		c.idInstance,
		c.apiToken,
		c.receiveTimeoutSeconds,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, fmt.Errorf("create receiveNotification request: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, false, fmt.Errorf("call receiveNotification: %w", err)
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("read receiveNotification response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, false, c.statusError("receiveNotification", resp.StatusCode, data)
	}

	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return nil, false, nil
	}

	var notification Notification
	if err := json.Unmarshal(data, &notification); err != nil {
		return nil, false, fmt.Errorf("decode receiveNotification response: %w", err)
	}

	return &notification, true, nil
}

func (c *Client) DeleteNotification(ctx context.Context, receiptID int) error {
	url := fmt.Sprintf(
		"%s/waInstance%s/deleteNotification/%s/%d",
		c.apiURL,
		c.idInstance,
		c.apiToken,
		receiptID,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create deleteNotification request: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("call deleteNotification: %w", err)
	}
	defer closeBody(resp.Body)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return c.statusError("deleteNotification", resp.StatusCode, data)
	}

	return nil
}

func (c *Client) SendMessage(ctx context.Context, chatID string, message string) error {
	payload := map[string]string{
		"chatId":  strings.TrimSpace(chatID),
		"message": strings.TrimSpace(message),
	}

	return c.postJSON(ctx, c.apiURL, "sendMessage", payload)
}

func (c *Client) SendFileByUpload(ctx context.Context, chatID string, filePath string, caption string) (string, error) {
	filePath = filepath.Clean(filePath)
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file for upload: %w", err)
	}
	defer closeFile(file)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	fields := map[string]string{
		"chatId":  strings.TrimSpace(chatID),
		"caption": strings.TrimSpace(caption),
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return "", fmt.Errorf("write multipart field %s: %w", key, err)
		}
	}

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("create multipart file field: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy multipart file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	url := fmt.Sprintf("%s/waInstance%s/sendFileByUpload/%s", c.mediaAPIURL, c.idInstance, c.apiToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", fmt.Errorf("create sendFileByUpload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("call sendFileByUpload: %w", err)
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read sendFileByUpload response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if len(data) > maxErrorBodyBytes {
			data = data[:maxErrorBodyBytes]
		}
		return "", c.statusError("sendFileByUpload", resp.StatusCode, data)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return "", nil
	}
	var result struct {
		IDMessage string `json:"idMessage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("decode sendFileByUpload response: %w", err)
	}

	return strings.TrimSpace(result.IDMessage), nil
}

func (c *Client) UploadFile(ctx context.Context, filePath string) (string, error) {
	filePath = filepath.Clean(filePath)
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file for upload: %w", err)
	}
	defer closeFile(file)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("create multipart file field: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy multipart file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	url := fmt.Sprintf("%s/waInstance%s/uploadFile/%s", c.mediaAPIURL, c.idInstance, c.apiToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", fmt.Errorf("create uploadFile request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("call uploadFile: %w", err)
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read uploadFile response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", c.statusError("uploadFile", resp.StatusCode, data)
	}

	var result struct {
		URLFile string `json:"urlFile"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("decode uploadFile response: %w", err)
	}
	if strings.TrimSpace(result.URLFile) == "" {
		return "", fmt.Errorf("uploadFile response does not contain urlFile")
	}

	return result.URLFile, nil
}

func (c *Client) SendFileByURL(ctx context.Context, chatID string, urlFile string, fileName string, caption string) error {
	payload := map[string]string{
		"chatId":   strings.TrimSpace(chatID),
		"urlFile":  strings.TrimSpace(urlFile),
		"fileName": strings.TrimSpace(fileName),
		"caption":  strings.TrimSpace(caption),
	}

	return c.postJSON(ctx, c.apiURL, "sendFileByUrl", payload)
}

func (c *Client) postJSON(ctx context.Context, baseURL string, method string, payload any) error {
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", method, err)
	}

	url := fmt.Sprintf("%s/waInstance%s/%s/%s", baseURL, c.idInstance, method, c.apiToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("create %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", method, err)
	}
	defer closeBody(resp.Body)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return c.statusError(method, resp.StatusCode, data)
	}

	return nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, sanitizeHTTPError(err)
	}
	return resp, nil
}

func sanitizeHTTPError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s request failed: %w", urlErr.Op, urlErr.Err)
	}
	return err
}

func (c *Client) statusError(method string, statusCode int, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	if bodyText == "" {
		return fmt.Errorf("greenapi %s returned status %d", method, statusCode)
	}

	bodyText = strings.ReplaceAll(bodyText, c.apiToken, "[redacted]")
	if len(bodyText) > maxErrorBodyBytes {
		bodyText = bodyText[:maxErrorBodyBytes]
	}
	return fmt.Errorf("greenapi %s returned status %d: %s", method, statusCode, bodyText)
}

func closeBody(body io.Closer) {
	_ = body.Close()
}

func closeFile(file *os.File) {
	_ = file.Close()
}
