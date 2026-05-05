package upload

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"
)

func TestSidecarPath_AppendsChunksJSON(t *testing.T) {
	got := SidecarPath("banner/finance/releases/2026/Banner_Finance_9.3.22.pdf")
	want := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf.chunks.json"
	if got != want {
		t.Fatalf("sidecar path: got %q, want %q", got, want)
	}
}

func TestWriteSidecar_RoundTripsJSON(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := NewService(Dependencies{BlobStore: store})
	uploadedAt := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)

	state := SidecarState{
		BlobPath:      "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
		UploadID:      "upload-123",
		UploadedAt:    uploadedAt,
		SourceType:    "banner",
		Module:        "finance",
		Version:       "9.3.22",
		Year:          "2026",
		TotalPages:    120,
		ChunkedRanges: []ChunkRange{{Start: 1, End: 10}},
	}

	if err := service.WriteSidecar(context.Background(), state); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	got, err := service.ReadSidecar(context.Background(), state.BlobPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}

	want, _, err := DeriveSidecarState(state)
	if err != nil {
		t.Fatalf("derive sidecar: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sidecar round trip:\ngot  %#v\nwant %#v", got, want)
	}
	if _, ok := store.blobs[SidecarPath(state.BlobPath)]; !ok {
		t.Fatalf("expected sidecar stored at %q", SidecarPath(state.BlobPath))
	}
}

func TestListUploads_IgnoresNonSidecarBlobs(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := NewService(Dependencies{BlobStore: store})
	ctx := context.Background()
	uploadedAt := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)

	sidecars := []SidecarState{
		{
			BlobPath:      "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
			UploadID:      "finance-upload",
			UploadedAt:    uploadedAt,
			SourceType:    "banner",
			Module:        "finance",
			Version:       "9.3.22",
			Year:          "2026",
			TotalPages:    20,
			ChunkedRanges: []ChunkRange{{Start: 1, End: 5}},
		},
		{
			BlobPath:   "banner/student/use/Banner_Student_Use.pdf",
			UploadID:   "student-upload",
			UploadedAt: uploadedAt,
			SourceType: "banner_user_guide",
			Module:     "student",
			TotalPages: 10,
		},
	}

	for _, sidecar := range sidecars {
		if err := service.WriteSidecar(ctx, sidecar); err != nil {
			t.Fatalf("write sidecar: %v", err)
		}
	}
	store.blobs["banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"] = []byte("pdf")
	store.blobs["banner/finance/releases/2026/readme.json"] = []byte(`{"not":"a sidecar"}`)

	got, err := service.ListUploads(ctx, "banner/")
	if err != nil {
		t.Fatalf("list uploads: %v", err)
	}

	wantIDs := []string{"finance-upload", "student-upload"}
	var gotIDs []string
	for _, upload := range got {
		gotIDs = append(gotIDs, upload.UploadID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("upload ids: got %v, want %v", gotIDs, wantIDs)
	}
	if got[0].QueryablePageCount != 5 {
		t.Fatalf("queryable pages: got %d, want 5", got[0].QueryablePageCount)
	}
}

func TestBlobUpload_UsesProvidedBlobPath(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	service := NewService(Dependencies{BlobStore: store})
	blobPath := "banner/custom/path/Exact_Name.pdf"

	if err := service.UploadBlob(context.Background(), blobPath, bytes.NewBufferString("pdf"), "application/pdf"); err != nil {
		t.Fatalf("upload blob: %v", err)
	}

	if _, ok := store.blobs[blobPath]; !ok {
		t.Fatalf("expected upload at exact path %q", blobPath)
	}
	if _, ok := store.blobs["Exact_Name.pdf"]; ok {
		t.Fatalf("did not expect upload to flatten blob path")
	}
}
