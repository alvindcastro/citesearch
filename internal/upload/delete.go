package upload

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrPurgeNotImplemented = errors.New("purge_index=true is not implemented until uploaded chunk IDs are reliably persisted")

func (s *Service) DeleteUpload(ctx context.Context, req DeleteRequest) (DeleteResponse, error) {
	uploadID := strings.TrimSpace(req.UploadID)
	if uploadID == "" {
		return DeleteResponse{}, ErrMissingUploadID
	}
	if s.deps.BlobStore == nil {
		return DeleteResponse{}, ErrUploadNotFound
	}
	if req.PurgeIndex {
		return DeleteResponse{}, ErrPurgeNotImplemented
	}

	state, err := s.findSidecarByUploadID(ctx, uploadID)
	if err != nil {
		return DeleteResponse{}, err
	}

	if err := s.deps.BlobStore.Delete(ctx, state.BlobPath); err != nil {
		return DeleteResponse{}, fmt.Errorf("delete upload blob: %w", err)
	}
	if err := s.deps.BlobStore.Delete(ctx, SidecarPath(state.BlobPath)); err != nil {
		return DeleteResponse{
			UploadID:    uploadID,
			BlobDeleted: true,
		}, fmt.Errorf("delete upload sidecar after deleting blob: %w", err)
	}

	return DeleteResponse{
		UploadID:       uploadID,
		BlobDeleted:    true,
		SidecarDeleted: true,
		ChunksPurged:   false,
	}, nil
}
