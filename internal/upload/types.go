package upload

import (
	"context"
	"io"
	"time"

	"citesearch/internal/blobstore"
)

const (
	StatusPending  = "pending"
	StatusPartial  = "partial"
	StatusComplete = "complete"

	PatternNone       = "none"
	PatternSequential = "sequential"
	PatternContiguous = "contiguous"
	PatternSparse     = "sparse"
)

// SidecarState is the persisted upload/chunk state stored at {blob_path}.chunks.json.
type SidecarState struct {
	BlobPath        string       `json:"blob_path"`
	UploadID        string       `json:"upload_id"`
	UploadedAt      time.Time    `json:"uploaded_at"`
	SourceType      string       `json:"source_type"`
	Module          string       `json:"module"`
	Version         string       `json:"version"`
	Year            string       `json:"year"`
	TotalPages      int          `json:"total_pages"`
	ChunkedRanges   []ChunkRange `json:"chunked_ranges"`
	UnchunkedRanges []ChunkRange `json:"unchunked_ranges"`
	Status          string       `json:"status"`
	ChunkingPattern string       `json:"chunking_pattern"`
	GapCount        int          `json:"gap_count"`
	GapSummary      string       `json:"gap_summary"`
}

// ChunkRange describes either a completed chunk range or an unchunked gap.
type ChunkRange struct {
	Start     int        `json:"start"`
	End       int        `json:"end"`
	ChunkedAt *time.Time `json:"chunked_at,omitempty"`
	ChunkIDs  []string   `json:"chunk_ids,omitempty"`
}

type UploadRequest struct {
	SourceType string `json:"source_type"`
	Module     string `json:"module"`
	Version    string `json:"version"`
	Year       string `json:"year"`
	Filename   string `json:"filename"`
	SizeBytes  int64  `json:"size_bytes"`
}

type URLUploadRequest struct {
	URL        string `json:"url"`
	SourceType string `json:"source_type"`
	Module     string `json:"module"`
	Version    string `json:"version"`
	Year       string `json:"year"`
}

type UploadResponse struct {
	UploadID        string `json:"upload_id"`
	BlobPath        string `json:"blob_path"`
	TotalPages      int    `json:"total_pages"`
	Status          string `json:"status"`
	ChunkingPattern string `json:"chunking_pattern"`
	GapCount        int    `json:"gap_count"`
	GapSummary      string `json:"gap_summary"`
	Message         string `json:"message"`
}

type ChunkRequest struct {
	UploadID  string `json:"upload_id"`
	PageStart int    `json:"page_start,omitempty"`
	PageEnd   int    `json:"page_end,omitempty"`
}

type ChunkResponse struct {
	SidecarState
	GapsProcessed int `json:"gaps_processed"`
	GapsRemaining int `json:"gaps_remaining"`
}

type StatusResponse struct {
	SidecarState
	QueryablePageCount        int `json:"queryable_page_count"`
	RemainingPageCount        int `json:"remaining_page_count"`
	EstimatedRemainingMinutes int `json:"estimated_remaining_minutes"`
}

type UploadSummary struct {
	UploadID           string `json:"upload_id"`
	BlobPath           string `json:"blob_path"`
	Status             string `json:"status"`
	ChunkingPattern    string `json:"chunking_pattern"`
	TotalPages         int    `json:"total_pages"`
	QueryablePageCount int    `json:"queryable_page_count"`
	GapCount           int    `json:"gap_count"`
	GapSummary         string `json:"gap_summary"`
}

type BlobInfo = blobstore.Info

type BlobStore interface {
	Upload(ctx context.Context, blobPath string, content io.Reader, contentType string) error
	Download(ctx context.Context, blobPath string, dest io.Writer) error
	Exists(ctx context.Context, blobPath string) (bool, error)
	Delete(ctx context.Context, blobPath string) error
	ReadJSON(ctx context.Context, blobPath string, dest any) error
	WriteJSON(ctx context.Context, blobPath string, value any) error
	List(ctx context.Context, prefix string) ([]BlobInfo, error)
}

type PageCounter interface {
	CountPages(filePath string) (int, error)
}

type IngestRunner interface {
	Run(ctx context.Context, req IngestRequest) (IngestResult, error)
}

type IngestRequest struct {
	BlobPath      string `json:"blob_path"`
	LocalPath     string `json:"local_path"`
	PagesPerBatch int    `json:"pages_per_batch"`
	StartPage     int    `json:"start_page"`
	EndPage       int    `json:"end_page"`
	Overwrite     bool   `json:"overwrite"`
}

type IngestResult struct {
	DocumentsProcessed int      `json:"documents_processed"`
	ChunksIndexed      int      `json:"chunks_indexed"`
	ChunkIDs           []string `json:"chunk_ids"`
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type HTTPDownloader interface {
	Download(ctx context.Context, url string, maxBytes int64) (DownloadResult, error)
}

type DownloadResult struct {
	Filename    string
	ContentType string
	SizeBytes   int64
	Body        io.ReadCloser
}

type Dependencies struct {
	BlobStore      BlobStore
	PageCounter    PageCounter
	IngestRunner   IngestRunner
	Clock          Clock
	IDGenerator    IDGenerator
	HTTPDownloader HTTPDownloader
}
