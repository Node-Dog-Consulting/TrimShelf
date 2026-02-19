package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

const (
	// minSegmentGap is the minimum duration (in seconds) between cut regions
	// for the gap to be considered a meaningful keep-segment.
	minSegmentGap = 0.1

	// cutAddedMessageDuration is how long the "Cut added!" confirmation
	// label is shown before reverting to the default text.
	cutAddedMessageDuration = 1500 * time.Millisecond
)

type videoCut struct {
	Start float64
	End   float64
}

// repeatButton is a button that fires repeatedly when held down.
type repeatButton struct {
	widget.Button
	onPress func()
	mu      sync.Mutex
	stopCh  chan struct{}
}

func newRepeatButton(label string, action func()) *repeatButton {
	b := &repeatButton{onPress: action}
	b.Text = label
	b.OnTapped = action
	b.ExtendBaseWidget(b)
	return b
}

func (b *repeatButton) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button != desktop.MouseButtonPrimary {
		return
	}
	b.mu.Lock()
	b.stopCh = make(chan struct{})
	ch := b.stopCh
	b.mu.Unlock()

	go func() {
		delay := time.NewTimer(500 * time.Millisecond)
		defer delay.Stop()
		select {
		case <-ch:
			return
		case <-delay.C:
		}
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ch:
				return
			case <-ticker.C:
				b.onPress()
			}
		}
	}()
}

func (b *repeatButton) MouseUp(ev *desktop.MouseEvent) {
	b.mu.Lock()
	if b.stopCh != nil {
		close(b.stopCh)
		b.stopCh = nil
	}
	b.mu.Unlock()
}

func detectGPUEncoder() (encoder string, name string) {
	cmd := exec.Command(findFFmpeg(), "-encoders")
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	s := string(out)
	if strings.Contains(s, "h264_videotoolbox") {
		return "h264_videotoolbox", "Apple VideoToolbox"
	}
	if strings.Contains(s, "h264_nvenc") {
		return "h264_nvenc", "NVIDIA NVENC"
	}
	if strings.Contains(s, "h264_amf") {
		return "h264_amf", "AMD AMF"
	}
	if strings.Contains(s, "h264_qsv") {
		return "h264_qsv", "Intel QuickSync"
	}
	return "", ""
}

