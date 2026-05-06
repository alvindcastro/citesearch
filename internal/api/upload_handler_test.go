package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"citesearch/config"
	"citesearch/internal/blobstore"
	"citesearch/internal/upload"

	"github.com/gin-gonic/gin"
)

func TestBannerUpload_MissingSourceType_Returns400(t *testing.T) {
	router, store, _, runner := newBannerUploadTestRouter(t, 100)
	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", []byte("%PDF-1.4"), map[string]string{
		"module":  "Finance",
		"version": "9.3.22",
		"year":    "2026",
	})

	w := performMultipartUpload(router, body, contentType)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUpload_MissingModuleForBanner_Returns400(t *testing.T) {
	router, store, _, runner := newBannerUploadTestRouter(t, 100)
	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", []byte("%PDF-1.4"), map[string]string{
		"source_type": "banner",
		"version":     "9.3.22",
		"year":        "2026",
	})

	w := performMultipartUpload(router, body, contentType)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUpload_UnsupportedExtension_Returns400(t *testing.T) {
	router, store, _, runner := newBannerUploadTestRouter(t, 100)
	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.docx", []byte("docx"), validBannerUploadFields())

	w := performMultipartUpload(router, body, contentType)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUpload_SOPSourceType_Returns400(t *testing.T) {
	router, store, _, runner := newBannerUploadTestRouter(t, 100)
	fields := validBannerUploadFields()
	fields["source_type"] = "sop"
	body, contentType := multipartUploadBody(t, "SOP-42.pdf", []byte("%PDF-1.4"), fields)

	w := performMultipartUpload(router, body, contentType)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUpload_FileTooLarge_Returns413(t *testing.T) {
	router, store, _, runner := newBannerUploadTestRouter(t, 1)
	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", bytes.Repeat([]byte("x"), 1024*1024+1), validBannerUploadFields())

	w := performMultipartUpload(router, body, contentType)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusRequestEntityTooLarge, w.Body.String())
	}
	assertNoUploadSideEffects(t, store, runner)
}

func TestBannerUpload_DuplicateBlob_Returns409(t *testing.T) {
	router, store, _, runner := newBannerUploadTestRouter(t, 100)
	store.blobs["banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"] = []byte("existing")
	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", []byte("%PDF-1.4"), validBannerUploadFields())

	w := performMultipartUpload(router, body, contentType)

	if w.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusConflict, w.Body.String())
	}
	if store.uploadCalls != 0 {
		t.Fatalf("upload calls: got %d, want 0", store.uploadCalls)
	}
	if store.writeJSONCalls != 0 {
		t.Fatalf("sidecar writes: got %d, want 0", store.writeJSONCalls)
	}
	if runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

func TestBannerUpload_CreatesBlobAndInitialSidecar(t *testing.T) {
	router, store, counter, runner := newBannerUploadTestRouter(t, 100)
	counter.pages = 120
	pdf := []byte("%PDF-1.4\nfixture")
	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", pdf, validBannerUploadFields())

	w := performMultipartUpload(router, body, contentType)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	if got := string(store.blobs[blobPath]); got != string(pdf) {
		t.Fatalf("blob content: got %q, want %q", got, string(pdf))
	}

	sidecarPath := upload.SidecarPath(blobPath)
	var sidecar upload.SidecarState
	if err := json.Unmarshal(store.blobs[sidecarPath], &sidecar); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	if sidecar.UploadID != "upload-123" {
		t.Fatalf("upload id: got %q, want upload-123", sidecar.UploadID)
	}
	if !sidecar.UploadedAt.Equal(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("uploaded_at: got %s", sidecar.UploadedAt)
	}
	if sidecar.Status != upload.StatusPending || sidecar.ChunkingPattern != upload.PatternNone {
		t.Fatalf("sidecar state: status=%q pattern=%q", sidecar.Status, sidecar.ChunkingPattern)
	}
	if sidecar.GapCount != 1 || sidecar.GapSummary != "1 gap: pages 1-120 (120 pages total unchunked)" {
		t.Fatalf("gap state: count=%d summary=%q", sidecar.GapCount, sidecar.GapSummary)
	}
	if len(sidecar.ChunkedRanges) != 0 {
		t.Fatalf("chunked ranges: got %#v, want empty", sidecar.ChunkedRanges)
	}
	if len(sidecar.UnchunkedRanges) != 1 || sidecar.UnchunkedRanges[0].Start != 1 || sidecar.UnchunkedRanges[0].End != 120 {
		t.Fatalf("unchunked ranges: got %#v", sidecar.UnchunkedRanges)
	}
	if counter.calls != 1 {
		t.Fatalf("page counter calls: got %d, want 1", counter.calls)
	}
	if runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}

	var resp upload.UploadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.UploadID != "upload-123" || resp.BlobPath != blobPath || resp.TotalPages != 120 {
		t.Fatalf("response metadata: %#v", resp)
	}
	if resp.Status != upload.StatusPending || resp.ChunkingPattern != upload.PatternNone || resp.GapCount != 1 {
		t.Fatalf("response state: %#v", resp)
	}
	if !strings.Contains(resp.Message, "No pages chunked yet") {
		t.Fatalf("response message: %q", resp.Message)
	}
}

