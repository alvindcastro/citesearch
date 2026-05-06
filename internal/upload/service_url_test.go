package upload

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestCreateUploadFromURL_CreatesBlobAndInitialSidecar(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	downloader := &recordingHTTPDownloader{result: DownloadResult{
		Filename:    "Banner_Finance_9.3.22.pdf",
		ContentType: "application/pdf",
		SizeBytes:   12,
		Body:        io.NopCloser(strings.NewReader("%PDF-1.4")),
	}}
	service := NewService(Dependencies{
		BlobStore:      store,
		PageCounter:    fakePageCounter{},
		IngestRunner:   &fakeIngestRunner{},
		Clock:          fakeClock{},
		IDGenerator:    fakeIDGenerator{},
		HTTPDownloader: downloader,
	})

	resp, err := service.CreateUploadFromURL(context.Background(), URLUploadRequest{
		URL:        "https://customercare.ellucian.com/downloads/Banner_Finance_9.3.22.pdf",
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
	}, DefaultUploadURLAllowlist, 100)
	if err != nil {
		t.Fatalf("CreateUploadFromURL: %v", err)
	}

	blobPath := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	if resp.BlobPath != blobPath || resp.UploadID != "upload-id" || resp.Status != StatusPending {
		t.Fatalf("response: %#v", resp)
	}
	if _, ok := store.blobs[blobPath]; !ok {
		t.Fatalf("expected PDF blob at %q", blobPath)
	}
	if _, ok := store.blobs[SidecarPath(blobPath)]; !ok {
		t.Fatalf("expected sidecar at %q", SidecarPath(blobPath))
	}
	if downloader.calls != 1 {
		t.Fatalf("downloader calls: got %d, want 1", downloader.calls)
	}
}

func TestCreateUploadFromURL_NonPDFDownloadDoesNotWrite(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	downloader := &recordingHTTPDownloader{result: DownloadResult{
		Filename:  "Banner_Finance_9.3.22.docx",
		SizeBytes: 4,
		Body:      io.NopCloser(strings.NewReader("docx")),
	}}
	service := NewService(Dependencies{
		BlobStore:      store,
		PageCounter:    fakePageCounter{},
		IngestRunner:   &fakeIngestRunner{},
		Clock:          fakeClock{},
		IDGenerator:    fakeIDGenerator{},
		HTTPDownloader: downloader,
	})

	_, err := service.CreateUploadFromURL(context.Background(), URLUploadRequest{
		URL:        "https://customercare.ellucian.com/downloads/Banner_Finance_9.3.22.docx",
		SourceType: SourceTypeBanner,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
	}, DefaultUploadURLAllowlist, 100)
	if err == nil {
		t.Fatal("expected non-PDF download to fail")
	}
	if len(store.blobs) != 0 {
		t.Fatalf("expected no blob writes, got %d", len(store.blobs))
	}
}

func TestCreateUploadFromURL_SOPRejectedBeforeDownload(t *testing.T) {
	store := &fakeBlobStore{blobs: map[string][]byte{}}
	downloader := &recordingHTTPDownloader{}
	service := NewService(Dependencies{
		BlobStore:      store,
		PageCounter:    fakePageCounter{},
		IngestRunner:   &fakeIngestRunner{},
		Clock:          fakeClock{},
		IDGenerator:    fakeIDGenerator{},
		HTTPDownloader: downloader,
	})

	_, err := service.CreateUploadFromURL(context.Background(), URLUploadRequest{
		URL:        "https://customercare.ellucian.com/downloads/SOP.pdf",
		SourceType: SourceTypeSOP,
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
	}, DefaultUploadURLAllowlist, 100)
	if err == nil {
		t.Fatal("expected SOP upload to fail")
	}
	if downloader.calls != 0 {
		t.Fatalf("downloader calls: got %d, want 0", downloader.calls)
	}
	if len(store.blobs) != 0 {
		t.Fatalf("expected no blob writes, got %d", len(store.blobs))
	}
}

type recordingHTTPDownloader struct {
	result DownloadResult
	err    error
	calls  int
}

func (f *recordingHTTPDownloader) Download(_ context.Context, _ string, _ int64) (DownloadResult, error) {
	f.calls++
	if f.err != nil {
		return DownloadResult{}, f.err
	}
	return f.result, nil
}