func ShowVideoTrimmer(a fyne.App, picker fyne.Window) {
	if !mpvAvailable {
		dialog.ShowError(fmt.Errorf("video trimmer requires mpv support.\nBuild with: go build -tags mpv"), picker)
		picker.Show()
		return
	}
	if err := checkFFmpeg(); err != nil {
		dialog.ShowError(err, picker)
		picker.Show()
		return
	}

	w := a.NewWindow("Video Trimmer")
	w.Resize(fyne.NewSize(900, 700))

	var videoPath string
	var durationSec float64
	var cuts []videoCut
	var markInTime *float64
	var selectedCut int = -1
	stopCh := make(chan struct{})

	player := NewMpvPlayer()

	// Video preview raster
	videoRaster := canvas.NewRaster(func(imgW, imgH int) image.Image {
		if imgW <= 0 || imgH <= 0 {
			return image.NewRGBA(image.Rect(0, 0, 1, 1))
		}
		frame := player.RenderFrame(imgW, imgH)
		if frame != nil {
			return frame
		}
		img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
		for y := 0; y < imgH; y++ {
			for x := 0; x < imgW; x++ {
				img.Set(x, y, color.Black)
			}
		}
		return img
	})
	videoRaster.SetMinSize(fyne.NewSize(640, 480))

	// Initialize mpv
	if err := player.Init(func() {
		fyne.Do(func() {
			videoRaster.Refresh()
		})
	}); err != nil {
		dialog.ShowError(fmt.Errorf("failed to initialize mpv: %w", err), picker)
		picker.Show()
		return
	}

	timeLabel := widget.NewLabel("00:00:00.000")
	durationLabel := widget.NewLabel("00:00:00.000")
	durationLabel.Alignment = fyne.TextAlignTrailing

	scrubber := widget.NewSlider(0, 1000)
	scrubber.Step = 1

	markLabel := widget.NewLabel("No mark set")
	markLabel.Importance = widget.LowImportance

	progressLabel := widget.NewLabel("")
	progressLabel.Alignment = fyne.TextAlignCenter
	progress := widget.NewProgressBar()
	progress.Hide()

	outputLabel := widget.NewLabel("Output will be saved as: [select a file first]")

	// Encoding options
	gpuEncoder, gpuName := detectGPUEncoder()
	encodingOptions := []string{"Stream Copy (fast, keyframe-aligned cuts)"}
	encodingValues := []string{"copy"}
	if gpuEncoder != "" {
		encodingOptions = append(encodingOptions, fmt.Sprintf("GPU Re-encode - %s (frame-accurate)", gpuName))
		encodingValues = append(encodingValues, "gpu")
	}
	encodingOptions = append(encodingOptions, "CPU Re-encode (frame-accurate, slower)")
	encodingValues = append(encodingValues, "cpu")

	encodeSelect := widget.NewSelect(encodingOptions, nil)
	encodeSelect.SetSelectedIndex(0)

	cutsList := widget.NewList(
		func() int { return len(cuts) },
		func() fyne.CanvasObject {
			return widget.NewLabel("00:00:00.000 -> 00:00:00.000 (0.0s)")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(cuts) {
				return
			}
			label, ok := obj.(*widget.Label)
			if !ok {
				return
			}
			c := cuts[id]
			startStr := formatTimeMs(c.Start * 1000)
			endStr := formatTimeMs(c.End * 1000)
			dur := c.End - c.Start
			label.SetText(fmt.Sprintf("%s -> %s  (%.1fs)", startStr, endStr, dur))
		},
	)
	cutsList.OnSelected = func(id widget.ListItemID) {
		selectedCut = id
	}

	var playBtn *widget.Button

	// Scrubber events
	scrubber.OnChanged = func(val float64) {
		if durationSec > 0 {
			pos := (val / 1000) * durationSec
			timeLabel.SetText(formatTimeMs(pos * 1000))
			player.Seek(pos)
		}
	}

	// Position update goroutine
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				dur := player.GetDuration()
				if dur > 0 && !math.IsInf(dur, 0) && !math.IsNaN(dur) && dur != durationSec {
					durationSec = dur
					fyne.Do(func() {
						durationLabel.SetText(formatTimeMs(dur * 1000))
					})
				}
				if durationSec > 0 {
					pos := player.GetPosition()
					fyne.Do(func() {
						timeLabel.SetText(formatTimeMs(pos * 1000))
						scrubber.SetValue((pos / durationSec) * 1000)
					})
				}
			}
		}
	}()

	browseBtn := widget.NewButton("Browse...", func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil || reader == nil {
				return
			}
			reader.Close()
			path := uriPath(reader.URI().Path())

			videoPath = path
			cuts = nil
			markInTime = nil
			selectedCut = -1
			markLabel.SetText("No mark set")
			markLabel.Importance = widget.LowImportance
			markLabel.Refresh()

			outputLabel.SetText(fmt.Sprintf("Output will be saved as: %s_trimmed%s",
				strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), filepath.Ext(path)))

			player.LoadVideo(path)
			cutsList.Refresh()
		}, w)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{
			".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpeg", ".mpg",
		}))
		fd.Show()
	})

	playBtn = widget.NewButton("Play", func() {
		isPlaying := player.TogglePlay()
		if isPlaying {
			playBtn.SetText("Pause")
		} else {
			playBtn.SetText("Play")
		}
	})

	markInBtn := widget.NewButton("Mark IN", func() {
		pos := player.GetPosition()
		markInTime = &pos
		markLabel.SetText(fmt.Sprintf("IN: %s", formatTimeMs(pos*1000)))
		markLabel.Importance = widget.WarningImportance
		markLabel.Refresh()
	})

	markOutBtn := widget.NewButton("Mark OUT + Add", func() {
		if markInTime == nil {
			return
		}
		pos := player.GetPosition()
		if pos <= *markInTime {
			dialog.ShowError(fmt.Errorf("out point must be after in point"), w)
			return
		}

		cuts = append(cuts, videoCut{Start: *markInTime, End: pos})
		cutsList.Refresh()

		markInTime = nil
		markLabel.SetText("Cut added!")
		markLabel.Importance = widget.SuccessImportance
		markLabel.Refresh()

		go func() {
			time.Sleep(cutAddedMessageDuration)
			fyne.Do(func() {
				markLabel.SetText("No mark set")
				markLabel.Importance = widget.LowImportance
				markLabel.Refresh()
			})
		}()
	})

	gotoBtn := widget.NewButton("Go to Selected", func() {
		if selectedCut >= 0 && selectedCut < len(cuts) {
			player.Seek(cuts[selectedCut].Start)
		}
	})

	removeBtn := widget.NewButton("Remove Selected", func() {
		if selectedCut >= 0 && selectedCut < len(cuts) {
			cuts = append(cuts[:selectedCut], cuts[selectedCut+1:]...)
			selectedCut = -1
			cutsList.UnselectAll()
			cutsList.Refresh()
		}
	})

	clearBtn := widget.NewButton("Clear All", func() {
		if len(cuts) == 0 {
			return
		}
		dialog.ShowConfirm("Confirm", "Clear all cuts?", func(ok bool) {
			if ok {
				cuts = nil
				selectedCut = -1
				cutsList.UnselectAll()
				cutsList.Refresh()
				markInTime = nil
				markLabel.SetText("No mark set")
				markLabel.Importance = widget.LowImportance
				markLabel.Refresh()
			}
		}, w)
	})

	cancelBtn := widget.NewButton("Cancel", nil)
	cancelBtn.Hide()

	var cutBtn *widget.Button
	cutBtn = widget.NewButton("Remove Sections", func() {
		if videoPath == "" {
			dialog.ShowError(fmt.Errorf("please select a video file first"), w)
			return
		}
		if len(cuts) == 0 {
			dialog.ShowError(fmt.Errorf("no cuts defined"), w)
			return
		}

		ext := filepath.Ext(videoPath)
		stem := strings.TrimSuffix(filepath.Base(videoPath), ext)
		outPath := filepath.Join(filepath.Dir(videoPath), stem+"_trimmed"+ext)

		// Verify the output directory exists and is writable.
		outDir := filepath.Dir(outPath)
		if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
			dialog.ShowError(fmt.Errorf("output directory does not exist: %s", outDir), w)
			return
		}

		// Capture encoding mode on UI thread before spawning goroutine.
		encMode := "copy"
		if idx := encodeSelect.SelectedIndex(); idx >= 0 && idx < len(encodingValues) {
			encMode = encodingValues[idx]
		}

		startTrim := func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancelBtn.Show()
			cancelBtn.OnTapped = func() {
				cancel()
				cancelBtn.Disable()
				progressLabel.SetText("Cancelling...")
			}
			doVideoTrim(ctx, cancel, w, videoPath, outPath, cuts, durationSec,
				encMode, gpuEncoder,
				cutBtn, cancelBtn, progress, progressLabel)
		}

		if _, err := os.Stat(outPath); err == nil {
			dialog.ShowConfirm("Confirm",
				fmt.Sprintf("Output file exists:\n%s\n\nOverwrite?", outPath),
				func(ok bool) {
					if !ok {
						return
					}
					startTrim()
				}, w)
			return
		}

		startTrim()
	})

	// Layout
	fileGroup := container.NewBorder(nil, nil, nil, browseBtn,
		widget.NewLabel("Video File:"))

	scrubberRow := container.NewBorder(nil, nil, timeLabel, durationLabel, scrubber)

	controlsRow := container.NewHBox(
		layout.NewSpacer(),
		widget.NewButton("|<", func() { player.Seek(0) }),
		newRepeatButton("-10s", func() { player.SeekRelative(-10) }),
		newRepeatButton("-1s", func() { player.SeekRelative(-1) }),
		newRepeatButton("<", func() { player.FrameBackStep() }),
		playBtn,
		newRepeatButton(">", func() { player.FrameStep() }),
		newRepeatButton("+1s", func() { player.SeekRelative(1) }),
		newRepeatButton("+10s", func() { player.SeekRelative(10) }),
		widget.NewButton(">|", func() {
			if durationSec > 0 {
				player.Seek(durationSec)
			}
		}),
		layout.NewSpacer(),
	)

	markRow := container.NewHBox(markInBtn, markLabel, markOutBtn, layout.NewSpacer())
	listBtns := container.NewHBox(gotoBtn, removeBtn, clearBtn, layout.NewSpacer())

	// Give the cuts list a minimum height so multiple cuts are visible
	cutsMinRect := canvas.NewRectangle(color.Transparent)
	cutsMinRect.SetMinSize(fyne.NewSize(0, 150))
	cutsListContainer := container.NewStack(cutsMinRect, cutsList)

	hintLabel := widget.NewLabel("1) Scrub to start of unwanted section, click 'Mark IN'\n2) Scrub to end, click 'Mark OUT + Add'\n3) Repeat for multiple cuts")
	hintLabel.Importance = widget.LowImportance

	encodeRow := container.NewBorder(nil, nil, widget.NewLabel("Encoding:"), nil, encodeSelect)

	topControls := container.NewVBox(
		fileGroup,
	)

	bottomControls := container.NewVBox(
		scrubberRow,
		controlsRow,
		widget.NewSeparator(),
		markRow,
		cutsListContainer,
		listBtns,
		hintLabel,
		widget.NewSeparator(),
		outputLabel,
		encodeRow,
		cutBtn,
		cancelBtn,
		progressLabel,
		progress,
	)

	content := container.NewBorder(topControls, bottomControls, nil, nil, videoRaster)

	w.SetContent(content)
	w.SetOnClosed(func() {
		close(stopCh)
		player.Cleanup()
		picker.Show()
	})
	w.Show()
}

