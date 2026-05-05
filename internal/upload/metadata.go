package upload

import (
	"errors"
	"path/filepath"
	"strings"
)

const (
	SourceTypeBanner          = "banner"
	SourceTypeBannerUserGuide = "banner_user_guide"
	SourceTypeSOP             = "sop"
)

var (
	ErrMissingSourceType      = errors.New("source_type is required")
	ErrUnsupportedSourceType  = errors.New("source_type must be banner or banner_user_guide")
	ErrSOPUploadUnsupported   = errors.New("source_type=sop upload is deferred in Phase U")
	ErrMissingModule          = errors.New("module is required for banner uploads")
	ErrUnknownModule          = errors.New("unknown module")
	ErrMissingVersion         = errors.New("version is required for banner release notes")
	ErrMissingYear            = errors.New("year is required for banner release notes")
	ErrUserGuideVersionOrYear = errors.New("version and year must be omitted for banner_user_guide uploads")
	ErrMissingFilename        = errors.New("filename is required")
	ErrUnsupportedExtension   = errors.New("unsupported upload extension")
	ErrNonPDFUpload           = errors.New("Phase U upload accepts PDF files only")
	ErrUploadTooLarge         = errors.New("file exceeds MAX_UPLOAD_SIZE_MB")
)

type ValidatedMetadata struct {
	SourceType string
	Module     string
	Version    string
	Year       string
	Filename   string
	SizeBytes  int64

	moduleFolder string
}

type uploadModule struct {
	display string
	folder  string
}

var knownUploadModules = map[string]uploadModule{
	"finance":             {display: "Finance", folder: "finance"},
	"student":             {display: "Student", folder: "student"},
	"hr":                  {display: "HR", folder: "hr"},
	"human resources":     {display: "Human Resources", folder: "human_resources"},
	"human_resources":     {display: "Human Resources", folder: "human_resources"},
	"financial aid":       {display: "Financial Aid", folder: "financial_aid"},
	"financial_aid":       {display: "Financial Aid", folder: "financial_aid"},
	"general":             {display: "General", folder: "general"},
	"advancement":         {display: "Advancement", folder: "advancement"},
	"payroll":             {display: "Payroll", folder: "payroll"},
	"accounts receivable": {display: "Accounts Receivable", folder: "accounts_receivable"},
	"accounts_receivable": {display: "Accounts Receivable", folder: "accounts_receivable"},
	"position control":    {display: "Position Control", folder: "position_control"},
	"position_control":    {display: "Position Control", folder: "position_control"},
}

func ValidateUploadMetadata(req UploadRequest, maxUploadSizeMB int) (ValidatedMetadata, error) {
	sourceType := strings.TrimSpace(req.SourceType)
	if sourceType == "" {
		return ValidatedMetadata{}, ErrMissingSourceType
	}
	if sourceType == SourceTypeSOP {
		return ValidatedMetadata{}, ErrSOPUploadUnsupported
	}
	if sourceType != SourceTypeBanner && sourceType != SourceTypeBannerUserGuide {
		return ValidatedMetadata{}, ErrUnsupportedSourceType
	}

	filename := filepath.Base(strings.TrimSpace(req.Filename))
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		return ValidatedMetadata{}, ErrMissingFilename
	}
	if err := validateUploadExtension(filename); err != nil {
		return ValidatedMetadata{}, err
	}
	if maxUploadSizeMB > 0 && req.SizeBytes > int64(maxUploadSizeMB)*1024*1024 {
		return ValidatedMetadata{}, ErrUploadTooLarge
	}

	module, moduleFolder, ok := normalizeUploadModule(req.Module)
	if !ok {
		if strings.TrimSpace(req.Module) == "" {
			return ValidatedMetadata{}, ErrMissingModule
		}
		return ValidatedMetadata{}, ErrUnknownModule
	}

	version := strings.TrimSpace(req.Version)
	year := strings.TrimSpace(req.Year)
	switch sourceType {
	case SourceTypeBanner:
		if version == "" {
			return ValidatedMetadata{}, ErrMissingVersion
		}
		if year == "" {
			return ValidatedMetadata{}, ErrMissingYear
		}
	case SourceTypeBannerUserGuide:
		if version != "" || year != "" {
			return ValidatedMetadata{}, ErrUserGuideVersionOrYear
		}
	}

	return ValidatedMetadata{
		SourceType:   sourceType,
		Module:       module,
		Version:      version,
		Year:         year,
		Filename:     filename,
		SizeBytes:    req.SizeBytes,
		moduleFolder: moduleFolder,
	}, nil
}

func SynthesizeBlobPath(meta ValidatedMetadata) string {
	switch meta.SourceType {
	case SourceTypeBannerUserGuide:
		return strings.Join([]string{"banner", meta.moduleFolder, "use", meta.Filename}, "/")
	default:
		return strings.Join([]string{"banner", meta.moduleFolder, "releases", meta.Year, meta.Filename}, "/")
	}
}

func validateUploadExtension(filename string) error {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf":
		return nil
	case ".docx", ".txt", ".md":
		return ErrNonPDFUpload
	default:
		return ErrUnsupportedExtension
	}
}

func normalizeUploadModule(module string) (display string, folder string, ok bool) {
	key := strings.ToLower(strings.TrimSpace(module))
	key = strings.ReplaceAll(key, "-", " ")
	key = strings.Join(strings.Fields(key), " ")
	if module, exists := knownUploadModules[key]; exists {
		return module.display, module.folder, true
	}

	key = strings.ReplaceAll(key, " ", "_")
	if module, exists := knownUploadModules[key]; exists {
		return module.display, module.folder, true
	}
	return "", "", false
}
