package main

import (
	"strings"
	"testing"
)

func TestValidBrowserProxy(t *testing.T) {
	for _, v := range []string{"socks5://u:p@host:3000", "http://host:8080", "vless://abc@h:443?type=tcp#x"} {
		if !validBrowserProxy(v) {
			t.Errorf("validBrowserProxy(%q) = false", v)
		}
	}
	for _, v := range []string{"", "socks5://", "ftp://host:21", "host:3000", "socks5://a b@h:1"} {
		if validBrowserProxy(v) {
			t.Errorf("validBrowserProxy(%q) = true", v)
		}
	}
}

func TestMergeFingerprintArgs(t *testing.T) {
	template := []string{"--fingerprint-platform=macos", "--fingerprint-platform-version=15.2.0", "--lang=ja-JP", "--timezone=Asia/Tokyo"}
	got := mergeFingerprintArgs(template, []string{"--fingerprint-platform=macos"})
	if strings.Join(got, " ") != strings.Join(template, " ") {
		t.Fatalf("same platform: %v", got)
	}
	got = mergeFingerprintArgs(template, []string{"--fingerprint-platform=windows", "--fingerprint=42", "--timezone=UTC"})
	want := "--fingerprint-platform=windows --lang=ja-JP --timezone=UTC --fingerprint=42"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}

func TestProvisionOverrideKeepsTemplate(t *testing.T) {
	t.Setenv(antBrowserProvisionJSON, `{"fingerprintArgs":["--fingerprint-platform=macos"]}`)
	req, err := browserProvisionRequestWithOverrides()
	if err != nil {
		t.Fatal(err)
	}
	if req.FingerprintArgs[0] != "--fingerprint-brand=Chrome" || browserFingerprintTemplate[0] != "--fingerprint-brand=Chrome" {
		t.Fatalf("template clobbered: %v / %v", req.FingerprintArgs, browserFingerprintTemplate)
	}
}

func TestProvisionIgnoresLegacyWindowsDefault(t *testing.T) {
	t.Setenv(antBrowserProvisionJSON, legacyDefaultBrowserProvisionJSON)
	req, err := browserProvisionRequestWithOverrides()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(req.FingerprintArgs, " "); got != strings.Join(browserFingerprintTemplate, " ") {
		t.Fatalf("legacy default should keep the template, got %q", got)
	}
}
