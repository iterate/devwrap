package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
)

func adminURL(path string) string {
	if strings.HasPrefix(path, "/") {
		return caddyAdminBase + path
	}
	return caddyAdminBase + "/" + path
}

func adminHealthy() bool {
	res, err := apiClient().Get(adminURL("/config/"))
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode < 500
}

func waitForAdminReady(maxWait time.Duration) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	bo.MaxInterval = time.Second

	ctx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		if adminHealthy() {
			return struct{}{}, nil
		}
		return struct{}{}, errors.New("caddy admin not ready")
	}, backoff.WithBackOff(bo), backoff.WithMaxElapsedTime(maxWait))
	if err != nil {
		return errors.New("caddy admin did not become ready")
	}
	return nil
}

func adminGet(path string) (*http.Response, error) {
	return apiClient().Get(adminURL(path))
}

func adminDoJSON(method, path string, payload any) (*http.Response, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, adminURL(path), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return apiClient().Do(req)
}

func adminDo(method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, adminURL(path), nil)
	if err != nil {
		return nil, err
	}
	return apiClient().Do(req)
}

func adminReadBody(res *http.Response) string {
	b, _ := io.ReadAll(res.Body)
	return strings.TrimSpace(string(b))
}
