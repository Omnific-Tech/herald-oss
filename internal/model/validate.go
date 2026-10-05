package model

import (
	"net/url"
	"strings"
)

// SafeLinkPath reports whether a link is a path on the application's own
// origin.
//
// Checking only the first character is not enough. A browser resolves both
// "//evil.example/x" and "/\evil.example/x" against another origin entirely,
// and both begin with "/".
func SafeLinkPath(link string) bool {
	if link == "" {
		return true
	}
	if !strings.HasPrefix(link, "/") {
		return false
	}
	if len(link) > 1 && (link[1] == '/' || link[1] == '\\') {
		return false
	}
	if strings.ContainsAny(link, "\\\x00") {
		return false
	}

	parsed, err := url.Parse(link)
	if err != nil {
		return false
	}
	return parsed.Scheme == "" && parsed.Host == "" && parsed.Opaque == ""
}

// SafeWebPushEndpoint performs the request-level checks. The dispatcher also
// rejects private resolved addresses at dial time to prevent DNS rebinding.
func SafeWebPushEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil
}
