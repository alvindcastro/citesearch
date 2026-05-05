package upload

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const uploadSuccessMessage = "PDF uploaded. No pages chunked yet. Call POST /banner/upload/chunk to begin."

var ErrDuplicateBlob = errors.New("a PDF already exists at this blob path")

// Service is the upload package entry point for sidecar persistence and chunk orchestration.
type Service struct {
	deps Dependencies
}

func NewService(deps Dependencies) *Service {
	return &Service{deps: deps}
}

func (s *Service) Dependencies() Dependencies {
	return s.deps
}

func SidecarPath(blobPath string) string {
	return blobPath + ".chunks.json"
}

func IsSidecarPath(blobPath string) bool {
	return strings.HasSuffix(blobPath, ".chunks.json")
}

func (s *Service) UploadBlob(ctx context.Context, blobPath string, content io.Reader, contentType string) error {
	return s.deps.BlobStore.Upload(ctx, blobPath, content, contentType)
}

func (s *Service) CreateUploadFromFile(ctx context.Context, req UploadRequest, localPath string, maxUploadSizeMB int) (UploadResponse, error) {
	meta, err := ValidateUploadMetadata(req, maxUploadSizeMB)
	if err != nil {
		return UploadResponse{}, err
	}

	blobPath := SynthesizeBlobPath(meta)
	exists, err := s.deps.BlobStore.Exists(ctx, blobPath)
	if err != nil {
		return UploadResponse{}, err
	}
	if exists {
		return UploadResponse{}, ErrDuplicateBlob
	}

	totalPages, err := s.deps.PageCounter.CountPages(localPath)
	if err != nil {
		return UploadResponse{}, err
	}

	file, err := os.Open(localPath)
	if err != nil {
		return UploadResponse{}, err
	}
	defer file.Close()

	if err := s.UploadBlob(ctx, blobPath, file, "application/pdf"); err != nil {
		return UploadResponse{}, err
	}

	state := SidecarState{
		BlobPath:      blobPath,
		UploadID:      s.deps.IDGenerator.NewID(),
		UploadedAt:    s.deps.Clock.Now(),
		SourceType:    meta.SourceType,
		Module:        meta.Module,
		Version:       meta.Version,
		Year:          meta.Year,
		TotalPages:    totalPages,
		ChunkedRanges: nil,
	}
	derived, _, err := DeriveSidecarState(state)
	if err != nil {
		return UploadResponse{}, err
	}
	if err := s.WriteSidecar(ctx, derived); err != nil {
		return UploadResponse{}, err
	}

	return UploadResponse{
		UploadID:        derived.UploadID,
		BlobPath:        derived.BlobPath,
		TotalPages:      derived.TotalPages,
		Status:          derived.Status,
		ChunkingPattern: derived.ChunkingPattern,
		GapCount:        derived.GapCount,
		GapSummary:      derived.GapSummary,
		Message:         uploadSuccessMessage,
	}, nil
}

