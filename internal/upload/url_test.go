package upload

import "testing"

func TestIsAllowedURL_RejectsHTTP(t *testing.T) {
	err := IsAllowedURL("http://customercare.ellucian.com/downloads/Banner.pdf", []string{"customercare.ellucian.com"})
	if err == nil {
		t.Fatal("expected HTTP URL to be rejected")
	}
}

func TestIsAllowedURL_RejectsDisallowedHostname(t *testing.T) {
	err := IsAllowedURL("https://internal.example.com/Banner.pdf", []string{"customercare.ellucian.com", "ellucian.com"})
	if err == nil {
		t.Fatal("expected disallowed hostname to be rejected")
	}
}

func TestIsAllowedURL_AllowsDefaultEllucianDomains(t *testing.T) {
	for _, rawURL := range []string{
		"https://customercare.ellucian.com/downloads/Banner.pdf",
		"https://ellucian.com/docs/Banner.pdf",
	} {
		if err := IsAllowedURL(rawURL, DefaultUploadURLAllowlist); err != nil {
			t.Fatalf("IsAllowedURL(%q): %v", rawURL, err)
		}
	}
}

func TestIsAllowedURL_AllowsWildcardOnlyForHTTPS(t *testing.T) {
	if err := IsAllowedURL("https://internal.example.com/Banner.pdf", []string{"*"}); err != nil {
		t.Fatalf("HTTPS wildcard URL rejected: %v", err)
	}
	if err := IsAllowedURL("http://internal.example.com/Banner.pdf", []string{"*"}); err == nil {
		t.Fatal("expected HTTP wildcard URL to be rejected")
	}
}
