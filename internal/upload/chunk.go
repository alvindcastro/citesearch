package upload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrMissingUploadID     = errors.New("upload_id is required")
	ErrUploadNotFound      = errors.New("upload not found")
	ErrIncompletePageRange = errors.New("page_start and page_end must be provided together")
	ErrIngestNotConfigured = errors.New("ingest runner is not configured")
	ErrChunkAlreadyRunning = errors.New("chunk run already active for upload_id")
)

func (s *Service) ChunkUpload(ctx context.Context, req ChunkRequest, defaultPagesPerBatch int) (ChunkResponse, error) {
	if strings.TrimSpace(req.UploadID) == "" {
		return ChunkResponse{}, ErrMissingUploadID
	}
	if s.deps.BlobStore == nil {
		return ChunkResponse{}, ErrUploadNotFound
	}
	if s.deps.IngestRunner == nil {
		return ChunkResponse{}, ErrIngestNotConfigured
	}
	release, acquired := s.deps.ChunkLocks.TryLock(req.UploadID)
	if !acquired {
		return ChunkResponse{}, ErrChunkAlreadyRunning
	}
	defer release()

	state, err := s.findSidecarByUploadID(ctx, req.UploadID)
	if err != nil {
		return ChunkResponse{}, err
	}
	derived, _, err := DeriveSidecarState(state)
	if err != nil {
		return ChunkResponse{}, err
	}
	targets, err := chunkTargets(derived, req)
	if err != nil {
		return ChunkResponse{}, err
	}
	exists, err := s.deps.BlobStore.Exists(ctx, derived.BlobPath)
	if err != nil {
		return ChunkResponse{}, err
	}
	if !exists {
		return ChunkResponse{}, ErrUploadNotFound
	}
	if len(targets) == 0 {
		return chunkResponse(derived, 0, 0, 0), nil
	}

	localPath, cleanup, err := s.downloadToTempFile(ctx, derived.BlobPath)
	if err != nil {
		return ChunkResponse{}, err
	}
	defer cleanup()

	pagesPerBatch := req.PagesPerBatch
	if pagesPerBatch == 0 {
		pagesPerBatch = defaultPagesPerBatch
	}
	if pagesPerBatch == 0 {
		pagesPerBatch = 10
	}

	pagesChunked := 0
	chunksIndexed := 0
	gapsProcessed := 0
	current := derived
	for _, target := range targets {
		result, err := s.deps.IngestRunner.Run(ctx, IngestRequest{
			BlobPath:      current.BlobPath,
			LocalPath:     localPath,
			PagesPerBatch: pagesPerBatch,
			StartPage:     target.Start,
			EndPage:       target.End,
		})
		if err != nil {
			return ChunkResponse{}, err
		}

		chunkedAt := s.deps.Clock.Now()
		current.ChunkedRanges = append(current.ChunkedRanges, ChunkRange{
			Start:     target.Start,
			End:       target.End,
			ChunkedAt: &chunkedAt,
			ChunkIDs:  result.ChunkIDs,
		})
		current, _, err = DeriveSidecarState(current)
		if err != nil {
			return ChunkResponse{}, err
		}
		if err := s.WriteSidecar(ctx, current); err != nil {
			return ChunkResponse{}, err
		}

		pagesChunked += rangeLength(target)
		chunksIndexed += result.ChunksIndexed
		gapsProcessed++
	}

	return chunkResponse(current, pagesChunked, chunksIndexed, gapsProcessed), nil
}

func (s *Service) findSidecarByUploadID(ctx context.Context, uploadID string) (SidecarState, error) {
	blobs, err := s.deps.BlobStore.List(ctx, "")
	if err != nil {
		return SidecarState{}, err
	}
	for _, blob := range blobs {
		if !IsSidecarPath(blob.Name) {
			continue
		}
		var state SidecarState
		if err := s.deps.BlobStore.ReadJSON(ctx, blob.Name, &state); err != nil {
			return SidecarState{}, err
		}
		if state.UploadID != uploadID {
			continue
		}
		derived, _, err := DeriveSidecarState(state)
		if err != nil {
			return SidecarState{}, err
		}
		return derived, nil
	}
	return SidecarState{}, ErrUploadNotFound
}

func chunkTargets(state SidecarState, req ChunkRequest) ([]ChunkRange, error) {
	hasStart := req.PageStart != 0
	hasEnd := req.PageEnd != 0
	if hasStart != hasEnd {
		return nil, ErrIncompletePageRange
	}
	if !hasStart {
		return append([]ChunkRange(nil), state.UnchunkedRanges...), nil
	}
	requested := ChunkRange{Start: req.PageStart, End: req.PageEnd}
	if err := CheckOverlap(state.TotalPages, state.ChunkedRanges, requested); err != nil {
		return nil, err
	}
	return []ChunkRange{requested}, nil
}

func (s *Service) downloadToTempFile(ctx context.Context, blobPath string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "banner-upload-chunk-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	cleanBlobPath := filepath.Clean(blobPath)
	if strings.HasPrefix(cleanBlobPath, "..") || filepath.IsAbs(cleanBlobPath) {
		cleanup()
		return "", func() {}, fmt.Errorf("invalid blob path %q", blobPath)
	}
	localPath := filepath.Join(tempDir, cleanBlobPath)
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		cleanup()
		return "", func() {}, err
	}
	tempFile, err := os.Create(localPath)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}

	if err := s.deps.BlobStore.Download(ctx, blobPath, tempFile); err != nil {
		_ = tempFile.Close()
		cleanup()
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return "", func() {}, ErrUploadNotFound
		}
		return "", func() {}, fmt.Errorf("download upload blob: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return localPath, cleanup, nil
}

func chunkResponse(state SidecarState, pagesChunked, chunksIndexed, gapsProcessed int) ChunkResponse {
	return ChunkResponse{
		SidecarState:  state,
		PagesChunked:  pagesChunked,
		ChunksIndexed: chunksIndexed,
		GapsProcessed: gapsProcessed,
		GapsRemaining: 0,
	}
}
