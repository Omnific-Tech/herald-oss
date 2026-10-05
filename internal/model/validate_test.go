package model_test

import (
	"testing"

	"github.com/Omnific-Tech/herald/internal/model"
)

func TestSafeLinkPath(t *testing.T) {
	tests := []struct {
		link string
		want bool
	}{
		{link: "", want: true},
		{link: "/", want: true},
		{link: "/events/8f21", want: true},
		{link: "/events?id=8f21#top", want: true},
		{link: "https://evil.example/x", want: false},
		{link: "http://evil.example/x", want: false},
		// Protocol-relative: a browser resolves this against evil.example.
		{link: "//evil.example/x", want: false},
		{link: "//evil.example@good.example/x", want: false},
		// WHATWG normalises the backslash to a slash for special schemes.
		{link: `/\evil.example/x`, want: false},
		{link: `/\/evil.example/x`, want: false},
		{link: "events/8f21", want: false},
		{link: "javascript:alert(1)", want: false},
		{link: "/events\x00/8f21", want: false},
	}

	for _, tt := range tests {
		if got := model.SafeLinkPath(tt.link); got != tt.want {
			t.Errorf("SafeLinkPath(%q) = %v, want %v", tt.link, got, tt.want)
		}
	}
}

func TestSafeWebPushEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"http://push.example/x",
		"https://user@push.example/x",
		"//push.example/x",
		"not a url",
	} {
		if model.SafeWebPushEndpoint(endpoint) {
			t.Errorf("accepted unsafe endpoint %q", endpoint)
		}
	}
	if !model.SafeWebPushEndpoint("https://push.example/x") {
		t.Fatal("rejected https endpoint")
	}
}
