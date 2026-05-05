package config

import "testing"

func TestLoad_MaxUploadSizeMBDefaultsTo100(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MAX_UPLOAD_SIZE_MB", "")

	cfg := Load()
	if cfg.MaxUploadSizeMB != 100 {
		t.Fatalf("MaxUploadSizeMB: got %d, want 100", cfg.MaxUploadSizeMB)
	}
}

func TestLoad_MaxUploadSizeMBCanBeOverridden(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MAX_UPLOAD_SIZE_MB", "250")

	cfg := Load()
	if cfg.MaxUploadSizeMB != 250 {
		t.Fatalf("MaxUploadSizeMB: got %d, want 250", cfg.MaxUploadSizeMB)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://example.openai.azure.com")
	t.Setenv("AZURE_OPENAI_API_KEY", "test-openai-key")
	t.Setenv("AZURE_SEARCH_ENDPOINT", "https://example.search.windows.net")
	t.Setenv("AZURE_SEARCH_API_KEY", "test-search-key")
}
