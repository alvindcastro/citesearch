package upload

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrInvalidTotalPages = errors.New("total pages must be at least 1")
	ErrRangeStart        = errors.New("start page must be at least 1")
	ErrRangeOrder        = errors.New("end page must be greater than or equal to start page")
	ErrRangeEnd          = errors.New("end page exceeds total pages")
	ErrRangeOverlap      = errors.New("requested page range overlaps an already chunked range")
)

type SidecarCounts struct {
	QueryablePageCount int
	RemainingPageCount int
}

func ComputeUnchunkedRanges(totalPages int, chunkedRanges []ChunkRange) ([]ChunkRange, error) {
	merged, err := mergedChunkedRanges(totalPages, chunkedRanges)
	if err != nil {
		return nil, err
	}
	if len(merged) == 0 {
		return []ChunkRange{{Start: 1, End: totalPages}}, nil
	}

	gaps := make([]ChunkRange, 0, len(merged)+1)
	nextStart := 1
	for _, r := range merged {
		if nextStart < r.Start {
			gaps = append(gaps, ChunkRange{Start: nextStart, End: r.Start - 1})
		}
		nextStart = r.End + 1
	}
	if nextStart <= totalPages {
		gaps = append(gaps, ChunkRange{Start: nextStart, End: totalPages})
	}
	return gaps, nil
}

func CheckOverlap(totalPages int, existing []ChunkRange, requested ChunkRange) error {
	if err := validateRange(totalPages, requested); err != nil {
		return err
	}
	for _, r := range existing {
		if err := validateRange(totalPages, r); err != nil {
			return err
		}
		if requested.Start <= r.End && requested.End >= r.Start {
			return ErrRangeOverlap
		}
	}
	return nil
}

func ComputePattern(totalPages int, chunkedRanges []ChunkRange) (string, error) {
	merged, err := mergedChunkedRanges(totalPages, chunkedRanges)
	if err != nil {
		return "", err
	}
	if len(merged) == 0 {
		return PatternNone, nil
	}
	if len(merged) == 1 {
		if merged[0].Start == 1 {
			return PatternSequential, nil
		}
		return PatternContiguous, nil
	}
	return PatternSparse, nil
}

func GapSummary(gaps []ChunkRange) string {
	if len(gaps) == 0 {
		return "fully indexed"
	}

	parts := make([]string, 0, len(gaps))
	total := 0
	for _, gap := range gaps {
		parts = append(parts, fmt.Sprintf("%d-%d", gap.Start, gap.End))
		total += rangeLength(gap)
	}

	label := "gap"
	if len(gaps) != 1 {
		label = "gaps"
	}
	return fmt.Sprintf("%d %s: pages %s (%d pages total unchunked)", len(gaps), label, strings.Join(parts, ", "), total)
}

func DeriveSidecarState(state SidecarState) (SidecarState, SidecarCounts, error) {
	merged, err := mergedChunkedRanges(state.TotalPages, state.ChunkedRanges)
	if err != nil {
		return SidecarState{}, SidecarCounts{}, err
	}
	gaps, err := ComputeUnchunkedRanges(state.TotalPages, state.ChunkedRanges)
	if err != nil {
		return SidecarState{}, SidecarCounts{}, err
	}
	pattern, err := ComputePattern(state.TotalPages, state.ChunkedRanges)
	if err != nil {
		return SidecarState{}, SidecarCounts{}, err
	}

	counts := SidecarCounts{
		QueryablePageCount: pageCount(merged),
		RemainingPageCount: pageCount(gaps),
	}

	state.ChunkedRanges = sortChunkedRanges(state.ChunkedRanges)
	state.UnchunkedRanges = gaps
	state.Status = deriveStatus(counts)
	state.ChunkingPattern = pattern
	state.GapCount = len(gaps)
	state.GapSummary = GapSummary(gaps)
	return state, counts, nil
}

func deriveStatus(counts SidecarCounts) string {
	if counts.QueryablePageCount == 0 {
		return StatusPending
	}
	if counts.RemainingPageCount == 0 {
		return StatusComplete
	}
	return StatusPartial
}

func mergedChunkedRanges(totalPages int, ranges []ChunkRange) ([]ChunkRange, error) {
	if totalPages < 1 {
		return nil, ErrInvalidTotalPages
	}
	sorted := sortChunkedRanges(ranges)
	merged := make([]ChunkRange, 0, len(sorted))
	for _, r := range sorted {
		if err := validateRange(totalPages, r); err != nil {
			return nil, err
		}
		if len(merged) == 0 {
			merged = append(merged, ChunkRange{Start: r.Start, End: r.End})
			continue
		}
		last := &merged[len(merged)-1]
		if r.Start <= last.End+1 {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		merged = append(merged, ChunkRange{Start: r.Start, End: r.End})
	}
	return merged, nil
}

func sortChunkedRanges(ranges []ChunkRange) []ChunkRange {
	sorted := append([]ChunkRange(nil), ranges...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Start == sorted[j].Start {
			return sorted[i].End < sorted[j].End
		}
		return sorted[i].Start < sorted[j].Start
	})
	return sorted
}

func validateRange(totalPages int, r ChunkRange) error {
	if totalPages < 1 {
		return ErrInvalidTotalPages
	}
	if r.Start < 1 {
		return ErrRangeStart
	}
	if r.End < r.Start {
		return ErrRangeOrder
	}
	if r.End > totalPages {
		return ErrRangeEnd
	}
	return nil
}

func pageCount(ranges []ChunkRange) int {
	total := 0
	for _, r := range ranges {
		total += rangeLength(r)
	}
	return total
}

func rangeLength(r ChunkRange) int {
	return r.End - r.Start + 1
}
