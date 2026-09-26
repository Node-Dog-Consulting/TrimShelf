package main

import (
	"testing"
)

func TestVideoKeepSegments(t *testing.T) {
	tests := []struct {
		name     string
		cuts     []videoCut
		duration float64
		want     []videoCut
	}{
		{
			name:     "no cuts keeps everything",
			cuts:     nil,
			duration: 100,
			want:     []videoCut{{Start: 0, End: 100}},
		},
		{
			name:     "cut at start",
			cuts:     []videoCut{{Start: 0, End: 10}},
			duration: 100,
			want:     []videoCut{{Start: 10, End: 100}},
		},
		{
			name:     "cut at end",
			cuts:     []videoCut{{Start: 90, End: 100}},
			duration: 100,
			want:     []videoCut{{Start: 0, End: 90}},
		},
		{
			name:     "cut in middle",
			cuts:     []videoCut{{Start: 30, End: 60}},
			duration: 100,
			want:     []videoCut{{Start: 0, End: 30}, {Start: 60, End: 100}},
		},
		{
			name:     "multiple non-overlapping cuts",
			cuts:     []videoCut{{Start: 10, End: 20}, {Start: 50, End: 60}},
			duration: 100,
			want:     []videoCut{{Start: 0, End: 10}, {Start: 20, End: 50}, {Start: 60, End: 100}},
		},
		{
			name:     "overlapping cuts are merged",
			cuts:     []videoCut{{Start: 10, End: 40}, {Start: 30, End: 60}},
			duration: 100,
			want:     []videoCut{{Start: 0, End: 10}, {Start: 60, End: 100}},
		},
		{
			name:     "adjacent cuts are merged",
			cuts:     []videoCut{{Start: 10, End: 30}, {Start: 30, End: 60}},
			duration: 100,
			want:     []videoCut{{Start: 0, End: 10}, {Start: 60, End: 100}},
		},
		{
			name:     "unsorted cuts are sorted before processing",
			cuts:     []videoCut{{Start: 50, End: 60}, {Start: 10, End: 20}},
			duration: 100,
			want:     []videoCut{{Start: 0, End: 10}, {Start: 20, End: 50}, {Start: 60, End: 100}},
		},
		{
			name:     "cut covers everything returns nothing",
			cuts:     []videoCut{{Start: 0, End: 100}},
			duration: 100,
			want:     nil,
		},
		{
			name:     "tiny remainder below minSegmentGap is dropped",
			cuts:     []videoCut{{Start: 0, End: 99.95}},
			duration: 100,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := videoKeepSegments(tt.cuts, tt.duration)
			if len(got) != len(tt.want) {
				t.Fatalf("videoKeepSegments len = %d, want %d\n  got:  %v\n  want: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range tt.want {
				if got[i].Start != tt.want[i].Start || got[i].End != tt.want[i].End {
					t.Errorf("segment[%d] = {%v, %v}, want {%v, %v}", i, got[i].Start, got[i].End, tt.want[i].Start, tt.want[i].End)
				}
			}
		})
	}
}

func TestSnapSegmentsToKeyframes(t *testing.T) {
	const e = keyframeSeekEpsilon
	keyframes := []float64{0, 10, 20, 30}
	tests := []struct {
		name      string
		keyframes []float64
		segments  []videoCut
		want      []videoCut
	}{
		{
			name:      "start at zero is untouched",
			keyframes: keyframes,
			segments:  []videoCut{{Start: 0, End: 12}},
			want:      []videoCut{{Start: 0, End: 12}},
		},
		{
			name:      "start between keyframes snaps forward",
			keyframes: keyframes,
			segments:  []videoCut{{Start: 0, End: 12}, {Start: 18, End: 40}},
			want:      []videoCut{{Start: 0, End: 12}, {Start: 20 + e, End: 40}},
		},
		{
			name:      "start on a keyframe stays on it",
			keyframes: keyframes,
			segments:  []videoCut{{Start: 10, End: 15}},
			want:      []videoCut{{Start: 10 + e, End: 15}},
		},
		{
			name:      "segment with no keyframe inside is dropped",
			keyframes: keyframes,
			segments:  []videoCut{{Start: 0, End: 5}, {Start: 12, End: 19}, {Start: 25, End: 40}},
			want:      []videoCut{{Start: 0, End: 5}, {Start: 30 + e, End: 40}},
		},
		{
			name:     "no keyframes leaves segments unchanged",
			segments: []videoCut{{Start: 18, End: 40}},
			want:     []videoCut{{Start: 18, End: 40}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := snapSegmentsToKeyframes(tt.segments, tt.keyframes)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d\n  got:  %v\n  want: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("segment[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
