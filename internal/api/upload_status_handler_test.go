package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"citesearch/config"
	"citesearch/internal/upload"

	"github.com/gin-gonic/gin"
)

func TestUploadStatus_NotFoundReturns404(t *testing.T) {
	router, _, _ := newUploadStatusListTestRouter(t)

	w := performUploadStatus(router, "missing")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestUploadStatus_ReturnsDerivedFields(t *testing.T) {
	router, store, _ := newUploadStatusListTestRouter(t)
	writeAPIStatusSidecar(t, store, upload.SidecarState{
		BlobPath:      "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    upload.SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    120,
		ChunkedRanges: []upload.ChunkRange{{Start: 33, End: 44}, {Start: 78, End: 90}},
	})

	w := performUploadStatus(router, "upload-123")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp upload.StatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != upload.StatusPartial || resp.ChunkingPattern != upload.PatternSparse {
		t.Fatalf("response status: %#v", resp)
	}
	if resp.QueryablePageCount != 25 || resp.RemainingPageCount != 95 || resp.EstimatedRemainingMinutes != 7 {
		t.Fatalf("response counts: %#v", resp)
	}
	if resp.GapSummary != "3 gaps: pages 1-32, 45-77, 91-120 (95 pages total unchunked)" {
		t.Fatalf("gap summary: %q", resp.GapSummary)
	}
}

func TestUploadStatus_DoesNotWriteSidecar(t *testing.T) {
	router, store, _ := newUploadStatusListTestRouter(t)
	writeAPIStatusSidecar(t, store, upload.SidecarState{
		BlobPath:   "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})
	store.writeJSONCalls = 0

	w := performUploadStatus(router, "upload-123")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if store.writeJSONCalls != 0 {
		t.Fatalf("sidecar writes: got %d, want 0", store.writeJSONCalls)
	}
}

func TestUploadList_ReturnsAllSidecarSummaries(t *testing.T) {
	router, store, _ := newUploadStatusListTestRouter(t)
	writeAPIStatusSidecar(t, store, upload.SidecarState{
		BlobPath:      "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:      "finance-upload",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    upload.SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    20,
		ChunkedRanges: []upload.ChunkRange{{Start: 1, End: 5}},
	})
	writeAPIStatusSidecar(t, store, upload.SidecarState{
		BlobPath:   "banner/student/use/Banner_Student_Use.pdf",
		UploadID:   "student-upload",
		UploadedAt: time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBannerUserGuide,
		Module:     "Student",
		TotalPages: 10,
	})

	w := performUploadList(router)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp []upload.UploadSummary
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	gotIDs := []string{resp[0].UploadID, resp[1].UploadID}
	wantIDs := []string{"student-upload", "finance-upload"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("upload ids: got %v, want %v", gotIDs, wantIDs)
	}
	if resp[1].QueryablePageCount != 5 || resp[1].GapCount != 1 {
		t.Fatalf("finance summary: %#v", resp[1])
	}
}

func TestUploadList_SortsByUploadedAtDescending(t *testing.T) {
	router, store, _ := newUploadStatusListTestRouter(t)
	writeAPIStatusSidecar(t, store, upload.SidecarState{
		BlobPath:   "banner/finance/releases/2026/old.pdf",
		UploadID:   "old-upload",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 10,
	})
	writeAPIStatusSidecar(t, store, upload.SidecarState{
		BlobPath:   "banner/finance/releases/2026/new.pdf",
		UploadID:   "new-upload",
		UploadedAt: time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC),
		SourceType: upload.SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.23",
		Year:       "2026",
		TotalPages: 10,
	})

	w := performUploadList(router)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp []upload.UploadSummary
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	gotIDs := []string{resp[0].UploadID, resp[1].UploadID}
	wantIDs := []string{"new-upload", "old-upload"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("upload ids: got %v, want %v", gotIDs, wantIDs)
	}
}

func TestUploadList_EmptyReturnsEmptyArray(t *testing.T) {
	router, _, _ := newUploadStatusListTestRouter(t)

	w := performUploadList(router)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != "[]" {
		t.Fatalf("body: got %q, want []", got)
	}
}

func TestUploadStatusAndList_RoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(&config.Config{})

	status := performUploadStatus(router, "missing")
	if status.Code == http.StatusNotFound || status.Code == http.StatusMethodNotAllowed {
		t.Fatalf("status route not registered: status=%d body=%s", status.Code, status.Body.String())
	}

	list := performUploadList(router)
	if list.Code == http.StatusNotFound || list.Code == http.StatusMethodNotAllowed {
		t.Fatalf("list route not registered: status=%d body=%s", list.Code, list.Body.String())
	}
}

func newUploadStatusListTestRouter(t *testing.T) (*gin.Engine, *fakeUploadBlobStore, *fakeUploadIngestRunner) {
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
	router.GET("/banner/upload/:upload_id/status", h.BannerUploadStatus)
	router.GET("/banner/upload", h.BannerUploadList)
	return router, store, runner
}

func performUploadStatus(router http.Handler, uploadID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/banner/upload/"+uploadID+"/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func performUploadList(router http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/banner/upload", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func writeAPIStatusSidecar(t *testing.T, store *fakeUploadBlobStore, state upload.SidecarState) {
	t.Helper()
	service := upload.NewService(upload.Dependencies{BlobStore: store})
	if err := service.WriteSidecar(context.Background(), state); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}
