package upload

import (
	"context"
	"strings"
)

const (
	estimatedChunksPerPage      = 8
	estimatedSecondsPerChunk    = 0.5
	estimatedSecondsPerPageUnit = int(float64(estimatedChunksPerPage) * estimatedSecondsPerChunk)
)

func (s *Service) UploadStatus(ctx context.Context, uploadID string) (StatusResponse, error) {
	if strings.TrimSpace(uploadID) == "" {
		return StatusResponse{}, ErrMissingUploadID
	}
	if s.deps.BlobStore == nil {
		return StatusResponse{}, ErrUploadNotFound
	}
	state, err := s.findSidecarByUploadID(ctx, uploadID)
	if err != nil {
		return StatusResponse{}, err
	}
	derived, counts, err := DeriveSidecarState(state)
	if err != nil {
		return StatusResponse{}, err
	}
	return statusResponse(derived, counts), nil
}

func statusResponse(state SidecarState, counts SidecarCounts) StatusResponse {
	return StatusResponse{
		SidecarState:              state,
		QueryablePageCount:        counts.QueryablePageCount,
		RemainingPageCount:        counts.RemainingPageCount,
		EstimatedRemainingMinutes: estimateRemainingMinutes(counts.RemainingPageCount),
	}
}

func uploadSummary(state SidecarState, counts SidecarCounts) UploadSummary {
	return UploadSummary{
		UploadID:           state.UploadID,
		BlobPath:           state.BlobPath,
		Status:             state.Status,
		ChunkingPattern:    state.ChunkingPattern,
		TotalPages:         state.TotalPages,
		QueryablePageCount: counts.QueryablePageCount,
		GapCount:           state.GapCount,
		GapSummary:         state.GapSummary,
	}
}

func estimateRemainingMinutes(remainingPages int) int {
	if remainingPages <= 0 {
		return 0
	}
	seconds := remainingPages * estimatedSecondsPerPageUnit
	return (seconds + 59) / 60
}
