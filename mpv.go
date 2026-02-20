//go:build mpv

package main

/*
#cgo !windows pkg-config: mpv
#cgo windows LDFLAGS: -lmpv
#include <mpv/client.h>
#include <mpv/render.h>
#include <stdlib.h>
#include <string.h>

// Callback bridge for render updates
extern void goMpvRenderUpdate(void *ctx);

static void set_render_update_callback(mpv_render_context *ctx) {
    mpv_render_context_set_update_callback(ctx, goMpvRenderUpdate, NULL);
}

static int create_sw_render_context(mpv_render_context **res, mpv_handle *mpv) {
    mpv_render_param params[] = {
        {MPV_RENDER_PARAM_API_TYPE, "sw"},
        {0}
    };
    return mpv_render_context_create(res, mpv, params);
}

static int render_sw(mpv_render_context *ctx, void *data, int w, int h, int stride) {
    // Use a stack-allocated array for render params
    int size[] = {w, h};
    mpv_render_param params[] = {
        {MPV_RENDER_PARAM_SW_SIZE, size},
        {MPV_RENDER_PARAM_SW_FORMAT, "rgb0"},
        {MPV_RENDER_PARAM_SW_STRIDE, &stride},
        {MPV_RENDER_PARAM_SW_POINTER, data},
        {0}
    };
    return mpv_render_context_render(ctx, params);
}
*/
import "C"

import (
	"fmt"
	"image"
	"image/color"
	"sync"
	"unsafe"
)

var (
	mpvRenderMu     sync.Mutex
	mpvNeedsRender  bool
	mpvOnRender     func()
)

//export goMpvRenderUpdate
func goMpvRenderUpdate(ctx unsafe.Pointer) {
	mpvRenderMu.Lock()
	mpvNeedsRender = true
	cb := mpvOnRender
	mpvRenderMu.Unlock()
	if cb != nil {
		cb()
	}
}

// MpvPlayer wraps a libmpv instance with software rendering.
type MpvPlayer struct {
	handle    *C.mpv_handle
	renderCtx *C.mpv_render_context
	mu        sync.Mutex
	frame     *image.RGBA
	width     int
	height    int
	position  float64
	duration  float64
	paused    bool
	onUpdate  func() // called when frame is updated
	done      chan struct{}
}

const mpvAvailable = true

// NewMpvPlayer creates a new mpv player instance.
func NewMpvPlayer() *MpvPlayer {
	return &MpvPlayer{
		paused: true,
	}
}

// Init initializes mpv and the render context.
func (p *MpvPlayer) Init(onUpdate func()) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.onUpdate = onUpdate

	p.handle = C.mpv_create()
	if p.handle == nil {
		return fmt.Errorf("failed to create mpv instance")
	}

	// Set options
	setMpvOption(p.handle, "vo", "libmpv")
	setMpvOption(p.handle, "hwdec", "no")
	setMpvOption(p.handle, "keep-open", "yes")
	setMpvOption(p.handle, "pause", "yes")
	setMpvOption(p.handle, "osc", "no")
	setMpvOption(p.handle, "input-default-bindings", "no")
	setMpvOption(p.handle, "input-vo-keyboard", "no")

	if C.mpv_initialize(p.handle) < 0 {
		C.mpv_destroy(p.handle)
		p.handle = nil
		return fmt.Errorf("failed to initialize mpv")
	}

	// Create software render context
	var renderCtx *C.mpv_render_context
	rc := C.create_sw_render_context(&renderCtx, p.handle)
	if rc < 0 {
		C.mpv_terminate_destroy(p.handle)
		p.handle = nil
		return fmt.Errorf("failed to create mpv render context: %d", rc)
	}
	p.renderCtx = renderCtx

	// Set render update callback
	mpvOnRender = func() {
		if p.onUpdate != nil {
			p.onUpdate()
		}
	}
	C.set_render_update_callback(p.renderCtx)

	// Observe properties
	cTimePos := C.CString("time-pos")
	defer C.free(unsafe.Pointer(cTimePos))
	C.mpv_observe_property(p.handle, 1, cTimePos, C.MPV_FORMAT_DOUBLE)

	cDuration := C.CString("duration")
	defer C.free(unsafe.Pointer(cDuration))
	C.mpv_observe_property(p.handle, 2, cDuration, C.MPV_FORMAT_DOUBLE)

	cPause := C.CString("pause")
	defer C.free(unsafe.Pointer(cPause))
	C.mpv_observe_property(p.handle, 3, cPause, C.MPV_FORMAT_FLAG)

	// Start event loop
	p.done = make(chan struct{})
	go p.eventLoop()

	return nil
}

