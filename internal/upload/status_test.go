package upload

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestUploadStatus_ReturnsDerivedFields(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := NewService(Dependencies{BlobStore: store})
	ctx := context.Background()
	chunkedAt := time.Date(2026, 4, 26, 10, 5, 0, 0, time.UTC)
	state := SidecarState{
		BlobPath:   "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:   "upload-123",
		UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		TotalPages: 120,
		ChunkedRanges: []ChunkRange{
			{Start: 33, End: 44, ChunkedAt: &chunkedAt},
			{Start: 78, End: 90, ChunkedAt: &chunkedAt},
		},
	}
	if err := service.WriteSidecar(ctx, state); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	store.writeJSONCalls = 0

	got, err := service.UploadStatus(ctx, "upload-123")
	if err != nil {
		t.Fatalf("upload status: %v", err)
	}

	if got.UploadID != "upload-123" || got.Status != StatusPartial || got.ChunkingPattern != PatternSparse {
		t.Fatalf("status metadata: %#v", got)
	}
	wantGaps := []ChunkRange{{Start: 1, End: 32}, {Start: 45, End: 77}, {Start: 91, End: 120}}
	if !reflect.DeepEqual(got.UnchunkedRanges, wantGaps) {
		t.Fatalf("unchunked ranges: got %#v, want %#v", got.UnchunkedRanges, wantGaps)
	}
	if got.GapCount != 3 || got.GapSummary != "3 gaps: pages 1-32, 45-77, 91-120 (95 pages total unchunked)" {
		t.Fatalf("gap fields: count=%d summary=%q", got.GapCount, got.GapSummary)
	}
	if got.QueryablePageCount != 25 || got.RemainingPageCount != 95 {
		t.Fatalf("page counts: queryable=%d remaining=%d", got.QueryablePageCount, got.RemainingPageCount)
	}
	if got.EstimatedRemainingMinutes != 7 {
		t.Fatalf("estimated remaining minutes: got %d, want 7", got.EstimatedRemainingMinutes)
	}
	if store.writeJSONCalls != 0 {
		t.Fatalf("status must not write sidecar, writes=%d", store.writeJSONCalls)
	}
}

func TestUploadStatus_NotFound(t *testing.T) {
	service := NewService(Dependencies{BlobStore: &fakeBlobStore{blobs: map[string][]byte{}}})

	_, err := service.UploadStatus(context.Background(), "missing")

	if err != ErrUploadNotFound {
		t.Fatalf("error: got %v, want %v", err, ErrUploadNotFound)
	}
}

func TestListUploads_SortsByUploadedAtDescending(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := NewService(Dependencies{BlobStore: store})
	ctx := context.Background()
	states := []SidecarState{
		{
			BlobPath:   "banner/finance/releases/2026/old.pdf",
			UploadID:   "old-upload",
			UploadedAt: time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
			SourceType: SourceTypeBanner,
			Module:     "Finance",
			Version:    "9.3.22",
			Year:       "2026",
			TotalPages: 10,
		},
		{
			BlobPath:      "banner/finance/releases/2026/new.pdf",
			UploadID:      "new-upload",
			UploadedAt:    time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC),
			SourceType:    SourceTypeBanner,
			Module:        "Finance",
			Version:       "9.3.23",
			Year:          "2026",
			TotalPages:    10,
			ChunkedRanges: []ChunkRange{{Start: 1, End: 10}},
		},
	}
	for _, state := range states {
		if err := service.WriteSidecar(ctx, state); err != nil {
			t.Fatalf("write sidecar: %v", err)
		}
	}

	got, err := service.ListUploads(ctx, "banner/")
	if err != nil {
		t.Fatalf("list uploads: %v", err)
	}

	gotIDs := []string{got[0].UploadID, got[1].UploadID}
	wantIDs := []string{"new-upload", "old-upload"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("upload ids: got %v, want %v", gotIDs, wantIDs)
	}
	if got[0].QueryablePageCount != 10 || got[0].Status != StatusComplete {
		t.Fatalf("new upload summary: %#v", got[0])
	}
}

func TestListUploads_EmptyReturnsEmptyArray(t *testing.T) {
	service := NewService(Dependencies{BlobStore: &fakeBlobStore{blobs: map[string][]byte{}}})

	got, err := service.ListUploads(context.Background(), "banner/")
	if err != nil {
		t.Fatalf("list uploads: %v", err)
	}
	if got == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("uploads: got %d, want 0", len(got))
	}
}
