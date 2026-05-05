package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"citesearch/config"
	"citesearch/internal/upload"

	"github.com/gin-gonic/gin"
)

func TestBannerUploadFromURL_Remote404Returns404(t *testing.T) {
	router, store, _, runner, _ := newBannerUploadFromURLTestRouter(t, 100, "customercare.ellucian.com", fakeUploadHTTPDownloader{
		err: upload.ErrDownloadNotFound,
	})

	w := performURLUpload(router, validURLUploadPayload(t))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUploadFromURL_Remote5xxReturns502(t *testing.T) {
	router, store, _, runner, _ := newBannerUploadFromURLTestRouter(t, 100, "customercare.ellucian.com", fakeUploadHTTPDownloader{
		err: upload.ErrDownloadRemoteServer,
	})

	w := performURLUpload(router, validURLUploadPayload(t))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadGateway, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUploadFromURL_TimeoutReturns408(t *testing.T) {
	router, store, _, runner, _ := newBannerUploadFromURLTestRouter(t, 100, "customercare.ellucian.com", fakeUploadHTTPDownloader{
		err: upload.ErrDownloadTimeout,
	})

	w := performURLUpload(router, validURLUploadPayload(t))

	if w.Code != http.StatusRequestTimeout {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusRequestTimeout, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUploadFromURL_TooLargeReturns413(t *testing.T) {
	router, store, _, runner, _ := newBannerUploadFromURLTestRouter(t, 100, "customercare.ellucian.com", fakeUploadHTTPDownloader{
		err: upload.ErrDownloadTooLarge,
	})

	w := performURLUpload(router, validURLUploadPayload(t))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusRequestEntityTooLarge, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUploadFromURL_NonPDFDownloadReturns400(t *testing.T) {
	router, store, _, runner, _ := newBannerUploadFromURLTestRouter(t, 100, "customercare.ellucian.com", fakeUploadHTTPDownloader{
		result: upload.DownloadResult{
			Filename:    "Banner_Finance_9.3.22.docx",
			ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			SizeBytes:   4,
			Body:        io.NopCloser(strings.NewReader("docx")),
		},
	})

	w := performURLUpload(router, validURLUploadPayload(t))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUploadFromURL_SOPSourceTypeReturns400(t *testing.T) {
	downloader := &countingURLDownloader{}
	router, store, _, runner := newBannerUploadFromURLRouterWithDownloader(t, 100, "customercare.ellucian.com", downloader)
	payload := validURLUploadPayload(t)
	payload["source_type"] = "sop"

	w := performURLUpload(router, payload)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if downloader.calls != 0 {
		t.Fatalf("downloader calls: got %d, want 0", downloader.calls)
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUploadFromURL_CreatesBlobAndSidecar(t *testing.T) {
	pdf := "%PDF-1.4\nfixture"
	router, store, counter, runner, _ := newBannerUploadFromURLTestRouter(t, 100, "customercare.ellucian.com", fakeUploadHTTPDownloader{
		result: upload.DownloadResult{
			Filename:    "Banner_Finance_9.3.22.pdf",
			ContentType: "application/pdf",
			SizeBytes:   int64(len(pdf)),
			Body:        io.NopCloser(strings.NewReader(pdf)),
		},
	})
	counter.pages = 42

	w := performURLUpload(router, validURLUploadPayload(t))

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	if got := string(store.blobs[blobPath]); got != pdf {
		t.Fatalf("blob content: got %q, want %q", got, pdf)
	}

	var sidecar upload.SidecarState
	if err := json.Unmarshal(store.blobs[upload.SidecarPath(blobPath)], &sidecar); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	if sidecar.UploadID != "upload-123" || sidecar.TotalPages != 42 {
		t.Fatalf("sidecar metadata: %#v", sidecar)
	}
	if sidecar.Status != upload.StatusPending || sidecar.ChunkingPattern != upload.PatternNone {
		t.Fatalf("sidecar state: status=%q pattern=%q", sidecar.Status, sidecar.ChunkingPattern)
	}
	if counter.calls != 1 {
		t.Fatalf("page counter calls: got %d, want 1", counter.calls)
	}
	if runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

func TestBannerUploadFromURL_DoesNotCallIngest(t *testing.T) {
	router, _, _, runner, _ := newBannerUploadFromURLTestRouter(t, 100, "customercare.ellucian.com", fakeUploadHTTPDownloader{
		result: upload.DownloadResult{
			Filename:  "Banner_Finance_9.3.22.pdf",
			SizeBytes: 8,
			Body:      io.NopCloser(strings.NewReader("%PDF-1.4")),
		},
	})

	w := performURLUpload(router, validURLUploadPayload(t))

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

func TestBannerUploadFromURL_RouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(&config.Config{MaxUploadSizeMB: 100, UploadURLAllowlist: "customercare.ellucian.com"})

	w := performURLUpload(router, validURLUploadPayload(t))

	if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed {
		t.Fatalf("route not registered: status=%d body=%s", w.Code, w.Body.String())
	}
}

func newBannerUploadFromURLTestRouter(t *testing.T, maxUploadSizeMB int, allowlist string, downloader fakeUploadHTTPDownloader) (*gin.Engine, *fakeUploadBlobStore, *fakeUploadPageCounter, *fakeUploadIngestRunner, *fakeUploadHTTPDownloader) {
	t.Helper()
	router, store, counter, runner := newBannerUploadFromURLRouterWithDownloader(t, maxUploadSizeMB, allowlist, &downloader)
	return router, store, counter, runner, &downloader
}

func newBannerUploadFromURLRouterWithDownloader(t *testing.T, maxUploadSizeMB int, allowlist string, downloader upload.HTTPDownloader) (*gin.Engine, *fakeUploadBlobStore, *fakeUploadPageCounter, *fakeUploadIngestRunner) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := &fakeUploadBlobStore{blobs: map[string][]byte{}}
	counter := &fakeUploadPageCounter{pages: 1}
	runner := &fakeUploadIngestRunner{}
	service := upload.NewService(upload.Dependencies{
		BlobStore:      store,
		PageCounter:    counter,
		IngestRunner:   runner,
		Clock:          fakeUploadClock{},
		IDGenerator:    fakeUploadIDGenerator{},
		HTTPDownloader: downloader,
	})

	h := &Handler{
		cfg:           &config.Config{MaxUploadSizeMB: maxUploadSizeMB, UploadURLAllowlist: allowlist},
		uploadService: service,
	}
	router := gin.New()
	router.POST("/banner/upload/from-url", h.BannerUploadFromURL)
	return router, store, counter, runner
}

func validURLUploadPayload(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"url":         "https://customercare.ellucian.com/downloads/Banner_Finance_9.3.22.pdf",
		"source_type": "banner",
		"module":      "Finance",
		"version":     "9.3.22",
		"year":        "2026",
	}
}

func performURLUpload(router http.Handler, payload map[string]string) *httptest.ResponseRecorder {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/banner/upload/from-url", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

type fakeUploadHTTPDownloader struct {
	result upload.DownloadResult
	err    error
	calls  int
}

func (f *fakeUploadHTTPDownloader) Download(_ context.Context, _ string, _ int64) (upload.DownloadResult, error) {
	f.calls++
	if f.err != nil {
		return upload.DownloadResult{}, f.err
	}
	return f.result, nil
}

type countingURLDownloader struct {
	calls int
}

func (f *countingURLDownloader) Download(context.Context, string, int64) (upload.DownloadResult, error) {
	f.calls++
	return upload.DownloadResult{}, nil
}
