package blobstore

// Info holds metadata for a blob object without exposing provider-specific SDK types.
type Info struct {
	Name        string `json:"name"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type"`
}
