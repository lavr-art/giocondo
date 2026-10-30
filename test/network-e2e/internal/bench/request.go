package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response exceeds size limit")
	}
	return data, nil
}

// HTTPRequest writes only successful response bodies; an empty body is represented by {}.
func HTTPRequest(ctx context.Context, client *http.Client, method, url, body, token string, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("create HTTP request: %w", err)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return errors.New("HTTP request requires an http or https URL")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := readBounded(response.Body, 32<<20)
	if err != nil {
		return fmt.Errorf("read HTTP response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if len(data) == 0 {
		data = []byte("{}")
	}
	_, err = out.Write(data)
	return err
}
