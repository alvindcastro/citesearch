package api

import (
	"bytes"
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

func TestUploadWorkflow_MultipartStatusChunkListDelete(t *testing.T) {
	harness := newUploadWorkflowHarness(t)
	harness.counter.pages = 12
	harness.runner.chunkIDs = []string{"chunk-1", "chunk-2", "chunk-3"}

	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", []byte("%PDF-1.4\nworkflow"), validBannerUploadFields())
	uploadRecorder := performMultipartUpload(harness.router, body, contentType)
	if uploadRecorder.Code != http.StatusOK {
		t.Fatalf("upload status: got %d, want %d; body=%s", uploadRecorder.Code, http.StatusOK, uploadRecorder.Body.String())
	}
	var uploadResp upload.UploadResponse
	decodeWorkflowJSON(t, uploadRecorder, &uploadResp)
	if uploadResp.UploadID != "upload-123" || uploadResp.Status != upload.StatusPending || uploadResp.GapSummary != "1 gap: pages 1-12 (12 pages total unchunked)" {
		t.Fatalf("upload response: %#v", uploadResp)
	}
	if harness.runner.calls != 0 {
		t.Fatalf("ingest calls after upload: got %d, want 0", harness.runner.calls)
	}

	initialStatus := performUploadStatus(harness.router, uploadResp.UploadID)
	if initialStatus.Code != http.StatusOK {
		t.Fatalf("initial status: got %d, want %d; body=%s", initialStatus.Code, http.StatusOK, initialStatus.Body.String())
	}
	var initialStatusResp upload.StatusResponse
	decodeWorkflowJSON(t, initialStatus, &initialStatusResp)
	if initialStatusResp.Status != upload.StatusPending || initialStatusResp.QueryablePageCount != 0 || initialStatusResp.RemainingPageCount != 12 {
		t.Fatalf("initial status response: %#v", initialStatusResp)
	}

	chunkRecorder := performChunkUpload(harness.router, map[string]any{"upload_id": uploadResp.UploadID, "page_start": 1, "page_end": 12})
	if chunkRecorder.Code != http.StatusOK {
		t.Fatalf("chunk status: got %d, want %d; body=%s", chunkRecorder.Code, http.StatusOK, chunkRecorder.Body.String())
	}
	var chunkResp upload.ChunkResponse
	decodeWorkflowJSON(t, chunkRecorder, &chunkResp)
	if chunkResp.Status != upload.StatusComplete || chunkResp.PagesChunked != 12 || chunkResp.ChunksIndexed != 3 || chunkResp.GapSummary != "fully indexed" {
		t.Fatalf("chunk response: %#v", chunkResp)
	}
	if len(harness.runner.requests) != 1 || harness.runner.requests[0].StartPage != 1 || harness.runner.requests[0].EndPage != 12 {
		t.Fatalf("ingest requests: %#v", harness.runner.requests)
	}

	finalStatus := performUploadStatus(harness.router, uploadResp.UploadID)
	if finalStatus.Code != http.StatusOK {
		t.Fatalf("final status: got %d, want %d; body=%s", finalStatus.Code, http.StatusOK, finalStatus.Body.String())
	}
	var finalStatusResp upload.StatusResponse
	decodeWorkflowJSON(t, finalStatus, &finalStatusResp)
	if finalStatusResp.Status != upload.StatusComplete || finalStatusResp.QueryablePageCount != 12 || finalStatusResp.RemainingPageCount != 0 {
		t.Fatalf("final status response: %#v", finalStatusResp)
	}

	listRecorder := performUploadList(harness.router)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status: got %d, want %d; body=%s", listRecorder.Code, http.StatusOK, listRecorder.Body.String())
	}
	var summaries []upload.UploadSummary
	decodeWorkflowJSON(t, listRecorder, &summaries)
	if len(summaries) != 1 || summaries[0].UploadID != uploadResp.UploadID || summaries[0].Status != upload.StatusComplete || summaries[0].QueryablePageCount != 12 {
		t.Fatalf("list response: %#v", summaries)
	}

	deleteRecorder := performUploadDelete(harness.router, uploadResp.UploadID, false)
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("delete status: got %d, want %d; body=%s", deleteRecorder.Code, http.StatusOK, deleteRecorder.Body.String())
	}
	var deleteResp upload.DeleteResponse
	decodeWorkflowJSON(t, deleteRecorder, &deleteResp)
	if !deleteResp.BlobDeleted || !deleteResp.SidecarDeleted || deleteResp.ChunksPurged {
		t.Fatalf("delete response: %#v", deleteResp)
	}

	deletedStatus := performUploadStatus(harness.router, uploadResp.UploadID)
	if deletedStatus.Code != http.StatusNotFound {
		t.Fatalf("deleted status: got %d, want %d; body=%s", deletedStatus.Code, http.StatusNotFound, deletedStatus.Body.String())
	}
}

