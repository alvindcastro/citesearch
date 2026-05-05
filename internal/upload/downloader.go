package upload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

const DefaultDownloadTimeout = 60 * time.Second

type BoundedHTTPDownloader struct {
	client *http.Client
}

func NewBoundedHTTPDownloader(timeout time.Duration) *BoundedHTTPDownloader {
	if timeout <= 0 {
		timeout = DefaultDownloadTimeout
	}
	return &BoundedHTTPDownloader{
		client: &http.Client{Timeout: timeout},
	}
}

func (d *BoundedHTTPDownloader) Download(ctx context.Context, rawURL string, maxBytes int64) (DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return DownloadResult{}, err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(ctx.Err(), context.DeadlineExceeded) ||
			(errors.As(err, &netErr) && netErr.Timeout()) {
			return DownloadResult{}, ErrDownloadTimeout
		}
		return DownloadResult{}, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return DownloadResult{}, ErrDownloadNotFound
	case resp.StatusCode >= 500:
		return DownloadResult{}, ErrDownloadRemoteServer
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return DownloadResult{}, fmt.Errorf("remote URL returned status %d", resp.StatusCode)
	}

	if maxBytes > 0 && resp.ContentLength > maxBytes {
		return DownloadResult{}, ErrDownloadTooLarge
	}

	var body io.Reader = resp.Body
	if maxBytes > 0 {
		body = io.LimitReader(resp.Body, maxBytes+1)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return DownloadResult{}, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return DownloadResult{}, ErrDownloadTooLarge
	}

	filename := filepath.Base(req.URL.Path)
	return DownloadResult{
		Filename:    filename,
		ContentType: resp.Header.Get("Content-Type"),
		SizeBytes:   int64(len(data)),
		Body:        io.NopCloser(bytes.NewReader(data)),
	}, nil
}
