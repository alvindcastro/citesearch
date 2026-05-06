package azure

import (
	"testing"

	"citesearch/internal/upload"
)

var _ upload.BlobStore = (*BlobClient)(nil)

func TestListDocuments_IgnoresSidecarJSON(t *testing.T) {
	tests := []struct {
		name     string
		blobName string
		want     bool
	}{
		{name: "pdf", blobName: "banner/finance/doc.pdf", want: true},
		{name: "txt", blobName: "banner/finance/doc.txt", want: true},
		{name: "markdown", blobName: "banner/finance/doc.md", want: true},
		{name: "sidecar for pdf", blobName: "banner/finance/doc.pdf.chunks.json", want: false},
		{name: "unrelated json", blobName: "banner/finance/doc.json", want: false},
		{name: "docx remains unsupported by blob sync", blobName: "banner/finance/doc.docx", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSupportedBlobDocument(tt.blobName)
			if got != tt.want {
				t.Fatalf("supported document: got %v, want %v", got, tt.want)
			}
		})
	}
}