func TestUploadWorkflow_FromURLStatusChunkAllRemaining(t *testing.T) {
	harness := newUploadWorkflowHarness(t)
	harness.counter.pages = 9
	harness.runner.chunkIDs = []string{"chunk-url"}
	harness.downloader.result = upload.DownloadResult{
		Filename:    "Banner_Finance_9.3.22.pdf",
		ContentType: "application/pdf",
		SizeBytes:   int64(len("%PDF-1.4\nfrom-url")),
		Body:        io.NopCloser(strings.NewReader("%PDF-1.4\nfrom-url")),
	}

	uploadRecorder := performURLUpload(harness.router, validURLUploadPayload(t))
	if uploadRecorder.Code != http.StatusOK {
		t.Fatalf("url upload status: got %d, want %d; body=%s", uploadRecorder.Code, http.StatusOK, uploadRecorder.Body.String())
	}
	var uploadResp upload.UploadResponse
	decodeWorkflowJSON(t, uploadRecorder, &uploadResp)
	if uploadResp.TotalPages != 9 || uploadResp.Status != upload.StatusPending {
		t.Fatalf("url upload response: %#v", uploadResp)
	}

	statusRecorder := performUploadStatus(harness.router, uploadResp.UploadID)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("url status: got %d, want %d; body=%s", statusRecorder.Code, http.StatusOK, statusRecorder.Body.String())
	}
	var statusResp upload.StatusResponse
	decodeWorkflowJSON(t, statusRecorder, &statusResp)
	if statusResp.GapCount != 1 || statusResp.GapSummary != "1 gap: pages 1-9 (9 pages total unchunked)" {
		t.Fatalf("url status response: %#v", statusResp)
	}

	chunkRecorder := performChunkUpload(harness.router, map[string]any{"upload_id": uploadResp.UploadID})
	if chunkRecorder.Code != http.StatusOK {
		t.Fatalf("url chunk status: got %d, want %d; body=%s", chunkRecorder.Code, http.StatusOK, chunkRecorder.Body.String())
	}
	var chunkResp upload.ChunkResponse
	decodeWorkflowJSON(t, chunkRecorder, &chunkResp)
	if chunkResp.Status != upload.StatusComplete || chunkResp.PagesChunked != 9 || chunkResp.GapsProcessed != 1 {
		t.Fatalf("url chunk response: %#v", chunkResp)
	}
	if got := apiIngestRanges(harness.runner.requests); len(got) != 1 || got[0].Start != 1 || got[0].End != 9 {
		t.Fatalf("url ingest ranges: %#v", got)
	}
}

func TestUploadWorkflow_PartialSparseStatusExplainsGaps(t *testing.T) {
	harness := newUploadWorkflowHarness(t)
	harness.counter.pages = 20

	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", []byte("%PDF-1.4\nsparse"), validBannerUploadFields())
	uploadRecorder := performMultipartUpload(harness.router, body, contentType)
	if uploadRecorder.Code != http.StatusOK {
		t.Fatalf("upload status: got %d, want %d; body=%s", uploadRecorder.Code, http.StatusOK, uploadRecorder.Body.String())
	}
	var uploadResp upload.UploadResponse
	decodeWorkflowJSON(t, uploadRecorder, &uploadResp)

	for _, req := range []map[string]any{
		{"upload_id": uploadResp.UploadID, "page_start": 6, "page_end": 8},
		{"upload_id": uploadResp.UploadID, "page_start": 15, "page_end": 16},
	} {
		chunkRecorder := performChunkUpload(harness.router, req)
		if chunkRecorder.Code != http.StatusOK {
			t.Fatalf("chunk status: got %d, want %d; body=%s", chunkRecorder.Code, http.StatusOK, chunkRecorder.Body.String())
		}
	}

	statusRecorder := performUploadStatus(harness.router, uploadResp.UploadID)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", statusRecorder.Code, http.StatusOK, statusRecorder.Body.String())
	}
	var statusResp upload.StatusResponse
	decodeWorkflowJSON(t, statusRecorder, &statusResp)
	if statusResp.Status != upload.StatusPartial || statusResp.ChunkingPattern != upload.PatternSparse {
		t.Fatalf("sparse status fields: %#v", statusResp)
	}
	if statusResp.QueryablePageCount != 5 || statusResp.RemainingPageCount != 15 || statusResp.GapCount != 3 {
		t.Fatalf("sparse counts: %#v", statusResp)
	}
	if statusResp.GapSummary != "3 gaps: pages 1-5, 9-14, 17-20 (15 pages total unchunked)" {
		t.Fatalf("gap summary: got %q", statusResp.GapSummary)
	}
}