func setMpvOption(h *C.mpv_handle, key, value string) {
	cKey := C.CString(key)
	cVal := C.CString(value)
	defer C.free(unsafe.Pointer(cKey))
	defer C.free(unsafe.Pointer(cVal))
	C.mpv_set_option_string(h, cKey, cVal)
}

func (p *MpvPlayer) eventLoop() {
	defer close(p.done)
	for {
		// We acquire the handle under the lock, then release before the
		// blocking call to mpv_wait_event (0.1s timeout). This is safe
		// because Cleanup() first sends a "quit" command which triggers
		// MPV_EVENT_SHUTDOWN, causing this loop to exit. Only after
		// waiting on p.done does Cleanup() destroy the handle.
		p.mu.Lock()
		h := p.handle
		p.mu.Unlock()
		if h == nil {
			return
		}
		event := C.mpv_wait_event(h, 0.1)
		if event == nil || event.event_id == C.MPV_EVENT_SHUTDOWN {
			return
		}
		if event.event_id == C.MPV_EVENT_NONE {
			continue
		}
		if event.event_id == C.MPV_EVENT_PROPERTY_CHANGE {
			prop := (*C.mpv_event_property)(event.data)
			name := C.GoString(prop.name)
			switch name {
			case "time-pos":
				if prop.format == C.MPV_FORMAT_DOUBLE && prop.data != nil {
					val := *(*C.double)(prop.data)
					p.mu.Lock()
					p.position = float64(val)
					p.mu.Unlock()
				}
			case "duration":
				if prop.format == C.MPV_FORMAT_DOUBLE && prop.data != nil {
					val := *(*C.double)(prop.data)
					p.mu.Lock()
					p.duration = float64(val)
					p.mu.Unlock()
				}
			case "pause":
				if prop.format == C.MPV_FORMAT_FLAG && prop.data != nil {
					val := *(*C.int)(prop.data)
					p.mu.Lock()
					p.paused = val != 0
					p.mu.Unlock()
				}
			}
		}
	}
}

// LoadVideo loads a video file.
func (p *MpvPlayer) LoadVideo(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == nil {
		return
	}

	cCmd := C.CString("loadfile")
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cCmd))
	defer C.free(unsafe.Pointer(cPath))

	args := []*C.char{cCmd, cPath, nil}
	C.mpv_command(p.handle, &args[0])

	// Ensure paused
	cPause := C.CString("pause")
	cYes := C.CString("yes")
	defer C.free(unsafe.Pointer(cPause))
	defer C.free(unsafe.Pointer(cYes))
	C.mpv_set_property_string(p.handle, cPause, cYes)
}

// RenderFrame renders the current frame into an image.
func (p *MpvPlayer) RenderFrame(w, h int) *image.RGBA {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.renderCtx == nil || w <= 0 || h <= 0 {
		return nil
	}

	stride := w * 4
	buf := make([]byte, stride*h)

	rc := C.render_sw(p.renderCtx, unsafe.Pointer(&buf[0]), C.int(w), C.int(h), C.int(stride))
	if rc < 0 {
		return nil
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Convert rgb0 to RGBA
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := y*stride + x*4
			img.SetRGBA(x, y, color.RGBA{
				R: buf[off],
				G: buf[off+1],
				B: buf[off+2],
				A: 255,
			})
		}
	}

	p.frame = img
	p.width = w
	p.height = h
	return img
}

