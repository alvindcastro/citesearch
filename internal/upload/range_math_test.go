package upload

import (
	"reflect"
	"testing"
	"time"
)

func TestComputeUnchunkedRanges_NoneChunked(t *testing.T) {
	got, err := ComputeUnchunkedRanges(120, nil)
	if err != nil {
		t.Fatalf("compute unchunked ranges: %v", err)
	}

	want := []ChunkRange{{Start: 1, End: 120}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unchunked ranges: got %#v, want %#v", got, want)
	}
}

func TestComputeUnchunkedRanges_SequentialPrefix(t *testing.T) {
	got, err := ComputeUnchunkedRanges(120, []ChunkRange{{Start: 1, End: 50}})
	if err != nil {
		t.Fatalf("compute unchunked ranges: %v", err)
	}

	want := []ChunkRange{{Start: 51, End: 120}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unchunked ranges: got %#v, want %#v", got, want)
	}
}

func TestComputeUnchunkedRanges_ContiguousMiddle(t *testing.T) {
	got, err := ComputeUnchunkedRanges(120, []ChunkRange{{Start: 33, End: 44}})
	if err != nil {
		t.Fatalf("compute unchunked ranges: %v", err)
	}

	want := []ChunkRange{{Start: 1, End: 32}, {Start: 45, End: 120}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unchunked ranges: got %#v, want %#v", got, want)
	}
}

func TestComputeUnchunkedRanges_Sparse(t *testing.T) {
	chunkedAt := time.Date(2026, 4, 26, 10, 5, 0, 0, time.UTC)
	got, err := ComputeUnchunkedRanges(120, []ChunkRange{
		{Start: 78, End: 90, ChunkedAt: &chunkedAt, ChunkIDs: []string{"b"}},
		{Start: 33, End: 44, ChunkedAt: &chunkedAt, ChunkIDs: []string{"a"}},
	})
	if err != nil {
		t.Fatalf("compute unchunked ranges: %v", err)
	}

	want := []ChunkRange{{Start: 1, End: 32}, {Start: 45, End: 77}, {Start: 91, End: 120}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unchunked ranges: got %#v, want %#v", got, want)
	}
}

func TestComputeUnchunkedRanges_MergesAdjacentAndOverlappingDefensively(t *testing.T) {
	got, err := ComputeUnchunkedRanges(120, []ChunkRange{
		{Start: 1, End: 10},
		{Start: 11, End: 20},
		{Start: 20, End: 30},
		{Start: 50, End: 60},
	})
	if err != nil {
		t.Fatalf("compute unchunked ranges: %v", err)
	}

	want := []ChunkRange{{Start: 31, End: 49}, {Start: 61, End: 120}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unchunked ranges: got %#v, want %#v", got, want)
	}
}

func TestComputeUnchunkedRanges_RejectsInvalidRanges(t *testing.T) {
	tests := []struct {
		name        string
		totalPages  int
		chunked     []ChunkRange
		wantErrText string
	}{
		{name: "start before first page", totalPages: 120, chunked: []ChunkRange{{Start: 0, End: 10}}, wantErrText: "start page must be at least 1"},
		{name: "end before start", totalPages: 120, chunked: []ChunkRange{{Start: 10, End: 9}}, wantErrText: "end page must be greater than or equal to start page"},
		{name: "end after total", totalPages: 120, chunked: []ChunkRange{{Start: 100, End: 121}}, wantErrText: "end page exceeds total pages"},
		{name: "zero total pages", totalPages: 0, chunked: nil, wantErrText: "total pages must be at least 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ComputeUnchunkedRanges(tt.totalPages, tt.chunked)
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.wantErrText {
				t.Fatalf("error: got %q, want %q", err.Error(), tt.wantErrText)
			}
		})
	}
}

