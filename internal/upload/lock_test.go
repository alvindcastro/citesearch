package upload

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestChunkLock_SameUploadIDReturns409(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	runner := newBlockingIngestRunner()
	service := newChunkTestServiceWithRunner(store, runner)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 5,
	})

	done := make(chan error, 1)
	go func() {
		_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123"}, 10)
		done <- err
	}()
	runner.waitUntilStarted(t)

	_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123"}, 10)
	if !errors.Is(err, ErrChunkAlreadyRunning) {
		t.Fatalf("error: got %v, want ErrChunkAlreadyRunning", err)
	}

	runner.release()
	if err := <-done; err != nil {
		t.Fatalf("first chunk upload: %v", err)
	}
}

func TestChunkLock_DifferentUploadIDAllowed(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	firstBlob := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	secondBlob := "banner/student/releases/2026/Banner_Student_9.3.22.pdf"
	store.blobs[firstBlob] = []byte("%PDF-1.4")
	store.blobs[secondBlob] = []byte("%PDF-1.4")
	runner := newBlockingIngestRunner()
	service := newChunkTestServiceWithRunner(store, runner)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:   firstBlob,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 5,
	})
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:   secondBlob,
		UploadID:   "upload-456",
		UploadedAt: time.Date(2026, 4, 26, 10, 1, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Student",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 5,
	})

	done := make(chan error, 1)
	go func() {
		_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123"}, 10)
		done <- err
	}()
	runner.waitUntilStarted(t)

	_, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-456"}, 10)
	if err != nil {
		t.Fatalf("different upload id should be allowed: %v", err)
	}

	runner.release()
	if err := <-done; err != nil {
		t.Fatalf("first chunk upload: %v", err)
	}
}

func TestChunkLock_ReleasedAfterSuccess(t *testing.T) {
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
		TotalPages: 5,
	})

	if _, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123", PageStart: 1, PageEnd: 2}, 10); err != nil {
		t.Fatalf("first chunk upload: %v", err)
	}
	if _, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123", PageStart: 3, PageEnd: 5}, 10); err != nil {
		t.Fatalf("second chunk upload after success: %v", err)
	}
}

func TestChunkLock_ReleasedAfterFailure(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	runner := &recordingIngestRunner{failOnCall: 1}
	service := newChunkTestServiceWithRunner(store, runner)
	writeChunkSidecar(t, service, SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 5,
	})

	if _, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123", PageStart: 1, PageEnd: 2}, 10); err == nil {
		t.Fatal("expected first chunk upload to fail")
	}
	if _, err := service.ChunkUpload(context.Background(), ChunkRequest{UploadID: "upload-123", PageStart: 1, PageEnd: 2}, 10); err != nil {
		t.Fatalf("second chunk upload after failure: %v", err)
	}
}

type blockingIngestRunner struct {
	started     chan struct{}
	releaseOnce sync.Once
	released    chan struct{}
	inner       recordingIngestRunner
	mu          sync.Mutex
	calls       int
}

func newBlockingIngestRunner() *blockingIngestRunner {
	return &blockingIngestRunner{
		started:  make(chan struct{}),
		released: make(chan struct{}),
	}
}

func (r *blockingIngestRunner) Run(ctx context.Context, req IngestRequest) (IngestResult, error) {
	r.mu.Lock()
	r.calls++
	shouldBlock := r.calls == 1
	r.mu.Unlock()

	if shouldBlock {
		r.releaseOnce.Do(func() {
			close(r.started)
		})
		select {
		case <-r.released:
		case <-ctx.Done():
			return IngestResult{}, ctx.Err()
		}
	}
	return r.inner.Run(ctx, req)
}

func (r *blockingIngestRunner) waitUntilStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ingest runner to start")
	}
}

func (r *blockingIngestRunner) release() {
	close(r.released)
}