func doVideoTrim(ctx context.Context, cancel context.CancelFunc, w fyne.Window, inputPath, outputPath string, cuts []videoCut, durationSec float64,
	encMode string, gpuEncoder string,
	cutBtn *widget.Button, cancelBtn *widget.Button, progress *widget.ProgressBar, progressLabel *widget.Label) {

	cutBtn.Disable()
	progress.Show()
	progress.SetValue(0)
	progressLabel.SetText("Processing...")

	go func() {
		defer cancel()
		defer fyne.Do(func() {
			cutBtn.Enable()
			cancelBtn.Hide()
		})

		success := false
		defer func() {
			if !success {
				cleanupFile(outputPath)
			}
		}()

		tmpDir, err := os.MkdirTemp("", "video_trim_")
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(err, w)
				progress.Hide()
			})
			return
		}
		defer os.RemoveAll(tmpDir)

		// Merge overlapping cuts
		sortedCuts := make([]videoCut, len(cuts))
		copy(sortedCuts, cuts)
		sort.Slice(sortedCuts, func(i, j int) bool {
			return sortedCuts[i].Start < sortedCuts[j].Start
		})

		var mergedCuts []videoCut
		for _, c := range sortedCuts {
			if len(mergedCuts) > 0 && c.Start <= mergedCuts[len(mergedCuts)-1].End {
				last := &mergedCuts[len(mergedCuts)-1]
				if c.End > last.End {
					last.End = c.End
				}
			} else {
				mergedCuts = append(mergedCuts, c)
			}
		}

		// Compute keep segments
		var segments []videoCut
		currentPos := 0.0
		for _, c := range mergedCuts {
			if c.Start > currentPos+minSegmentGap {
				segments = append(segments, videoCut{Start: currentPos, End: c.Start})
			}
			currentPos = c.End
		}
		if currentPos < durationSec-minSegmentGap {
			segments = append(segments, videoCut{Start: currentPos, End: durationSec})
		}

		if len(segments) == 0 {
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("nothing left after removing all selected sections"), w)
				progress.Hide()
			})
			return
		}

		totalDuration := 0.0
		for _, s := range segments {
			totalDuration += s.End - s.Start
		}

		var parts []string
		completed := 0.0

		for i, seg := range segments {
			fyne.Do(func() {
				progressLabel.SetText(fmt.Sprintf("Processing segment %d/%d", i+1, len(segments)))
				progress.SetValue(completed / totalDuration * 0.9)
			})

			partPath := filepath.Join(tmpDir, fmt.Sprintf("part%d%s", i, filepath.Ext(outputPath)))
			segDuration := seg.End - seg.Start

			var args []string
			if seg.Start < 0.1 {
				args = []string{"-y", "-i", inputPath, "-t", fmt.Sprintf("%f", seg.End)}
			} else {
				args = []string{"-y", "-ss", fmt.Sprintf("%f", seg.Start), "-i", inputPath,
					"-t", fmt.Sprintf("%f", segDuration)}
			}
			switch encMode {
			case "gpu":
				args = append(args, "-map", "0", "-c:v", gpuEncoder, "-c:a", "aac", "-avoid_negative_ts", "make_zero", partPath)
			case "cpu":
				args = append(args, "-map", "0", "-c:v", "libx264", "-c:a", "aac", "-avoid_negative_ts", "make_zero", partPath)
			default:
				args = append(args, "-map", "0", "-c", "copy", "-avoid_negative_ts", "make_zero", partPath)
			}

			if err := runFFmpegCtx(ctx, args...); err != nil {
				if ctx.Err() != nil {
					log.Printf("video trimmer: segment processing cancelled: %v", ctx.Err())
					fyne.Do(func() {
						progress.Hide()
						progressLabel.SetText("Cancelled")
					})
					return
				}
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("failed segment %d: %w", i+1, err), w)
					progress.Hide()
				})
				return
			}

			parts = append(parts, partPath)
			completed += segDuration
		}

		fyne.Do(func() {
			progressLabel.SetText("Finalizing...")
			progress.SetValue(0.9)
		})

		if len(parts) == 1 {
			// Try rename first (fast, same filesystem), fall back to streaming copy
			if err := os.Rename(parts[0], outputPath); err != nil {
				if err := streamCopyFile(parts[0], outputPath); err != nil {
					fyne.Do(func() {
						dialog.ShowError(err, w)
						progress.Hide()
					})
					return
				}
			}
		} else {
			concatPath := filepath.Join(tmpDir, "concat.txt")
			var concat strings.Builder
			for _, p := range parts {
				fmt.Fprintf(&concat, "file '%s'\n", escapeConcatPath(p))
			}
			if err := os.WriteFile(concatPath, []byte(concat.String()), 0644); err != nil {
				fyne.Do(func() {
					dialog.ShowError(err, w)
					progress.Hide()
				})
				return
			}

			err := runFFmpegCtx(ctx, "-y", "-f", "concat", "-safe", "0", "-i", concatPath,
				"-map", "0", "-c", "copy", outputPath)
			if err != nil {
				if ctx.Err() != nil {
					log.Printf("video trimmer: concat cancelled: %v", ctx.Err())
					fyne.Do(func() {
						progress.Hide()
						progressLabel.SetText("Cancelled")
					})
					return
				}
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("concat failed: %w", err), w)
					progress.Hide()
				})
				return
			}
		}

		success = true
		fyne.Do(func() {
			progress.SetValue(1.0)
			progressLabel.SetText("Done!")
			dialog.ShowInformation("Success", fmt.Sprintf("Video trimmed!\n\nSaved to:\n%s", outputPath), w)
		})
	}()
}