// Seek seeks to an absolute position.
func (p *MpvPlayer) Seek(pos float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == nil {
		return
	}
	cCmd := C.CString("seek")
	cPos := C.CString(fmt.Sprintf("%f", pos))
	cAbs := C.CString("absolute")
	defer C.free(unsafe.Pointer(cCmd))
	defer C.free(unsafe.Pointer(cPos))
	defer C.free(unsafe.Pointer(cAbs))
	args := []*C.char{cCmd, cPos, cAbs, nil}
	C.mpv_command(p.handle, &args[0])
}

// SeekRelative seeks by a relative offset.
func (p *MpvPlayer) SeekRelative(delta float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == nil {
		return
	}
	cCmd := C.CString("seek")
	cDelta := C.CString(fmt.Sprintf("%f", delta))
	cRel := C.CString("relative+exact")
	defer C.free(unsafe.Pointer(cCmd))
	defer C.free(unsafe.Pointer(cDelta))
	defer C.free(unsafe.Pointer(cRel))
	args := []*C.char{cCmd, cDelta, cRel, nil}
	C.mpv_command(p.handle, &args[0])
}

// FrameStep advances one frame.
func (p *MpvPlayer) FrameStep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == nil {
		return
	}
	cCmd := C.CString("frame-step")
	defer C.free(unsafe.Pointer(cCmd))
	args := []*C.char{cCmd, nil}
	C.mpv_command(p.handle, &args[0])
}

// FrameBackStep goes back one frame.
func (p *MpvPlayer) FrameBackStep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == nil {
		return
	}
	cCmd := C.CString("frame-back-step")
	defer C.free(unsafe.Pointer(cCmd))
	args := []*C.char{cCmd, nil}
	C.mpv_command(p.handle, &args[0])
}

// Play starts playback.
func (p *MpvPlayer) Play() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == nil {
		return
	}
	cPause := C.CString("pause")
	cNo := C.CString("no")
	defer C.free(unsafe.Pointer(cPause))
	defer C.free(unsafe.Pointer(cNo))
	C.mpv_set_property_string(p.handle, cPause, cNo)
}

// Pause pauses playback.
func (p *MpvPlayer) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == nil {
		return
	}
	cPause := C.CString("pause")
	cYes := C.CString("yes")
	defer C.free(unsafe.Pointer(cPause))
	defer C.free(unsafe.Pointer(cYes))
	C.mpv_set_property_string(p.handle, cPause, cYes)
}

// TogglePlay toggles play/pause, returns whether now playing.
func (p *MpvPlayer) TogglePlay() bool {
	p.mu.Lock()
	isPaused := p.paused
	p.mu.Unlock()

	if isPaused {
		p.Play()
		return true
	}
	p.Pause()
	return false
}

// IsPlaying returns whether currently playing.
func (p *MpvPlayer) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.paused
}

// GetPosition returns the current position in seconds.
func (p *MpvPlayer) GetPosition() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.position
}

// GetDuration returns the total duration in seconds.
func (p *MpvPlayer) GetDuration() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.duration
}

// Cleanup releases mpv resources.
func (p *MpvPlayer) Cleanup() {
	p.mu.Lock()
	h := p.handle
	done := p.done
	p.mu.Unlock()

	// Signal mpv to shut down, which causes the event loop to receive
	// MPV_EVENT_SHUTDOWN and exit. We must do this outside the lock
	// because the event loop also acquires the lock.
	if h != nil {
		cQuit := C.CString("quit")
		defer C.free(unsafe.Pointer(cQuit))
		args := []*C.char{cQuit, nil}
		C.mpv_command(h, &args[0])
	}

	// Wait for the event loop goroutine to finish.
	if done != nil {
		<-done
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.renderCtx != nil {
		C.mpv_render_context_free(p.renderCtx)
		p.renderCtx = nil
	}
	if p.handle != nil {
		C.mpv_terminate_destroy(p.handle)
		p.handle = nil
	}
}
