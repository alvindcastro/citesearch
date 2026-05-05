package upload

import (
	"errors"
	"testing"
)

func TestSynthesizeBlobPath_BannerReleaseWithYear(t *testing.T) {
	meta, err := ValidateUploadMetadata(UploadRequest{
		SourceType: "banner",
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		Filename:   "Banner_Finance_9.3.22.pdf",
		SizeBytes:  42,
	}, 100)
	if err != nil {
		t.Fatalf("validate metadata: %v", err)
	}
	if meta.Module != "Finance" {
		t.Fatalf("module: got %q, want %q", meta.Module, "Finance")
	}

	got := SynthesizeBlobPath(meta)
	want := "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf"
	if got != want {
		t.Fatalf("blob path: got %q, want %q", got, want)
	}
}

func TestSynthesizeBlobPath_BannerUserGuide(t *testing.T) {
	meta, err := ValidateUploadMetadata(UploadRequest{
		SourceType: "banner_user_guide",
		Module:     "Student",
		Filename:   "Banner_Student_User_Guide.pdf",
		SizeBytes:  42,
	}, 100)
	if err != nil {
		t.Fatalf("validate metadata: %v", err)
	}

	got := SynthesizeBlobPath(meta)
	want := "banner/student/use/Banner_Student_User_Guide.pdf"
	if got != want {
		t.Fatalf("blob path: got %q, want %q", got, want)
	}
}

func TestValidateUploadMetadata_RejectsMissingSourceType(t *testing.T) {
	_, err := ValidateUploadMetadata(UploadRequest{
		Module:    "Finance",
		Version:   "9.3.22",
		Year:      "2026",
		Filename:  "Banner_Finance_9.3.22.pdf",
		SizeBytes: 42,
	}, 100)
	if !errors.Is(err, ErrMissingSourceType) {
		t.Fatalf("error: got %v, want %v", err, ErrMissingSourceType)
	}
}

func TestValidateUploadMetadata_RejectsMissingModuleForBanner(t *testing.T) {
	_, err := ValidateUploadMetadata(UploadRequest{
		SourceType: "banner",
		Version:    "9.3.22",
		Year:       "2026",
		Filename:   "Banner_Finance_9.3.22.pdf",
		SizeBytes:  42,
	}, 100)
	if !errors.Is(err, ErrMissingModule) {
		t.Fatalf("error: got %v, want %v", err, ErrMissingModule)
	}
}

func TestValidateUploadMetadata_RejectsVersionOrYearForUserGuide(t *testing.T) {
	for _, req := range []UploadRequest{
		{SourceType: "banner_user_guide", Module: "Finance", Version: "9.3.22", Filename: "Banner_Finance_User_Guide.pdf", SizeBytes: 42},
		{SourceType: "banner_user_guide", Module: "Finance", Year: "2026", Filename: "Banner_Finance_User_Guide.pdf", SizeBytes: 42},
	} {
		_, err := ValidateUploadMetadata(req, 100)
		if !errors.Is(err, ErrUserGuideVersionOrYear) {
			t.Fatalf("error for %+v: got %v, want %v", req, err, ErrUserGuideVersionOrYear)
		}
	}
}

func TestValidateUploadMetadata_RejectsUnknownModule(t *testing.T) {
	_, err := ValidateUploadMetadata(UploadRequest{
		SourceType: "banner",
		Module:     "Library",
		Version:    "9.3.22",
		Year:       "2026",
		Filename:   "Banner_Library_9.3.22.pdf",
		SizeBytes:  42,
	}, 100)
	if !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("error: got %v, want %v", err, ErrUnknownModule)
	}
}

func TestValidateUploadMetadata_RejectsUnsupportedExtension(t *testing.T) {
	_, err := ValidateUploadMetadata(UploadRequest{
		SourceType: "banner",
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		Filename:   "Banner_Finance_9.3.22.exe",
		SizeBytes:  42,
	}, 100)
	if !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("error: got %v, want %v", err, ErrUnsupportedExtension)
	}
}

func TestValidateUploadMetadata_RejectsSOPUploadInPhaseU(t *testing.T) {
	_, err := ValidateUploadMetadata(UploadRequest{
		SourceType: "sop",
		Filename:   "SOP122 - Smoke Test.docx",
		SizeBytes:  42,
	}, 100)
	if !errors.Is(err, ErrSOPUploadUnsupported) {
		t.Fatalf("error: got %v, want %v", err, ErrSOPUploadUnsupported)
	}
}

func TestValidateUploadMetadata_RejectsNonPDFUploadExtension(t *testing.T) {
	for _, filename := range []string{"Banner_Finance_9.3.22.docx", "Banner_Finance_9.3.22.txt", "Banner_Finance_9.3.22.md"} {
		_, err := ValidateUploadMetadata(UploadRequest{
			SourceType: "banner",
			Module:     "Finance",
			Version:    "9.3.22",
			Year:       "2026",
			Filename:   filename,
			SizeBytes:  42,
		}, 100)
		if !errors.Is(err, ErrNonPDFUpload) {
			t.Fatalf("error for %s: got %v, want %v", filename, err, ErrNonPDFUpload)
		}
	}
}

func TestValidateUploadMetadata_RejectsOversizeUpload(t *testing.T) {
	_, err := ValidateUploadMetadata(UploadRequest{
		SourceType: "banner",
		Module:     "Finance",
		Version:    "9.3.22",
		Year:       "2026",
		Filename:   "Banner_Finance_9.3.22.pdf",
		SizeBytes:  101 * 1024 * 1024,
	}, 100)
	if !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("error: got %v, want %v", err, ErrUploadTooLarge)
	}
}
