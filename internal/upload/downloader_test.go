package upload

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBoundedHTTPDownloader_RemoteStatusMapping(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{name: "not_found", status: http.StatusNotFound, want: ErrDownloadNotFound},
		{name: "server_error", status: http.StatusBadGateway, want: ErrDownloadRemoteServer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			downloader := NewBoundedHTTPDownloader(time.Second)
			_, err := downloader.Download(context.Background(), server.URL, 1024)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Download error: got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestBoundedHTTPDownloader_TooLargeReturns413Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "2048")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("too large"))
	}))
	defer server.Close()

	downloader := NewBoundedHTTPDownloader(time.Second)
	_, err := downloader.Download(context.Background(), server.URL, 1024)
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("Download error: got %v, want %v", err, ErrDownloadTooLarge)
	}
}

func TestBoundedHTTPDownloader_TimeoutReturns408Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	downloader := NewBoundedHTTPDownloader(time.Millisecond)
	_, err := downloader.Download(context.Background(), server.URL, 1024)
	if !errors.Is(err, ErrDownloadTimeout) {
		t.Fatalf("Download error: got %v, want %v", err, ErrDownloadTimeout)
	}
}
