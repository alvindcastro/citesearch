package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestUploadPackage_ExposesSidecarStateJSONShape(t *testing.T) {
	uploadedAt := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	chunkedAt := time.Date(2026, 4, 26, 10, 5, 0, 0, time.UTC)

	state := SidecarState{
		BlobPath:        "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:        "a3f8c1d2-test",
		UploadedAt:      uploadedAt,
		SourceType:      "banner",
		Module:          "Finance",
		Version:         "9.3.22",
		Year:            "2026",
		TotalPages:      120,
		ChunkedRanges:   []ChunkRange{{Start: 33, End: 44, ChunkedAt: &chunkedAt, ChunkIDs: []string{"abc123", "def456"}}},
		UnchunkedRanges: []ChunkRange{{Start: 1, End: 32}, {Start: 45, End: 120}},
		Status:          "partial",
		ChunkingPattern: "sparse",
		GapCount:        2,
		GapSummary:      "2 gaps: pages 1-32, 45-120 (108 pages total unchunked)",
	}

	got, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}

	want := `{"blob_path":"banner/finance/releases/2026/Banner_Finance_9.3.22.pdf","upload_id":"a3f8c1d2-test","uploaded_at":"2026-04-26T10:00:00Z","source_type":"banner","module":"Finance","version":"9.3.22","year":"2026","total_pages":120,"chunked_ranges":[{"start":33,"end":44,"chunked_at":"2026-04-26T10:05:00Z","chunk_ids":["abc123","def456"]}],"unchunked_ranges":[{"start":1,"end":32},{"start":45,"end":120}],"status":"partial","chunking_pattern":"sparse","gap_count":2,"gap_summary":"2 gaps: pages 1-32, 45-120 (108 pages total unchunked)"}`
	if string(got) != want {
		t.Fatalf("sidecar JSON:\ngot  %s\nwant %s", got, want)
	}
}

func TestUploadPackage_AllowsFakeBlobStore(t *testing.T) {
	var _ BlobStore = (*fakeBlobStore)(nil)

	store := &fakeBlobStore{blobs: map[string][]byte{}}
	ctx := context.Background()
	if err := store.Upload(ctx, "banner/test.pdf", bytes.NewBufferString("pdf"), "application/pdf"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	exists, err := store.Exists(ctx, "banner/test.pdf")
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("expected uploaded blob to exist")
	}
}

func TestUploadPackage_AllowsFakeIngestRunner(t *testing.T) {
	var _ IngestRunner = (*fakeIngestRunner)(nil)

	runner := &fakeIngestRunner{chunkIDs: []string{"chunk-1", "chunk-2"}}
	result, err := runner.Run(context.Background(), IngestRequest{
		BlobPath:      "banner/test.pdf",
		LocalPath:     "/tmp/test.pdf",
		PagesPerBatch: 10,
		StartPage:     1,
		EndPage:       5,
	})
	if err != nil {
		t.Fatalf("run ingest: %v", err)
	}
	if result.ChunksIndexed != 2 {
		t.Fatalf("chunks indexed: got %d, want %d", result.ChunksIndexed, 2)
	}
	if len(result.ChunkIDs) != 2 {
		t.Fatalf("chunk ids: got %v, want two ids", result.ChunkIDs)
	}
}

func TestUploadPackage_ServiceKeepsDependenciesExplicit(t *testing.T) {
	deps := Dependencies{
		BlobStore:      &fakeBlobStore{blobs: map[string][]byte{}},
		PageCounter:    fakePageCounter{},
		IngestRunner:   &fakeIngestRunner{},
		Clock:          fakeClock{},
		IDGenerator:    fakeIDGenerator{},
		HTTPDownloader: fakeHTTPDownloader{},
	}

	service := NewService(deps)
	if service == nil {
		t.Fatal("expected service")
	}
	if service.Dependencies().BlobStore == nil {
		t.Fatal("expected explicit blob store dependency")
	}
	if service.Dependencies().Clock == nil {
		t.Fatal("expected explicit clock dependency")
	}
}

type fakeBlobStore struct {
	blobs map[string][]byte
}

func (f *fakeBlobStore) Upload(_ context.Context, blobPath string, content io.Reader, _ string) error {
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	f.blobs[blobPath] = data
	return nil
}

func (f *fakeBlobStore) Download(_ context.Context, blobPath string, dest io.Writer) error {
	_, err := dest.Write(f.blobs[blobPath])
	return err
}

func (f *fakeBlobStore) Exists(_ context.Context, blobPath string) (bool, error) {
	_, ok := f.blobs[blobPath]
	return ok, nil
}

func (f *fakeBlobStore) Delete(_ context.Context, blobPath string) error {
	delete(f.blobs, blobPath)
	return nil
}

func (f *fakeBlobStore) ReadJSON(_ context.Context, blobPath string, dest any) error {
	return json.Unmarshal(f.blobs[blobPath], dest)
}

func (f *fakeBlobStore) WriteJSON(_ context.Context, blobPath string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f.blobs[blobPath] = data
	return nil
}

func (f *fakeBlobStore) List(_ context.Context, prefix string) ([]BlobInfo, error) {
	var blobs []BlobInfo
	for name, data := range f.blobs {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			blobs = append(blobs, BlobInfo{Name: name, SizeBytes: int64(len(data))})
		}
	}
	return blobs, nil
}

type fakeIngestRunner struct {
	chunkIDs []string
}

func (f *fakeIngestRunner) Run(_ context.Context, _ IngestRequest) (IngestResult, error) {
	return IngestResult{
		DocumentsProcessed: 1,
		ChunksIndexed:      len(f.chunkIDs),
		ChunkIDs:           f.chunkIDs,
	}, nil
}

type fakePageCounter struct{}

func (fakePageCounter) CountPages(_ string) (int, error) {
	return 1, nil
}

type fakeClock struct{}

func (fakeClock) Now() time.Time {
	return time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
}

type fakeIDGenerator struct{}

func (fakeIDGenerator) NewID() string {
	return "upload-id"
}

type fakeHTTPDownloader struct{}

func (fakeHTTPDownloader) Download(_ context.Context, _ string, _ int64) (DownloadResult, error) {
	return DownloadResult{}, nil
}
