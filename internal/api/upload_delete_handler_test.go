package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"citesearch/config"
	"citesearch/internal/upload"

	"github.com/gin-gonic/gin"
)

func TestUploadDelete_NotFoundReturns404(t *testing.T) {
	router, _, _ := newUploadDeleteTestRouter(t)

	w := performUploadDelete(router, "missing", false)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestUploadDelete_DeletesBlobAndSidecar(t *testing.T) {
	router, store, _ := newUploadDeleteTestRouter(t)
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeAPIDeleteSidecar(t, store, upload.SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})

	w := performUploadDelete(router, "upload-123", false)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp upload.DeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.UploadID != "upload-123" || !resp.BlobDeleted || !resp.SidecarDeleted || resp.ChunksPurged {
		t.Fatalf("response: %#v", resp)
	}
	if _, ok := store.blobs[blobPath]; ok {
		t.Fatalf("blob %q still exists", blobPath)
	}
	if _, ok := store.blobs[upload.SidecarPath(blobPath)]; ok {
		t.Fatalf("sidecar %q still exists", upload.SidecarPath(blobPath))
	}
}

func TestUploadDelete_DoesNotCallSearchByDefault(t *testing.T) {
	router, store, runner := newUploadDeleteTestRouter(t)
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeAPIDeleteSidecar(t, store, upload.SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})

	w := performUploadDelete(router, "upload-123", false)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if runner.calls != 0 {
		t.Fatalf("ingest/search calls: got %d, want 0", runner.calls)
	}
}

func TestUploadDelete_PurgeTrueReturnsNotImplementedUntilChunkIDsPersisted(t *testing.T) {
	router, store, _ := newUploadDeleteTestRouter(t)
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeAPIDeleteSidecar(t, store, upload.SidecarState{
		BlobPath:      blobPath,
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    upload.SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    20,
		ChunkedRanges: []upload.ChunkRange{{Start: 1, End: 10, ChunkIDs: []string{"chunk-1"}}},
	})

	w := performUploadDelete(router, "upload-123", true)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusNotImplemented, w.Body.String())
	}
	if _, ok := store.blobs[blobPath]; !ok {
		t.Fatalf("blob %q should not be deleted when purge is not implemented", blobPath)
	}
	if _, ok := store.blobs[upload.SidecarPath(blobPath)]; !ok {
		t.Fatalf("sidecar %q should not be deleted when purge is not implemented", upload.SidecarPath(blobPath))
	}
}

func TestUploadDelete_RouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(&config.Config{})

	w := performUploadDelete(router, "missing", false)

	if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed {
		t.Fatalf("delete route not registered: status=%d body=%s", w.Code, w.Body.String())
	}
}

func newUploadDeleteTestRouter(t *testing.T) (*gin.Engine, *fakeUploadBlobStore, *fakeUploadIngestRunner) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := &fakeUploadBlobStore{blobs: map[string][]byte{}}
	runner := &fakeUploadIngestRunner{}
	service := upload.NewService(upload.Dependencies{
		BlobStore:    store,
		PageCounter:  &fakeUploadPageCounter{pages: 1},
		IngestRunner: runner,
		Clock:        fakeUploadClock{},
		IDGenerator:  fakeUploadIDGenerator{},
	})
	h := &Handler{
		cfg:           &config.Config{},
		uploadService: service,
	}
	router := gin.New()
	router.DELETE("/banner/upload/:upload_id", h.BannerUploadDelete)
	return router, store, runner
}

func performUploadDelete(router http.Handler, uploadID string, purgeIndex bool) *httptest.ResponseRecorder {
	target := "/banner/upload/" + uploadID
	if purgeIndex {
		target += "?purge_index=true"
	}
	req := httptest.NewRequest(http.MethodDelete, target, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func writeAPIDeleteSidecar(t *testing.T, store *fakeUploadBlobStore, state upload.SidecarState) {
	t.Helper()
	service := upload.NewService(upload.Dependencies{BlobStore: store})
	if err := service.WriteSidecar(context.Background(), state); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}
