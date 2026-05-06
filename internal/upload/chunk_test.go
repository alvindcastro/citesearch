package upload

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestChunkUpload_NotFoundReturns404(t *testing.T) {
	service := newChunkTestService(&fakeBlobStore{blobs: map[string][]byte{}})

	_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "missing"}, 10)

	if !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("error: got %v, want ErrUploadNotFound", err)
	}
}

func TestChunkUpload_OverlapReturns400(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := newChunkTestService(store)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:      "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    20,
		ChunkedRanges: []ChunkRange{{Start: 1, End: 5}},
	})

	_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123", PageStart: 4, PageEnd: 8}, 10)

	if !errors.Is(err, ErrRangeOverlap) {
		t.Fatalf("error: got %v, want ErrRangeOverlap", err)
	}
	if runner := service.Dependencies().IngestRunner.(*recordingIngestRunner); runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

func TestChunkUpload_OutOfBoundsReturns400(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := newChunkTestService(store)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:   "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})

	_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123", PageStart: 18, PageEnd: 21}, 10)

	if !errors.Is(err, ErrRangeEnd) {
		t.Fatalf("error: got %v, want ErrRangeEnd", err)
	}
	if runner := service.Dependencies().IngestRunner.(*recordingIngestRunner); runner.calls != 0 {
		t.Fatalf("ingest calls: got %d, want 0", runner.calls)
	}
}

func TestChunkUpload_TargetedRangeCallsIngestWithStartEnd(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	runner := &recordingIngestRunner{chunkIDs: []string{"chunk-6", "chunk-7"}}
	service := newChunkTestServiceWithRunner(store, runner)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})

	resp, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123", PageStart: 6, PageEnd: 7}, 10)
	if err != nil {
		t.Fatalf("chunk upload: %v", err)
	}

	if len(runner.requests) != 1 {
		t.Fatalf("ingest calls: got %d, want 1", len(runner.requests))
	}
	req := runner.requests[0]
	if req.BlobPath != blobPath || req.StartPage != 6 || req.EndPage != 7 || req.PagesPerBatch != 10 {
		t.Fatalf("ingest request: %#v", req)
	}
	if req.LocalPath == "" {
		t.Fatal("expected downloaded local path in ingest request")
	}
	if !strings.Contains(req.LocalPath, blobPath) {
		t.Fatalf("local path %q does not preserve blob path %q", req.LocalPath, blobPath)
	}
	if resp.GapsProcessed != 1 || resp.GapsRemaining != 0 {
		t.Fatalf("response gaps: processed=%d remaining=%d", resp.GapsProcessed, resp.GapsRemaining)
	}
	if resp.PagesChunked != 2 || resp.ChunksIndexed != 2 {
		t.Fatalf("response chunk counts: pages=%d chunks=%d", resp.PagesChunked, resp.ChunksIndexed)
	}
	if resp.Status != StatusPartial || resp.GapCount != 2 {
		t.Fatalf("response state: status=%q gap_count=%d", resp.Status, resp.GapCount)
	}
	got := readChunkSidecar(t, service, blobPath)
	if len(got.ChunkedRanges) != 1 || got.ChunkedRanges[0].Start != 6 || got.ChunkedRanges[0].End != 7 {
		t.Fatalf("chunked ranges: %#v", got.ChunkedRanges)
	}
	if !reflect.DeepEqual(got.ChunkedRanges[0].ChunkIDs, []string{"chunk-6", "chunk-7"}) {
		t.Fatalf("chunk ids: got %v", got.ChunkedRanges[0].ChunkIDs)
	}
}

func TestChunkUpload_AllRemainingProcessesGapsInOrder(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	runner := &recordingIngestRunner{}
	service := newChunkTestServiceWithRunner(store, runner)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:      blobPath,
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    20,
		ChunkedRanges: []ChunkRange{{Start: 6, End: 8}, {Start: 15, End: 16}},
	})

	resp, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123"}, 5)
	if err != nil {
		t.Fatalf("chunk upload: %v", err)
	}

	gotRanges := ingestRequestRanges(runner.requests)
	wantRanges := []ChunkRange{{Start: 1, End: 5}, {Start: 9, End: 14}, {Start: 17, End: 20}}
	if !reflect.DeepEqual(gotRanges, wantRanges) {
		t.Fatalf("ingest ranges: got %#v, want %#v", gotRanges, wantRanges)
	}
	if resp.GapsProcessed != 3 || resp.GapsRemaining != 0 || resp.Status != StatusComplete || resp.PagesChunked != 15 {
		t.Fatalf("response: processed=%d remaining=%d pages=%d status=%q", resp.GapsProcessed, resp.GapsRemaining, resp.PagesChunked, resp.Status)
	}
}

