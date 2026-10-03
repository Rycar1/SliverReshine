package api

import "testing"

// The process-identification lookup sends the target's process list to a third
// party. Whether that happens at all is a deployment decision, so the resolution
// of the endpoint is worth pinning: a deployment that opted out must not be
// talked back in by a request body field.
func TestAVLookupEndpointResolution(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		request    string
		wantURL    string
		wantAllow  bool
	}{
		{
			name:       "unset falls back to the built-in default",
			configured: "",
			request:    "",
			wantURL:    "https://av8.de5.net/api.php",
			wantAllow:  true,
		},
		{
			name:       "a request override wins over the default",
			configured: "",
			request:    "https://internal.example/api",
			wantURL:    "https://internal.example/api",
			wantAllow:  true,
		},
		{
			name:       "a deployment URL replaces the default",
			configured: "https://internal.example/api",
			request:    "",
			wantURL:    "https://internal.example/api",
			wantAllow:  true,
		},
		{
			name:       "a request override still wins over a deployment URL",
			configured: "https://internal.example/api",
			request:    "https://other.example/api",
			wantURL:    "https://other.example/api",
			wantAllow:  true,
		},
		{
			name:       "off disables the default",
			configured: "off",
			request:    "",
			wantAllow:  false,
		},
		{
			// The point of the case: opt-out is not reversible from a request.
			name:       "off cannot be overridden by a request",
			configured: "off",
			request:    "https://attacker.example/collect",
			wantAllow:  false,
		},
		{
			name:       "off is matched case-insensitively and with padding",
			configured: "  OFF  ",
			request:    "",
			wantAllow:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			s.SetAVLookupURL(tc.configured)

			gotURL, gotAllow := s.avLookupEndpoint(tc.request)
			if gotAllow != tc.wantAllow {
				t.Fatalf("allowed = %v, want %v", gotAllow, tc.wantAllow)
			}
			if gotAllow && gotURL != tc.wantURL {
				t.Errorf("url = %q, want %q", gotURL, tc.wantURL)
			}
			if !gotAllow && gotURL != "" {
				t.Errorf("url = %q, want empty when the lookup is refused", gotURL)
			}
		})
	}
}

// A disabled deployment must refuse the endpoint rather than quietly using the
// default, which is the whole reason the setting exists.
func TestAVLookupDisabledRefuses(t *testing.T) {
	s := New()
	s.SetAVLookupURL("off")

	if _, allowed := s.avLookupEndpoint(""); allowed {
		t.Fatal("a deployment that set avLookupURL=off still resolved an endpoint")
	}
}