func TestUploadWorkflow_UploadNeverIndexesBeforeChunk(t *testing.T) {
	harness := newUploadWorkflowHarness(t)
	harness.counter.pages = 5

	body, contentType := multipartUploadBody(t, "Banner_Finance_9.3.22.pdf", []byte("%PDF-1.4\nno-index"), validBannerUploadFields())
	uploadRecorder := performMultipartUpload(harness.router, body, contentType)
	if uploadRecorder.Code != http.StatusOK {
		t.Fatalf("upload status: got %d, want %d; body=%s", uploadRecorder.Code, http.StatusOK, uploadRecorder.Body.String())
	}
	if harness.runner.calls != 0 || len(harness.runner.requests) != 0 {
		t.Fatalf("ingest calls after multipart upload: calls=%d requests=%#v", harness.runner.calls, harness.runner.requests)
	}

	var uploadResp upload.UploadResponse
	decodeWorkflowJSON(t, uploadRecorder, &uploadResp)
	statusRecorder := performUploadStatus(harness.router, uploadResp.UploadID)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%s", statusRecorder.Code, http.StatusOK, statusRecorder.Body.String())
	}
	if harness.runner.calls != 0 || len(harness.runner.requests) != 0 {
		t.Fatalf("ingest calls after status: calls=%d requests=%#v", harness.runner.calls, harness.runner.requests)
	}

	urlHarness := newUploadWorkflowHarness(t)
	urlHarness.counter.pages = 7
	urlHarness.downloader.result = upload.DownloadResult{
		Filename:    "Banner_Finance_9.3.22.pdf",
		ContentType: "application/pdf",
		SizeBytes:   int64(len("%PDF-1.4\nurl-no-index")),
		Body:        io.NopCloser(strings.NewReader("%PDF-1.4\nurl-no-index")),
	}
	urlUploadRecorder := performURLUpload(urlHarness.router, validURLUploadPayload(t))
	if urlUploadRecorder.Code != http.StatusOK {
		t.Fatalf("url upload status: got %d, want %d; body=%s", urlUploadRecorder.Code, http.StatusOK, urlUploadRecorder.Body.String())
	}
	if urlHarness.runner.calls != 0 || len(urlHarness.runner.requests) != 0 {
		t.Fatalf("ingest calls after URL upload: calls=%d requests=%#v", urlHarness.runner.calls, urlHarness.runner.requests)
	}

	var urlUploadResp upload.UploadResponse
	decodeWorkflowJSON(t, urlUploadRecorder, &urlUploadResp)
	urlStatusRecorder := performUploadStatus(urlHarness.router, urlUploadResp.UploadID)
	if urlStatusRecorder.Code != http.StatusOK {
		t.Fatalf("url status: got %d, want %d; body=%s", urlStatusRecorder.Code, http.StatusOK, urlStatusRecorder.Body.String())
	}
	urlListRecorder := performUploadList(urlHarness.router)
	if urlListRecorder.Code != http.StatusOK {
		t.Fatalf("url list: got %d, want %d; body=%s", urlListRecorder.Code, http.StatusOK, urlListRecorder.Body.String())
	}
	if urlHarness.runner.calls != 0 || len(urlHarness.runner.requests) != 0 {
		t.Fatalf("ingest calls after URL status/list: calls=%d requests=%#v", urlHarness.runner.calls, urlHarness.runner.requests)
	}
}

type uploadWorkflowHarness struct {
	router     *gin.Engine
	store      *fakeUploadBlobStore
	counter    *fakeUploadPageCounter
	runner     *fakeUploadIngestRunner
	downloader *fakeUploadHTTPDownloader
}

func newUploadWorkflowHarness(t *testing.T) *uploadWorkflowHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := &fakeUploadBlobStore{blobs: map[string][]byte{}}
	counter := &fakeUploadPageCounter{pages: 1}
	runner := &fakeUploadIngestRunner{}
	downloader := &fakeUploadHTTPDownloader{
		result: upload.DownloadResult{
			Filename:  "Banner_Finance_9.3.22.pdf",
			SizeBytes: int64(len("%PDF-1.4")),
			Body:      io.NopCloser(strings.NewReader("%PDF-1.4")),
		},
	}
	service := upload.NewService(upload.Dependencies{
		BlobStore:      store,
		PageCounter:    counter,
		IngestRunner:   runner,
		Clock:          fakeUploadClock{},
		IDGenerator:    fakeUploadIDGenerator{},
		HTTPDownloader: downloader,
	})
	h := &Handler{
		cfg: &config.Config{
			MaxUploadSizeMB:    100,
			UploadURLAllowlist: "customercare.ellucian.com",
		},
		uploadService: service,
	}
	router := gin.New()
	router.POST("/banner/upload", h.BannerUpload)
	router.GET("/banner/upload", h.BannerUploadList)
	router.POST("/banner/upload/from-url", h.BannerUploadFromURL)
	router.POST("/banner/upload/chunk", h.BannerUploadChunk)
	router.GET("/banner/upload/:upload_id/status", h.BannerUploadStatus)
	router.DELETE("/banner/upload/:upload_id", h.BannerUploadDelete)

	return &uploadWorkflowHarness{
		router:     router,
		store:      store,
		counter:    counter,
		runner:     runner,
		downloader: downloader,
	}
}

func decodeWorkflowJSON(t *testing.T, recorder *httptest.ResponseRecorder, dest any) {
	t.Helper()
	if err := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes())).Decode(dest); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
}