func TestChunkUpload_WritesSidecarAfterEachGap(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	runner := &recordingIngestRunner{}
	service := newChunkTestServiceWithRunner(store, runner)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:      blobPath,
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    10,
		ChunkedRanges: []ChunkRange{{Start: 4, End: 5}},
	})
	store.writeJSONCalls = 0

	_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123"}, 5)
	if err != nil {
		t.Fatalf("chunk upload: %v", err)
	}

	if store.writeJSONCalls != 2 {
		t.Fatalf("sidecar writes: got %d, want 2", store.writeJSONCalls)
	}
}

func TestChunkUpload_FailureMidwayPreservesCompletedGaps(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	runner := &recordingIngestRunner{failOnCall: 2}
	service := newChunkTestServiceWithRunner(store, runner)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:      blobPath,
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    10,
		ChunkedRanges: []ChunkRange{{Start: 4, End: 5}},
	})

	_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123"}, 5)
	if err == nil {
		t.Fatal("expected midway failure")
	}

	got := readChunkSidecar(t, service, blobPath)
	gotRanges := simpleRanges(got.ChunkedRanges)
	wantRanges := []ChunkRange{{Start: 1, End: 3}, {Start: 4, End: 5}}
	if !reflect.DeepEqual(gotRanges, wantRanges) {
		t.Fatalf("persisted ranges: got %#v, want %#v", gotRanges, wantRanges)
	}
}

func TestChunkUpload_ResponseIncludesGapCountsAndStatus(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	service := newChunkTestService(store)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 4,
	})

	resp, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123"}, 10)
	if err != nil {
		t.Fatalf("chunk upload: %v", err)
	}

	if resp.Status != StatusComplete || resp.GapCount != 0 || resp.GapSummary != "fully indexed" {
		t.Fatalf("response derived fields: status=%q gap_count=%d summary=%q", resp.Status, resp.GapCount, resp.GapSummary)
	}
	if resp.GapsProcessed != 1 || resp.GapsRemaining != 0 {
		t.Fatalf("response gap counters: processed=%d remaining=%d", resp.GapsProcessed, resp.GapsRemaining)
	}
}

func newChunkTestService(store *fakeBlobStore) *Service {
	return newChunkTestServiceWithRunner(store, &recordingIngestRunner{})
}

func newChunkTestServiceWithRunner(store *fakeBlobStore, runner *recordingIngestRunner) *Service {
	return NewService(Dependencies{
		BlobStore:    store,
		PageCounter:  fakePageCounter{},
		IngestRunner: runner,
		Clock:        fakeClock{},
		IDGenerator:  fakeIDGenerator{},
	})
}

func writeChunkSidecar(t *testing.T, service *Service, state SidecarState) {
	t.Helper()
	if err := service.WriteSidecar(context.Background(), state); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

func readChunkSidecar(t *testing.T, service *Service, blobPath string) SidecarState {
	t.Helper()
	state, err := service.ReadSidecar(context.Background(), blobPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	return state
}

func ingestRequestRanges(requests []IngestRequest) []ChunkRange {
	ranges := make([]ChunkRange, 0, len(requests))
	for _, req := range requests {
		ranges = append(ranges, ChunkRange{Start: req.StartPage, End: req.EndPage})
	}
	return ranges
}

func simpleRanges(ranges []ChunkRange) []ChunkRange {
	out := make([]ChunkRange, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, ChunkRange{Start: r.Start, End: r.End})
	}
	return out
}

type recordingIngestRunner struct {
	calls      int
	requests   []IngestRequest
	chunkIDs   []string
	failOnCall int
}

func (f *recordingIngestRunner) Run(_ context.Context, req IngestRequest) (IngestResult, error) {
	f.calls++
	f.requests = append(f.requests, req)
	if f.failOnCall > 0 && f.calls == f.failOnCall {
		return IngestResult{}, errors.New("ingest failed")
	}
	return IngestResult{
		DocumentsProcessed: 1,
		ChunksIndexed:      len(f.chunkIDs),
		ChunkIDs:           f.chunkIDs,
	}, nil
}