func TestCheckOverlap_DetectsContainmentPartialAndDuplicate(t *testing.T) {
	existing := []ChunkRange{{Start: 10, End: 20}, {Start: 40, End: 50}}

	tests := []struct {
		name       string
		requested  ChunkRange
		wantErr    bool
		wantErrMsg string
	}{
		{name: "contained by existing", requested: ChunkRange{Start: 12, End: 18}, wantErr: true, wantErrMsg: "requested page range overlaps an already chunked range"},
		{name: "contains existing", requested: ChunkRange{Start: 5, End: 25}, wantErr: true, wantErrMsg: "requested page range overlaps an already chunked range"},
		{name: "partial left overlap", requested: ChunkRange{Start: 5, End: 10}, wantErr: true, wantErrMsg: "requested page range overlaps an already chunked range"},
		{name: "partial right overlap", requested: ChunkRange{Start: 20, End: 30}, wantErr: true, wantErrMsg: "requested page range overlaps an already chunked range"},
		{name: "duplicate", requested: ChunkRange{Start: 10, End: 20}, wantErr: true, wantErrMsg: "requested page range overlaps an already chunked range"},
		{name: "adjacent before", requested: ChunkRange{Start: 1, End: 9}},
		{name: "adjacent after", requested: ChunkRange{Start: 21, End: 39}},
		{name: "after all", requested: ChunkRange{Start: 51, End: 60}},
		{name: "invalid requested", requested: ChunkRange{Start: 0, End: 1}, wantErr: true, wantErrMsg: "start page must be at least 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckOverlap(120, existing, tt.requested)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if err.Error() != tt.wantErrMsg {
					t.Fatalf("error: got %q, want %q", err.Error(), tt.wantErrMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestComputePattern_NoneSequentialContiguousSparse(t *testing.T) {
	tests := []struct {
		name    string
		chunked []ChunkRange
		want    string
	}{
		{name: "none", chunked: nil, want: PatternNone},
		{name: "sequential prefix", chunked: []ChunkRange{{Start: 1, End: 50}}, want: PatternSequential},
		{name: "sequential adjacent", chunked: []ChunkRange{{Start: 1, End: 50}, {Start: 51, End: 120}}, want: PatternSequential},
		{name: "contiguous middle", chunked: []ChunkRange{{Start: 33, End: 44}}, want: PatternContiguous},
		{name: "contiguous out of order adjacent", chunked: []ChunkRange{{Start: 45, End: 90}, {Start: 33, End: 44}}, want: PatternContiguous},
		{name: "sparse", chunked: []ChunkRange{{Start: 33, End: 44}, {Start: 78, End: 90}}, want: PatternSparse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ComputePattern(120, tt.chunked)
			if err != nil {
				t.Fatalf("compute pattern: %v", err)
			}
			if got != tt.want {
				t.Fatalf("pattern: got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGapSummary_FormatsSingleMultipleAndFullyIndexed(t *testing.T) {
	tests := []struct {
		name string
		gaps []ChunkRange
		want string
	}{
		{name: "fully indexed", gaps: nil, want: "fully indexed"},
		{name: "single", gaps: []ChunkRange{{Start: 1, End: 120}}, want: "1 gap: pages 1-120 (120 pages total unchunked)"},
		{name: "multiple", gaps: []ChunkRange{{Start: 1, End: 32}, {Start: 45, End: 77}, {Start: 91, End: 120}}, want: "3 gaps: pages 1-32, 45-77, 91-120 (95 pages total unchunked)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GapSummary(tt.gaps)
			if got != tt.want {
				t.Fatalf("summary: got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveSidecarFields_RecomputesStatusGapsAndCounts(t *testing.T) {
	state := SidecarState{
		TotalPages:      120,
		ChunkedRanges:   []ChunkRange{{Start: 78, End: 90}, {Start: 33, End: 44}},
		UnchunkedRanges: []ChunkRange{{Start: 1, End: 1}},
		Status:          StatusComplete,
		ChunkingPattern: PatternSequential,
		GapCount:        99,
		GapSummary:      "caller supplied",
	}

	got, counts, err := DeriveSidecarState(state)
	if err != nil {
		t.Fatalf("derive sidecar state: %v", err)
	}

	wantGaps := []ChunkRange{{Start: 1, End: 32}, {Start: 45, End: 77}, {Start: 91, End: 120}}
	if !reflect.DeepEqual(got.UnchunkedRanges, wantGaps) {
		t.Fatalf("unchunked ranges: got %#v, want %#v", got.UnchunkedRanges, wantGaps)
	}
	if got.Status != StatusPartial {
		t.Fatalf("status: got %q, want %q", got.Status, StatusPartial)
	}
	if got.ChunkingPattern != PatternSparse {
		t.Fatalf("pattern: got %q, want %q", got.ChunkingPattern, PatternSparse)
	}
	if got.GapCount != 3 {
		t.Fatalf("gap count: got %d, want %d", got.GapCount, 3)
	}
	if got.GapSummary != "3 gaps: pages 1-32, 45-77, 91-120 (95 pages total unchunked)" {
		t.Fatalf("gap summary: got %q", got.GapSummary)
	}
	if counts.QueryablePageCount != 25 {
		t.Fatalf("queryable page count: got %d, want %d", counts.QueryablePageCount, 25)
	}
	if counts.RemainingPageCount != 95 {
		t.Fatalf("remaining page count: got %d, want %d", counts.RemainingPageCount, 95)
	}
}
