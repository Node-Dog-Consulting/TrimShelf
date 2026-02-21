package main

import (
	"runtime"
	"testing"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0:00"},
		{5, "0:05"},
		{61, "1:01"},
		{3661, "1:01:01"},
		{7200, "2:00:00"},
		{59.9, "0:59"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.input)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatHHMMSS(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0, "00:00:00"},
		{5, "00:00:05"},
		{61, "00:01:01"},
		{3661, "01:01:01"},
		{7200, "02:00:00"},
	}
	for _, tt := range tests {
		got := formatHHMMSS(tt.input)
		if got != tt.want {
			t.Errorf("formatHHMMSS(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatTimeMs(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0, "00:00:00.000"},
		{1500, "00:00:01.500"},
		{61000, "00:01:01.000"},
		{3661500, "01:01:01.500"},
	}
	for _, tt := range tests {
		got := formatTimeMs(tt.input)
		if got != tt.want {
			t.Errorf("formatTimeMs(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEscapeFFMeta(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Simple Title", "Simple Title"},
		{"Title=Value", "Title\\=Value"},
		{"Title;With;Semicolons", "Title\\;With\\;Semicolons"},
		{"Title#Hash", "Title\\#Hash"},
		{"Back\\slash", "Back\\\\slash"},
		{"Line\nBreak", "Line\\\nBreak"},
		{"All=;#\\\n", "All\\=\\;\\#\\\\\\\n"},
	}
	for _, tt := range tests {
		got := escapeFFMeta(tt.input)
		if got != tt.want {
			t.Errorf("escapeFFMeta(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestUriPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		tests := []struct {
			input string
			want  string
		}{
			{"/C:/Users/foo/bar.m4b", `C:\Users\foo\bar.m4b`},
			{"/D:/My Documents/file.epub", `D:\My Documents\file.epub`},
		}
		for _, tt := range tests {
			got := uriPath(tt.input)
			if got != tt.want {
				t.Errorf("uriPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
	} else {
		tests := []struct {
			input string
			want  string
		}{
			{"/home/user/file.m4b", "/home/user/file.m4b"},
			{"/tmp/foo/../bar.m4b", "/tmp/bar.m4b"},
			{"/Users/alice/My File.epub", "/Users/alice/My File.epub"},
		}
		for _, tt := range tests {
			got := uriPath(tt.input)
			if got != tt.want {
				t.Errorf("uriPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
	}
}

func TestEscapeConcatPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/tmp/seg_0001.m4a", "/tmp/seg_0001.m4a"},
		{"/tmp/it's a file.m4a", "/tmp/it'\\''s a file.m4a"},
		{"no quotes here", "no quotes here"},
	}
	for _, tt := range tests {
		got := escapeConcatPath(tt.input)
		if got != tt.want {
			t.Errorf("escapeConcatPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
