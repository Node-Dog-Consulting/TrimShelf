package main

import (
	"runtime"
	"testing"
)

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"1.2.3", []int{1, 2, 3}},
		{"0.0.1", []int{0, 0, 1}},
		{"10.20.30", []int{10, 20, 30}},
		{"1.0", []int{1, 0}},
		{"2", []int{2}},
		{"", []int{0}},
	}
	for _, tt := range tests {
		got := parseSemver(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseSemver(%q) len = %d, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Errorf("parseSemver(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.1.0", true},
		{"1.0.0", "2.0.0", true},
		{"1.0.1", "1.0.0", false},
		{"1.0.0", "1.0.0", false},
		{"2.0.0", "1.9.9", false},
		// v-prefix stripped
		{"v1.0.0", "v1.0.1", true},
		{"v1.2.3", "v1.2.3", false},
		// dev builds are never outdated
		{"dev", "9.9.9", false},
		{"dev-abc123", "9.9.9", false},
		// minor version bump
		{"1.2.0", "1.2.1", true},
		{"1.2.1", "1.2.0", false},
	}
	for _, tt := range tests {
		got := isNewerVersion(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestPlatformAsset(t *testing.T) {
	release := &githubRelease{
		Assets: []githubAsset{
			{Name: "TrimShelf.dmg"},
			{Name: "TrimShelf-Setup.exe"},
			{Name: "TrimShelf-Linux-amd64.zip"},
		},
	}

	asset := platformAsset(release)

	switch runtime.GOOS {
	case "darwin":
		if asset == nil || asset.Name != "TrimShelf.dmg" {
			t.Errorf("platformAsset on darwin = %v, want TrimShelf.dmg", asset)
		}
	case "windows":
		if asset == nil || asset.Name != "TrimShelf-Setup.exe" {
			t.Errorf("platformAsset on windows = %v, want TrimShelf-Setup.exe", asset)
		}
	case "linux":
		if asset == nil || asset.Name != "TrimShelf-Linux-amd64.zip" {
			t.Errorf("platformAsset on linux = %v, want TrimShelf-Linux-amd64.zip", asset)
		}
	}
}

func TestPlatformAssetMissing(t *testing.T) {
	// A release with no matching asset returns nil.
	release := &githubRelease{
		Assets: []githubAsset{
			{Name: "SomeOtherFile.tar.gz"},
		},
	}
	if got := platformAsset(release); got != nil {
		t.Errorf("platformAsset with no match = %v, want nil", got)
	}
}
