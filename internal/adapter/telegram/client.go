package telegram

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
	"path"
	"strconv"
	"strings"
	"time"
)

const maxMediaBytes = 8 << 20

var errMediaRejected = errors.New("Telegram image is unsupported or exceeds 8 MiB")

// APIError deliberately excludes Telegram descriptions and request URLs: both
// transport errors and upstream responses can contain the credential in a URL.
type APIError struct {
	Method     string
	Code       int
	RetryAfter int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Telegram %s failed (code %d)", e.Method, e.Code)
}

type client struct {
	token, baseURL string
	http           *http.Client
}

func newClient(cfg Config) *client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.ProxyURL != "" {
		proxy, _ := url.Parse(cfg.ProxyURL)
		transport.Proxy = http.ProxyURL(proxy)
	}
	return &client{token: cfg.Token, baseURL: "https://api.telegram.org", http: &http.Client{
		Transport: transport, Timeout: 40 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// call retries only explicit rate-limit rejections. Retrying an ambiguous
// timeout/5xx after a send can duplicate a message that Telegram already sent.
func (c *client) call(ctx context.Context, method string, params map[string]any, upload []byte, filename string, out any) error {
	for attempts := 0; ; attempts++ {
		err := c.request(ctx, method, params, upload, filename, out)
		var apiErr *APIError
		if attempts >= 3 || !errors.As(err, &apiErr) || apiErr.Code != 429 || apiErr.RetryAfter <= 0 {
			return err
		}
		if !wait(ctx, time.Duration(apiErr.RetryAfter)*time.Second) {
			return ctx.Err()
		}
	}
}

func (c *client) request(ctx context.Context, method string, params map[string]any, upload []byte, filename string, out any) error {
	var body bytes.Buffer
	contentType := "application/json"
	if upload != nil {
		writer := multipart.NewWriter(&body)
		for key, value := range params {
			var encoded string
			if text, ok := value.(string); ok {
				encoded = text
			} else {
				raw, err := json.Marshal(value)
				if err != nil {
					return errors.New("Telegram request encoding failed")
				}
				encoded = string(raw)
			}
			if err := writer.WriteField(key, encoded); err != nil {
				return errors.New("Telegram upload encoding failed")
			}
		}
		field := map[string]string{"sendPhoto": "photo", "sendDocument": "document", "sendAudio": "audio", "sendVideo": "video", "sendVoice": "voice"}[method]
		part, err := writer.CreateFormFile(field, filename)
		if err != nil {
			return errors.New("Telegram upload encoding failed")
		}
		if _, err = part.Write(upload); err != nil {
			return errors.New("Telegram upload encoding failed")
		}
		if err = writer.Close(); err != nil {
			return errors.New("Telegram upload encoding failed")
		}
		contentType = writer.FormDataContentType()
	} else if err := json.NewEncoder(&body).Encode(params); err != nil {
		return errors.New("Telegram request encoding failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, &body)
	if err != nil {
		return errors.New("Telegram request creation failed")
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("Telegram %s transport failed", method)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("Telegram %s response read failed", method)
	}
	var envelope struct {
		OK         bool            `json:"ok"`
		Result     json.RawMessage `json:"result"`
		Code       int             `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return fmt.Errorf("Telegram %s returned an invalid response (HTTP %d)", method, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.OK {
		code := envelope.Code
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{Method: method, Code: code, RetryAfter: envelope.Parameters.RetryAfter}
	}
	if out != nil && json.Unmarshal(envelope.Result, out) != nil {
		return fmt.Errorf("Telegram %s returned an invalid result", method)
	}
	return nil
}

func (c *client) downloadPhoto(ctx context.Context, fileID string) ([]byte, error) {
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, nil, "", &file); err != nil {
		return nil, err
	}
	if file.Size > maxMediaBytes || file.Path == "" || strings.HasPrefix(file.Path, "/") || strings.ContainsAny(file.Path, "?#\\") || path.Clean(file.Path) != file.Path || strings.HasPrefix(file.Path, "..") {
		return nil, errMediaRejected
	}
	segments := strings.Split(file.Path, "/")
	for i := range segments {
		segments[i] = url.PathEscape(segments[i])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/file/bot"+c.token+"/"+strings.Join(segments, "/"), nil)
	if err != nil {
		return nil, errors.New("Telegram image request creation failed")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("Telegram image download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Telegram image download failed (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Telegram image response read failed")
	}
	if len(data) > maxMediaBytes || !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, errMediaRejected
	}
	return data, nil
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("Telegram target must be a nonzero numeric chat ID")
	}
	return id, nil
}
