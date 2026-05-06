package upload

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const DefaultUploadURLAllowlistString = "customercare.ellucian.com,ellucian.com"

var (
	DefaultUploadURLAllowlist = SplitURLAllowlist(DefaultUploadURLAllowlistString)

	ErrMissingURL            = errors.New("url is required")
	ErrInvalidURL            = errors.New("url is invalid")
	ErrURLRequiresHTTPS      = errors.New("url must use https")
	ErrURLHostnameNotAllowed = errors.New("url hostname is not on the allowed list")
	ErrDownloadNotFound      = errors.New("remote URL returned 404")
	ErrDownloadRemoteServer  = errors.New("remote server returned a 5xx error")
	ErrDownloadTimeout       = errors.New("download timed out")
	ErrDownloadTooLarge      = errors.New("downloaded file exceeds MAX_UPLOAD_SIZE_MB")
)

func SplitURLAllowlist(raw string) []string {
	var allowlist []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			allowlist = append(allowlist, item)
		}
	}
	return allowlist
}

func IsAllowedURL(rawURL string, allowlist []string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return ErrInvalidURL
	}
	if parsed.Scheme != "https" {
		return ErrURLRequiresHTTPS
	}

	hostname := strings.ToLower(parsed.Hostname())
	for _, allowed := range allowlist {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "*" || hostname == allowed {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrURLHostnameNotAllowed, hostname)
}

func filenameFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(parsed.EscapedPath())
	if path == "" || path == "/" {
		return ""
	}
	parts := strings.Split(path, "/")
	name, err := url.PathUnescape(parts[len(parts)-1])
	if err != nil {
		return parts[len(parts)-1]
	}
	return name
}
