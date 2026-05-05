package upload

import (
	"context"
	"io"
	"sort"
	"strings"
)

// Service is the upload package entry point. Phase U.1 stores dependencies only;
// later phases add upload, chunk, status, list, and delete behavior.
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