func TestBannerUpload_DoesNotCallIngest(t *testing.T) {
	router, _, _, runner := newBannerUploadTestRouter(t, 100)
	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", []byte("%PDF-1.4"), validBannerUploadFields())

	w := performMultipartUpload(router, body, contentType)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

func TestBannerUpload_RouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(&config.Config{MaxUploadSizeMB: 100})
	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", []byte("%PDF-1.4"), map[string]string{
		"module":  "Finance",
		"version": "9.3.22",
		"year":    "2026",
	})

	w := performMultipartUpload(router, body, contentType)

	if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed {
		t.Fatalf("route not registered: status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func newBannerUploadTestRouter(t *testing.T, maxUploadSizeMB int) (*gin.Engine, *fakeUploadBlobStore, *fakeUploadPageCounter, *fakeUploadIngestRunner) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := &fakeUploadBlobStore{blobs: map[string][]byte{}}
	counter := &fakeUploadPageCounter{pages: 1}
	runner := &fakeUploadIngestRunner{}
	service := upload.NewService(upload.Dependencies{
		BlobStore:    store,
		PageCounter:  counter,
		IngestRunner: runner,
		Clock:        fakeUploadClock{},
		IDGenerator:  fakeUploadIDGenerator{},
	})

	h := &Handler{
		cfg:           &config.Config{MaxUploadSizeMB: maxUploadSizeMB},
		uploadService: service,
	}
	router := gin.New()
	router.POST("/banner/upload", h.BannerUpload)
	return router, store, counter, runner
}

func multipartUploadBody(t *testing.T, filename string, fileContent []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(fileContent); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}

func validBannerUploadFields() map[string]string {
	return map[string]string{
		"source_type": "banner",
		"module":      "Finance",
		"version":     "9.3.22",
		"year":        "2026",
	}
}

func performMultipartUpload(router http.Handler, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/banner/upload", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func assertNoUploadSideEffects(t *testing.T, store *fakeUploadBlobStore, runner *fakeUploadIngestRunner) {
	t.Helper()
	if store.uploadCalls != 0 {
		t.Fatalf("upload calls: got %d, want 0", store.uploadCalls)
	}
	if store.writeJSONCalls != 0 {
		t.Fatalf("sidecar writes: got %d, want 0", store.writeJSONCalls)
	}
	if runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

type fakeUploadBlobStore struct {
	blobs          map[string][]byte
	uploadCalls    int
	writeJSONCalls int
}

func (f *fakeUploadBlobStore) Upload(_ context.Context, blobPath string, content io.Reader, _ string) error {
	f.uploadCalls++
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	f.blobs[blobPath] = data
	return nil
}

func (f *fakeUploadBlobStore) Download(_ context.Context, blobPath string, dest io.Writer) error {
	_, err := dest.Write(f.blobs[blobPath])
	return err
}

func (f *fakeUploadBlobStore) Exists(_ context.Context, blobPath string) (bool, error) {
	_, ok := f.blobs[blobPath]
	return ok, nil
}

func (f *fakeUploadBlobStore) Delete(_ context.Context, blobPath string) error {
	delete(f.blobs, blobPath)
	return nil
}

func (f *fakeUploadBlobStore) ReadJSON(_ context.Context, blobPath string, dest any) error {
	return json.Unmarshal(f.blobs[blobPath], dest)
}

func (f *fakeUploadBlobStore) WriteJSON(_ context.Context, blobPath string, value any) error {
	f.writeJSONCalls++
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f.blobs[blobPath] = data
	return nil
}

func (f *fakeUploadBlobStore) List(_ context.Context, prefix string) ([]blobstore.Info, error) {
	var out []blobstore.Info
	for name, data := range f.blobs {
		if strings.HasPrefix(name, prefix) {
			out = append(out, blobstore.Info{Name: name, SizeBytes: int64(len(data))})
		}
	}
	return out, nil
}

type fakeUploadPageCounter struct {
	pages int
	calls int
}

func (f *fakeUploadPageCounter) CountPages(path string) (int, error) {
	f.calls++
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return f.pages, nil
}

type fakeUploadIngestRunner struct {
	calls      int
	requests   []upload.IngestRequest
	chunkIDs   []string
	failOnCall int
	block      chan struct{}
	started    chan struct{}
	startOnce  sync.Once
}

func (f *fakeUploadIngestRunner) Run(_ context.Context, req upload.IngestRequest) (upload.IngestResult, error) {
	f.calls++
	f.requests = append(f.requests, req)
	if f.block != nil {
		if f.started == nil {
			f.started = make(chan struct{})
		}
		f.startOnce.Do(func() {
			close(f.started)
		})
		<-f.block
	}
	if f.failOnCall > 0 && f.calls == f.failOnCall {
		return upload.IngestResult{}, errors.New("ingest failed")
	}
	return upload.IngestResult{
		DocumentsProcessed: 1,
		ChunksIndexed:      len(f.chunkIDs),
		ChunkIDs:           f.chunkIDs,
	}, nil
}

func (f *fakeUploadIngestRunner) waitUntilStarted(t *testing.T) {
	t.Helper()
	if f.started == nil {
		f.started = make(chan struct{})
	}
	select {
	case <-f.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ingest runner to start")
	}
}

type fakeUploadClock struct{}

func (fakeUploadClock) Now() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

type fakeUploadIDGenerator struct{}

func (fakeUploadIDGenerator) NewID() string {
	return "upload-123"
}
