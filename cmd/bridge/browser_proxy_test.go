package main

import "testing"

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
