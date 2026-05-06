package upload

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUploadDelete_NotFoundReturns404(t *testing.T) {
	service := NewService(Dependencies{BlobStore: &fakeBlobStore{blobs: map[string][]byte{}}})

	_, err := service.DeleteUpload(context.Background(), DeleteRequest{UploadID: "missing"})

	if !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("error: got %v, want ErrUploadNotFound", err)
	}
}

func TestUploadDelete_DeletesBlobAndSidecar(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := NewService(Dependencies{BlobStore: store})
	ctx := context.Background()
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeDeleteSidecar(t, service, SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})

	resp, err := service.DeleteUpload(ctx, DeleteRequest{UploadID: "upload-123"})
	if err != nil {
		t.Fatalf("delete upload: %v", err)
	}

	if resp.UploadID != "upload-123" || !resp.BlobDeleted || !resp.SidecarDeleted || resp.ChunksPurged {
		t.Fatalf("response: %#v", resp)
	}
	if _, ok := store.blobs[blobPath]; ok {
		t.Fatalf("blob %q still exists", blobPath)
	}
	if _, ok := store.blobs[SidecarPath(blobPath)]; ok {
		t.Fatalf("sidecar %q still exists", SidecarPath(blobPath))
	}
}

func TestUploadDelete_DoesNotCallSearchByDefault(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	runner := &recordingIngestRunner{}
	service := NewService(Dependencies{
		BlobStore:    store,
		IngestRunner: runner,
	})
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeDeleteSidecar(t, service, SidecarState{
		BlobPath:   blobPath,
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 20,
	})

	resp, err := service.DeleteUpload(context.Background(), DeleteRequest{UploadID: "upload-123"})
	if err != nil {
		t.Fatalf("delete upload: %v", err)
	}

	if resp.ChunksPurged {
		t.Fatalf("chunks_purged: got true, want false")
	}
	if runner.calls != 0 {
		t.Fatalf("ingest/search calls: got %d, want 0", runner.calls)
	}
}

func TestUploadDelete_PurgeTrueReturnsNotImplementedUntilChunkIDsPersisted(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := NewService(Dependencies{BlobStore: store})
	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	store.blobs[blobPath] = []byte("%PDF-1.4")
	writeDeleteSidecar(t, service, SidecarState{
		BlobPath:      blobPath,
		UploadID:      "upload-123",
		UploadedAt:    time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType:    SourceTypeBanner,
		Module:        "Finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    20,
		ChunkedRanges: []ChunkRange{{Start: 1, End: 10, ChunkIDs: []string{"chunk-1"}}},
	})

	_, err := service.DeleteUpload(context.Background(), DeleteRequest{UploadID: "upload-123", PurgeIndex: true})

	if !errors.Is(err, ErrPurgeNotImplemented) {
		t.Fatalf("error: got %v, want ErrPurgeNotImplemented", err)
	}
	if _, ok := store.blobs[blobPath]; !ok {
		t.Fatalf("blob %q should not be deleted when purge is not implemented", blobPath)
	}
	if _, ok := store.blobs[SidecarPath(blobPath)]; !ok {
		t.Fatalf("sidecar %q should not be deleted when purge is not implemented", SidecarPath(blobPath))
	}
}

func writeDeleteSidecar(t *testing.T, service *Service, state SidecarState) {
	t.Helper()
	if err := service.WriteSidecar(context.Background(), state); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}
