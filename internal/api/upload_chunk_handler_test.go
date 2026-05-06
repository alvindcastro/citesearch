package api

import (
	"bytes"
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

func TestChunkUpload_NotFoundReturns404(t *testing.T) {
	router, _, _ := newChunkUploadTestRouter(t)

	w := performChunkUpload(router, map[string]any{"upload_id": "missing"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestChunkUpload_OverlapReturns400(t *testing.T) {
	router, store, runner := newChunkUploadTestRouter(t)
	writeAPIChunkSidecar(t, store, upload.SidecarState{
		BlobPath:      "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    upload.SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    20,
		ChunkedRanges: []upload.ChunkRange{{Start: 1, End: 5}},
	})

	w := performChunkUpload(router, map[string]any{"upload_id": "upload-123", "page_start": 5, "page_end": 8})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

func TestChunkUpload_OutOfBoundsReturns400(t *testing.T) {
	router, store, runner := newChunkUploadTestRouter(t)
	writeAPIChunkSidecar(t, store, upload.SidecarState{
		BlobPath:   "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})

	w := performChunkUpload(router, map[string]any{"upload_id": "upload-123", "page_start": 1, "page_end": 21})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

func TestChunkUpload_TargetedRangeCallsIngestWithStartEnd(t *testing.T) {
	router, store, runner := newChunkUploadTestRouter(t)
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	runner.chunkIDs = []string{"chunk-6", "chunk-7"}
	writeAPIChunkSidecar(t, store, upload.SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})

	w := performChunkUpload(router, map[string]any{"upload_id": "upload-123", "page_start": 6, "page_end": 7})

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(runner.requests) != 1 || runner.requests[0].StartPage != 6 || runner.requests[0].EndPage != 7 {
		t.Fatalf("ingest requests: %#v", runner.requests)
	}
	var resp upload.ChunkResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PagesChunked != 2 || resp.ChunksIndexed != 2 || resp.GapsRemaining != 0 || resp.Status != upload.StatusPartial || resp.GapCount != 2 {
		t.Fatalf("response: %#v", resp)
	}
}

func TestChunkUpload_AllRemainingProcessesGapsInOrder(t *testing.T) {
	router, store, runner := newChunkUploadTestRouter(t)
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeAPIChunkSidecar(t, store, upload.SidecarState{
		BlobPath:      blobPath,
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    upload.SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    20,
		ChunkedRanges: []upload.ChunkRange{{Start: 6, End: 8}, {Start: 15, End: 16}},
	})

	w := performChunkUpload(router, map[string]any{"upload_id": "upload-123"})

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := apiIngestRanges(runner.requests)
	want := []upload.ChunkRange{{Start: 1, End: 5}, {Start: 9, End: 14}, {Start: 17, End: 20}}
	if len(got) != len(want) {
		t.Fatalf("ingest ranges: got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i].Start != want[i].Start || got[i].End != want[i].End {
			t.Fatalf("ingest ranges: got %#v, want %#v", got, want)
		}
	}
}

func TestChunkUpload_WritesSidecarAfterEachGap(t *testing.T) {
	router, store, _ := newChunkUploadTestRouter(t)
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeAPIChunkSidecar(t, store, upload.SidecarState{
		BlobPath:      blobPath,
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    upload.SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    10,
		ChunkedRanges: []upload.ChunkRange{{Start: 4, End: 5}},
	})
	store.writeJSONCalls = 0

	w := performChunkUpload(router, map[string]any{"upload_id": "upload-123"})

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if store.writeJSONCalls != 2 {
		t.Fatalf("sidecar writes: got %d, want 2", store.writeJSONCalls)
	}
}

func TestChunkUpload_FailureMidwayPreservesCompletedGaps(t *testing.T) {
	router, store, runner := newChunkUploadTestRouter(t)
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	runner.failOnCall = 2
	writeAPIChunkSidecar(t, store, upload.SidecarState{
		BlobPath:      blobPath,
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    upload.SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    10,
		ChunkedRanges: []upload.ChunkRange{{Start: 4, End: 5}},
	})

	w := performChunkUpload(router, map[string]any{"upload_id": "upload-123"})

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	var sidecar upload.SidecarState
	if err := json.Unmarshal(store.blobs[upload.SidecarPath(blobPath)], &sidecar); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	if len(sidecar.ChunkedRanges) != 2 || sidecar.ChunkedRanges[0].Start != 1 || sidecar.ChunkedRanges[0].End != 3 || sidecar.ChunkedRanges[1].Start != 4 || sidecar.ChunkedRanges[1].End != 5 {
		t.Fatalf("persisted ranges: %#v", sidecar.ChunkedRanges)
	}
}

func TestChunkUpload_ResponseIncludesGapCountsAndStatus(t *testing.T) {
	router, store, _ := newChunkUploadTestRouter(t)
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeAPIChunkSidecar(t, store, upload.SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 4,
	})

	w := performChunkUpload(router, map[string]any{"upload_id": "upload-123"})

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp upload.ChunkResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != upload.StatusComplete || resp.GapCount != 0 || resp.GapSummary != "fully indexed" {
		t.Fatalf("response derived fields: %#v", resp)
	}
}

func TestChunkUpload_RouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(&config.Config{MaxUploadSizeMB: 100})

	w := performChunkUpload(router, map[string]any{"upload_id": "missing"})

	if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed {
		t.Fatalf("route not registered: status=%d body=%s", w.Code, w.Body.String())
	}
}

func newChunkUploadTestRouter(t *testing.T) (*gin.Engine, *fakeUploadBlobStore, *fakeUploadIngestRunner) {
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
	router.POST("/banner/upload/chunk", h.BannerUploadChunk)
	return router, store, runner
}

func performChunkUpload(router http.Handler, payload map[string]any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/banner/upload/chunk", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func writeAPIChunkSidecar(t *testing.T, store *fakeUploadBlobStore, state upload.SidecarState) {
	t.Helper()
	service := upload.NewService(upload.Dependencies{BlobStore: store})
	if err := service.WriteSidecar(context.Background(), state); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

func apiIngestRanges(requests []upload.IngestRequest) []upload.ChunkRange {
	out := make([]upload.ChunkRange, 0, len(requests))
	for _, req := range requests {
		out = append(out, upload.ChunkRange{Start: req.StartPage, End: req.EndPage})
	}
	return out
}
