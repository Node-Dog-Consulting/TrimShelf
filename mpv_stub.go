//go:build !mpv

package main

import "image"

const mpvAvailable = false

// MpvPlayer stub — used when built without mpv support.
type MpvPlayer struct {
	position float64
	duration float64
}

func NewMpvPlayer() *MpvPlayer { return &MpvPlayer{} }

func (p *MpvPlayer) Init(onUpdate func()) error { return nil }
func (p *MpvPlayer) LoadVideo(path string)       {}
func (p *MpvPlayer) RenderFrame(w, h int) *image.RGBA { return nil }
func (p *MpvPlayer) Seek(pos float64)            {}
func (p *MpvPlayer) SeekRelative(delta float64)  {}
func (p *MpvPlayer) FrameStep()                  {}
func (p *MpvPlayer) FrameBackStep()              {}
func (p *MpvPlayer) Play()                       {}
func (p *MpvPlayer) Pause()                      {}
func (p *MpvPlayer) TogglePlay() bool            { return false }
func (p *MpvPlayer) IsPlaying() bool             { return false }
func (p *MpvPlayer) GetPosition() float64        { return p.position }
func (p *MpvPlayer) GetDuration() float64        { return p.duration }
func (p *MpvPlayer) Cleanup()                    {}