func (s *Service) CreateUploadFromURL(ctx context.Context, req URLUploadRequest, allowlist []string, maxUploadSizeMB int) (UploadResponse, error) {
	if strings.TrimSpace(req.URL) == "" {
		return UploadResponse{}, ErrMissingURL
	}
	if err := IsAllowedURL(req.URL, allowlist); err != nil {
		return UploadResponse{}, err
	}

	initialFilename := filenameFromURL(req.URL)
	initialReq := UploadRequest{
		SourceType: req.SourceType,
		Module:     req.Module,
		Version:    req.Version,
		Year:       req.Year,
		Filename:   initialFilename,
	}
	if _, err := ValidateUploadMetadata(initialReq, maxUploadSizeMB); err != nil {
		return UploadResponse{}, err
	}
	if s.deps.HTTPDownloader == nil {
		return UploadResponse{}, errors.New("HTTP downloader is not configured")
	}

	maxBytes := int64(0)
	if maxUploadSizeMB > 0 {
		maxBytes = int64(maxUploadSizeMB) * 1024 * 1024
	}
	result, err := s.deps.HTTPDownloader.Download(ctx, req.URL, maxBytes)
	if err != nil {
		return UploadResponse{}, err
	}
	if result.Body == nil {
		return UploadResponse{}, io.ErrUnexpectedEOF
	}
	defer result.Body.Close()

	filename := strings.TrimSpace(result.Filename)
	if filename == "" {
		filename = initialFilename
	}
	uploadReq := UploadRequest{
		SourceType: req.SourceType,
		Module:     req.Module,
		Version:    req.Version,
		Year:       req.Year,
		Filename:   filename,
		SizeBytes:  result.SizeBytes,
	}
	if _, err := ValidateUploadMetadata(uploadReq, maxUploadSizeMB); err != nil {
		return UploadResponse{}, err
	}

	tempFile, err := os.CreateTemp("", "banner-upload-url-*"+strings.ToLower(filepath.Ext(filename)))
	if err != nil {
		return UploadResponse{}, err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	written, err := copyUploadBody(tempFile, result.Body, maxBytes)
	closeErr := tempFile.Close()
	if err != nil {
		return UploadResponse{}, err
	}
	if closeErr != nil {
		return UploadResponse{}, closeErr
	}
	uploadReq.SizeBytes = written
	if _, err := ValidateUploadMetadata(uploadReq, maxUploadSizeMB); err != nil {
		return UploadResponse{}, err
	}

	return s.CreateUploadFromFile(ctx, uploadReq, tempPath, maxUploadSizeMB)
}

func copyUploadBody(dest io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	var reader io.Reader = src
	if maxBytes > 0 {
		reader = io.LimitReader(src, maxBytes+1)
	}
	written, err := io.Copy(dest, reader)
	if err != nil {
		return written, err
	}
	if maxBytes > 0 && written > maxBytes {
		return written, ErrDownloadTooLarge
	}
	return written, nil
}

func (s *Service) WriteSidecar(ctx context.Context, state SidecarState) error {
	derived, _, err := DeriveSidecarState(state)
	if err != nil {
		return err
	}
	return s.deps.BlobStore.WriteJSON(ctx, SidecarPath(derived.BlobPath), derived)
}

func (s *Service) ReadSidecar(ctx context.Context, blobPath string) (SidecarState, error) {
	var state SidecarState
	if err := s.deps.BlobStore.ReadJSON(ctx, SidecarPath(blobPath), &state); err != nil {
		return SidecarState{}, err
	}
	derived, _, err := DeriveSidecarState(state)
	if err != nil {
		return SidecarState{}, err
	}
	return derived, nil
}

func (s *Service) ListUploads(ctx context.Context, prefix string) ([]UploadSummary, error) {
	blobs, err := s.deps.BlobStore.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	sidecars := make([]BlobInfo, 0, len(blobs))
	for _, blob := range blobs {
		if IsSidecarPath(blob.Name) {
			sidecars = append(sidecars, blob)
		}
	}
	sort.SliceStable(sidecars, func(i, j int) bool {
		return sidecars[i].Name < sidecars[j].Name
	})

	summaries := make([]UploadSummary, 0, len(sidecars))
	for _, blob := range sidecars {
		var state SidecarState
		if err := s.deps.BlobStore.ReadJSON(ctx, blob.Name, &state); err != nil {
			return nil, err
		}
		derived, counts, err := DeriveSidecarState(state)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, UploadSummary{
			UploadID:           derived.UploadID,
			BlobPath:           derived.BlobPath,
			Status:             derived.Status,
			ChunkingPattern:    derived.ChunkingPattern,
			TotalPages:         derived.TotalPages,
			QueryablePageCount: counts.QueryablePageCount,
			GapCount:           derived.GapCount,
			GapSummary:         derived.GapSummary,
		})
	}
	return summaries, nil
}
