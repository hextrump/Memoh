package main

import "testing"

func TestDisplayGeometryDefaultsToFourThree(t *testing.T) {
	t.Setenv(displayGeometryEnv, "")

	if got := displayGeometry(); got != "1280x960" {
		t.Fatalf("displayGeometry() = %q, want 1280x960", got)
	}
}

func TestDisplayGeometryCanBeOverridden(t *testing.T) {
	t.Setenv(displayGeometryEnv, "1440x1080")

	if got := displayGeometry(); got != "1440x1080" {
		t.Fatalf("displayGeometry() = %q, want 1440x1080", got)
	}
}
