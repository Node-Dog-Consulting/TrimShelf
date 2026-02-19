package main

import (
	"math"
	"testing"
)

func TestComputeKeepRanges(t *testing.T) {
	tests := []struct {
		name     string
		total    float64
		cuts     []cutRegion
		expected []cutRegion
	}{
		{
			name:     "no cuts",
			total:    100,
			cuts:     nil,
			expected: []cutRegion{{0, 100}},
		},
		{
			name:     "cut at start",
			total:    100,
			cuts:     []cutRegion{{0, 20}},
			expected: []cutRegion{{20, 100}},
		},
		{
			name:     "cut at end",
			total:    100,
			cuts:     []cutRegion{{80, 100}},
			expected: []cutRegion{{0, 80}},
		},
		{
			name:     "cut in middle",
			total:    100,
			cuts:     []cutRegion{{30, 60}},
			expected: []cutRegion{{0, 30}, {60, 100}},
		},
		{
			name:     "multiple cuts",
			total:    100,
			cuts:     []cutRegion{{10, 20}, {50, 70}},
			expected: []cutRegion{{0, 10}, {20, 50}, {70, 100}},
		},
		{
			name:     "cut entire duration",
			total:    100,
			cuts:     []cutRegion{{0, 100}},
			expected: nil,
		},
		{
			name:     "unsorted cuts",
			total:    100,
			cuts:     []cutRegion{{50, 70}, {10, 20}},
			expected: []cutRegion{{0, 10}, {20, 50}, {70, 100}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeKeepRanges(tt.total, tt.cuts)
			if len(got) != len(tt.expected) {
				t.Fatalf("got %d ranges, want %d: %v", len(got), len(tt.expected), got)
			}
			for i := range got {
				if got[i].Start != tt.expected[i].Start || got[i].End != tt.expected[i].End {
					t.Errorf("range[%d] = {%v, %v}, want {%v, %v}",
						i, got[i].Start, got[i].End, tt.expected[i].Start, tt.expected[i].End)
				}
			}
		})
	}
}

func TestRemapChapters(t *testing.T) {
	chapters := []trimmerChapter{
		{StartTime: "0.0", EndTime: "30.0", Tags: map[string]string{"title": "Chapter 1"}},
		{StartTime: "30.0", EndTime: "60.0", Tags: map[string]string{"title": "Chapter 2"}},
		{StartTime: "60.0", EndTime: "100.0", Tags: map[string]string{"title": "Chapter 3"}},
	}

	t.Run("no cuts preserves all chapters", func(t *testing.T) {
		keeps := []cutRegion{{0, 100}}
		result := remapChapters(chapters, keeps)
		if len(result) != 3 {
			t.Fatalf("expected 3 chapters, got %d", len(result))
		}
		if result[0].Title != "Chapter 1" || result[0].Start != 0 || result[0].End != 30 {
			t.Errorf("chapter 1: got %+v", result[0])
		}
		if result[2].Title != "Chapter 3" || result[2].Start != 60 || result[2].End != 100 {
			t.Errorf("chapter 3: got %+v", result[2])
		}
	})

	t.Run("cut middle chapter", func(t *testing.T) {
		keeps := []cutRegion{{0, 30}, {60, 100}}
		result := remapChapters(chapters, keeps)
		if len(result) != 2 {
			t.Fatalf("expected 2 chapters, got %d", len(result))
		}
		if result[0].Title != "Chapter 1" || result[0].Start != 0 || result[0].End != 30 {
			t.Errorf("chapter 1: got %+v", result[0])
		}
		// Chapter 3 should be remapped to start at 30 (after chapter 1)
		if result[1].Title != "Chapter 3" || result[1].Start != 30 || result[1].End != 70 {
			t.Errorf("chapter 3: got %+v", result[1])
		}
	})

	t.Run("partial chapter cut", func(t *testing.T) {
		// Cut the second half of chapter 2 (45-60)
		keeps := []cutRegion{{0, 45}, {60, 100}}
		result := remapChapters(chapters, keeps)
		if len(result) != 3 {
			t.Fatalf("expected 3 chapters, got %d", len(result))
		}
		// Chapter 2 should be truncated: was 30-60, kept 30-45, mapped to 30-45
		if result[1].Title != "Chapter 2" {
			t.Errorf("expected Chapter 2, got %q", result[1].Title)
		}
		if math.Abs(result[1].End-result[1].Start-15) > 0.001 {
			t.Errorf("chapter 2 duration should be 15, got %+v", result[1])
		}
	})

	t.Run("invalid chapter timestamps skipped", func(t *testing.T) {
		badChapters := []trimmerChapter{
			{StartTime: "0.0", EndTime: "30.0", Tags: map[string]string{"title": "Good"}},
			{StartTime: "bad", EndTime: "60.0", Tags: map[string]string{"title": "Bad Start"}},
			{StartTime: "60.0", EndTime: "bad", Tags: map[string]string{"title": "Bad End"}},
		}
		keeps := []cutRegion{{0, 100}}
		result := remapChapters(badChapters, keeps)
		if len(result) != 1 {
			t.Fatalf("expected 1 chapter (invalid skipped), got %d", len(result))
		}
		if result[0].Title != "Good" {
			t.Errorf("expected 'Good', got %q", result[0].Title)
		}
	})
}
