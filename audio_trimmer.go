package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// Timeline drawing constants.
const (
	timelinePad       = 4
	timelineBarMargin = 8
	timelineMinHeight = 40
)

type trimmerChapter struct {
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

type trimmerProbeResult struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Chapters []trimmerChapter `json:"chapters"`
}

type cutRegion struct {
	Start float64
	End   float64
}

func ShowAudioTrimmer(a fyne.App, picker fyne.Window) {
	if err := checkFFmpeg(); err != nil {
		dialog.ShowError(err, picker)
		picker.Show()
		return
	}

	w := a.NewWindow("M4B Audio Trimmer")
	w.Resize(fyne.NewSize(700, 560))

	var sourcePath string
	var totalDuration float64
	var chapters []trimmerChapter
	var cutRegions []cutRegion
	var pendingStart *float64
	var pendingEnd *float64

	// Playback state — all fields guarded by pb.mu.
	var pb struct {
		mu        sync.Mutex
		proc      *exec.Cmd
		startTime time.Time
		startPos  float64
		playing   bool
	}

	fileLabel := widget.NewLabel("No file loaded")
	fileLabel.Wrapping = fyne.TextTruncate
	durationLabel := widget.NewLabel("")
	durationLabel.Importance = widget.LowImportance

	statusLabel := widget.NewLabel("")
	progress := widget.NewProgressBar()
	progress.Hide()
	pendingLabel := widget.NewLabel("")

	positionLabel := widget.NewLabel("Position: 00:00:00 / 00:00:00")
	positionLabel.Importance = widget.LowImportance

	scrubber := widget.NewSlider(0, 1)
	scrubber.Step = 0.1

	// Timeline raster
	var timelineRaster *canvas.Raster
	drawTimeline := func(imgW, imgH int) image.Image {
		img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
		if imgW < 2 || totalDuration <= 0 {
			return img
		}

		bgColor := color.RGBA{192, 192, 192, 255}
		borderColor := color.RGBA{153, 153, 153, 255}

		pad := timelinePad
		barTop := timelineBarMargin
		barBottom := imgH - timelineBarMargin

		// Background bar
		for x := pad; x < imgW-pad; x++ {
			for y := barTop; y < barBottom; y++ {
				img.Set(x, y, bgColor)
			}
		}
		// Border
		barW := imgW - 2*pad
		for x := pad; x < imgW-pad; x++ {
			img.Set(x, barTop, borderColor)
			img.Set(x, barBottom-1, borderColor)
		}
		for y := barTop; y < barBottom; y++ {
			img.Set(pad, y, borderColor)
			img.Set(imgW-pad-1, y, borderColor)
		}

		// clamp restricts v to [lo, hi].
		clamp := func(v, lo, hi int) int {
			if v < lo {
				return lo
			}
			if v > hi {
				return hi
			}
			return v
		}

		// Cut regions (red)
		cutColor := color.RGBA{224, 64, 64, 255}
		for _, cr := range cutRegions {
			x1 := clamp(pad+int(cr.Start/totalDuration*float64(barW)), pad, imgW-pad)
			x2 := clamp(pad+int(cr.End/totalDuration*float64(barW)), pad, imgW-pad)
			for x := x1; x < x2; x++ {
				for y := barTop; y < barBottom; y++ {
					img.Set(x, y, cutColor)
				}
			}
		}

		// Playhead (blue line)
		pos := scrubber.Value
		playheadColor := color.RGBA{32, 96, 208, 255}
		px := clamp(pad+int(pos/totalDuration*float64(barW)), pad, imgW-pad-2)
		for y := 2; y < imgH-2; y++ {
			img.Set(px, y, playheadColor)
			if px+1 < imgW {
				img.Set(px+1, y, playheadColor)
			}
		}

		return img
	}
	timelineRaster = canvas.NewRaster(drawTimeline)
	timelineRaster.SetMinSize(fyne.NewSize(0, 40))

	refreshTimeline := func() {
		timelineRaster.Refresh()
	}

	updatePosition := func() {
		pos := scrubber.Value
		positionLabel.SetText(fmt.Sprintf("Position: %s / %s", formatHHMMSS(pos), formatHHMMSS(totalDuration)))
		refreshTimeline()
	}

	scrubber.OnChanged = func(val float64) {
		updatePosition()
	}

	// Playback functions
	killFFplay := func() {
		pb.mu.Lock()
		defer pb.mu.Unlock()
		if pb.proc != nil && pb.proc.Process != nil {
			pb.proc.Process.Kill()
			pb.proc.Wait()
			pb.proc = nil
		}
		pb.playing = false
	}

	playbackLabel := widget.NewLabel("")
	playbackLabel.Importance = widget.LowImportance

	var playBtn *widget.Button
	var stopBtn *widget.Button
	var tickStop chan struct{}

	startTicking := func() {
		pb.mu.Lock()
		tickStop = make(chan struct{})
		ch := tickStop
		pb.mu.Unlock()
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ch:
					return
				case <-ticker.C:
					pb.mu.Lock()
					if !pb.playing || pb.proc == nil {
						pb.mu.Unlock()
						fyne.Do(func() {
							playBtn.SetText("Play")
							stopBtn.Disable()
							playbackLabel.SetText("")
						})
						return
					}
					if pb.proc.ProcessState != nil {
						pb.playing = false
						pb.mu.Unlock()
						fyne.Do(func() {
							playBtn.SetText("Play")
							stopBtn.Disable()
							playbackLabel.SetText("")
						})
						return
					}
					elapsed := time.Since(pb.startTime).Seconds()
					newPos := pb.startPos + elapsed
					if newPos > totalDuration {
						newPos = totalDuration
					}
					pb.mu.Unlock()
					fyne.Do(func() {
						scrubber.SetValue(newPos)
						updatePosition()
					})
				}
			}
		}()
	}

	stopTicking := func() {
		pb.mu.Lock()
		if tickStop != nil {
			close(tickStop)
			tickStop = nil
		}
		pb.mu.Unlock()
	}

	stopPlayback := func() {
		killFFplay()
		stopTicking()
		playBtn.SetText("Play")
		stopBtn.Disable()
		playbackLabel.SetText("")
	}

	var doPlay func()
	doPlay = func() {
		if sourcePath == "" {
			return
		}
		killFFplay()
		stopTicking()

		pos := scrubber.Value
		if pos >= totalDuration {
			pos = 0
			scrubber.SetValue(0)
		}

		cmd := exec.Command(findFFplay(), "-nodisp", "-autoexit",
			"-ss", fmt.Sprintf("%f", pos),
			"-i", sourcePath)
		cmd.Stdout = nil
		cmd.Stderr = nil

		if err := cmd.Start(); err != nil {
			dialog.ShowError(fmt.Errorf("ffplay failed: %w", err), w)
			return
		}

		pb.mu.Lock()
		pb.proc = cmd
		pb.startTime = time.Now()
		pb.startPos = pos
		pb.playing = true
		pb.mu.Unlock()

		playBtn.SetText("Pause")
		stopBtn.Enable()
		playbackLabel.SetText("Playing...")

		startTicking()

		// Watch for process exit
		go func() {
			cmd.Wait()
			pb.mu.Lock()
			if pb.proc == cmd {
				pb.proc = nil
				pb.playing = false
			}
			pb.mu.Unlock()
		}()
	}

	playBtn = widget.NewButton("Play", func() {
		pb.mu.Lock()
		if pb.playing {
			pb.mu.Unlock()
			stopPlayback()
		} else {
			pb.mu.Unlock()
			doPlay()
		}
	})
	playBtn.Disable()

	stopBtn = widget.NewButton("Stop", func() {
		stopPlayback()
	})
	stopBtn.Disable()

	skip := func(delta float64) {
		if totalDuration <= 0 {
			return
		}
		newPos := math.Max(0, math.Min(scrubber.Value+delta, totalDuration))
		scrubber.SetValue(newPos)
		updatePosition()

		pb.mu.Lock()
		if pb.playing {
			pb.mu.Unlock()
			doPlay()
		} else {
			pb.mu.Unlock()
		}
	}

	// Cut region management
	updatePendingLabel := func() {
		var parts []string
		if pendingStart != nil {
			parts = append(parts, fmt.Sprintf("Start: %s", formatHHMMSS(*pendingStart)))
		}
		if pendingEnd != nil {
			parts = append(parts, fmt.Sprintf("End: %s", formatHHMMSS(*pendingEnd)))
		}
		if len(parts) > 0 {
			pendingLabel.SetText(strings.Join(parts, "  |  "))
		} else {
			pendingLabel.SetText("")
		}
	}

	markStartBtn := widget.NewButton("Mark Cut Start", nil)
	markStartBtn.Disable()
	markEndBtn := widget.NewButton("Mark Cut End", nil)
	markEndBtn.Disable()
	addCutBtn := widget.NewButton("Add Cut Region", nil)
	addCutBtn.Disable()

	// Cut regions list
	var cutList *widget.List
	cutList = widget.NewList(
		func() int { return len(cutRegions) },
		func() fyne.CanvasObject {
			label := widget.NewLabel("00:00:00 -> 00:00:00 (0:00)")
			label.Wrapping = fyne.TextTruncate
			delBtn := widget.NewButton("x", nil)
			delBtn.Importance = widget.DangerImportance
			return container.NewBorder(nil, nil, widget.NewLabel("1."), delBtn, label)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(cutRegions) {
				return
			}
			c, ok := obj.(*fyne.Container)
			if !ok || len(c.Objects) < 3 {
				return
			}
			numLabel, _ := c.Objects[1].(*widget.Label)
			label, _ := c.Objects[0].(*widget.Label)
			delBtn, _ := c.Objects[2].(*widget.Button)
			if numLabel == nil || label == nil || delBtn == nil {
				return
			}

			cr := cutRegions[id]
			dur := cr.End - cr.Start
			numLabel.SetText(fmt.Sprintf("%d.", id+1))
			label.SetText(fmt.Sprintf("%s  ->  %s   (%s)",
				formatHHMMSS(cr.Start), formatHHMMSS(cr.End), formatDuration(dur)))
			delBtn.OnTapped = func() {
				if id >= 0 && id < len(cutRegions) {
					cutRegions = append(cutRegions[:id], cutRegions[id+1:]...)
					cutList.Refresh()
					refreshTimeline()
				}
			}
		},
	)

	markStartBtn.OnTapped = func() {
		v := scrubber.Value
		pendingStart = &v
		updatePendingLabel()
		if pendingStart != nil && pendingEnd != nil {
			addCutBtn.Enable()
		}
	}
	markEndBtn.OnTapped = func() {
		v := scrubber.Value
		pendingEnd = &v
		updatePendingLabel()
		if pendingStart != nil && pendingEnd != nil {
			addCutBtn.Enable()
		}
	}
	addCutBtn.OnTapped = func() {
		if pendingStart == nil || pendingEnd == nil {
			return
		}
		start := *pendingStart
		end := *pendingEnd
		if start >= end {
			dialog.ShowInformation("Invalid Region", "Cut start must be before cut end.", w)
			return
		}

		// Check overlap
		for _, existing := range cutRegions {
			if start < existing.End && end > existing.Start {
				dialog.ShowInformation("Overlap",
					fmt.Sprintf("This region overlaps with existing cut %s -> %s.",
						formatHHMMSS(existing.Start), formatHHMMSS(existing.End)), w)
				return
			}
		}

		cutRegions = append(cutRegions, cutRegion{Start: start, End: end})
		sort.Slice(cutRegions, func(i, j int) bool {
			return cutRegions[i].Start < cutRegions[j].Start
		})

		pendingStart = nil
		pendingEnd = nil
		updatePendingLabel()
		addCutBtn.Disable()
		cutList.Refresh()
		refreshTimeline()
	}

	exportBtn := widget.NewButton("Export Trimmed File", nil)
	exportBtn.Disable()

	cancelBtn := widget.NewButton("Cancel", nil)
	cancelBtn.Hide()

	// Open file
	openBtn := widget.NewButton("Open M4B File", func() {
		pickFile(filterM4B, func(path string) {
			out, err := runFFprobe("-v", "quiet", "-print_format", "json",
				"-show_format", "-show_chapters", path)
			if err != nil {
				dialog.ShowError(fmt.Errorf("ffprobe failed: %w", err), w)
				return
			}

			var data trimmerProbeResult
			if err := json.Unmarshal(out, &data); err != nil {
				dialog.ShowError(fmt.Errorf("failed to parse ffprobe output: %w", err), w)
				return
			}

			dur, err := strconv.ParseFloat(data.Format.Duration, 64)
			if err != nil || dur <= 0 || math.IsInf(dur, 0) || math.IsNaN(dur) {
				dialog.ShowError(fmt.Errorf("failed to determine file duration"), w)
				return
			}

			stopPlayback()

			sourcePath = path
			totalDuration = dur
			chapters = data.Chapters
			cutRegions = nil
			pendingStart = nil
			pendingEnd = nil

			fileLabel.SetText(filepath.Base(path))
			durationLabel.SetText(fmt.Sprintf("Total: %s", formatHHMMSS(dur)))

			scrubber.Max = dur
			scrubber.SetValue(0)
			updatePosition()

			markStartBtn.Enable()
			markEndBtn.Enable()
			exportBtn.Enable()
			playBtn.Enable()

			updatePendingLabel()
			cutList.Refresh()
			refreshTimeline()

			chCount := len(chapters)
			if chCount > 0 {
				s := "s"
				if chCount == 1 {
					s = ""
				}
				statusLabel.SetText(fmt.Sprintf("Loaded — %d chapter%s found", chCount, s))
			} else {
				statusLabel.SetText("Loaded — no chapter metadata")
			}
		})
	})

	exportBtn.OnTapped = func() {
		if sourcePath == "" || len(cutRegions) == 0 {
			dialog.ShowInformation("No Cuts", "Add at least one cut region before exporting.", w)
			return
		}

		defaultName := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath)) + "_trimmed.m4b"
		pickSaveFile(defaultName, filterM4BSave, func(outPath string) {
			exportBtn.Disable()
			progress.Show()
			progress.SetValue(0)
			statusLabel.SetText("Exporting...")

			cancelBtn.Show()

			ctx, cancel := context.WithCancel(context.Background())
			cancelBtn.OnTapped = func() {
				cancel()
				cancelBtn.Disable()
				statusLabel.SetText("Cancelling...")
			}

			go func() {
				defer cancel()
				doTrimmerExport(ctx, w, sourcePath, outPath, totalDuration, chapters, cutRegions, progress, statusLabel, exportBtn, cancelBtn)
			}()
		})
	}

	// Layout
	topBar := container.NewBorder(nil, nil, openBtn, durationLabel, fileLabel)

	timelineBox := container.NewVBox(
		timelineRaster,
		scrubber,
		positionLabel,
	)

	playbackControls := container.NewHBox(
		playBtn, stopBtn,
		widget.NewSeparator(),
		widget.NewButton("<< 10s", func() { skip(-10) }),
		widget.NewButton("< 1s", func() { skip(-1) }),
		widget.NewButton("1s >", func() { skip(1) }),
		widget.NewButton("10s >>", func() { skip(10) }),
		layout.NewSpacer(),
		playbackLabel,
	)

	cutControls := container.NewHBox(
		markStartBtn, markEndBtn, addCutBtn,
		layout.NewSpacer(),
		pendingLabel,
	)

	content := container.NewBorder(
		container.NewVBox(topBar, timelineBox, playbackControls, cutControls),
		container.NewVBox(exportBtn, cancelBtn, progress, statusLabel),
		nil, nil,
		cutList,
	)

	w.SetContent(content)
	w.SetOnClosed(func() {
		stopPlayback()
		picker.Show()
	})
	w.Show()
}

func computeKeepRanges(totalDuration float64, cuts []cutRegion) []cutRegion {
	sorted := make([]cutRegion, len(cuts))
	copy(sorted, cuts)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start < sorted[j].Start
	})

	var keeps []cutRegion
	cursor := 0.0
	for _, c := range sorted {
		if cursor < c.Start {
			keeps = append(keeps, cutRegion{Start: cursor, End: c.Start})
		}
		cursor = c.End
	}
	if cursor < totalDuration {
		keeps = append(keeps, cutRegion{Start: cursor, End: totalDuration})
	}
	return keeps
}

type remappedChapter struct {
	Start float64
	End   float64
	Title string
}

func remapChapters(chapters []trimmerChapter, keepRanges []cutRegion) []remappedChapter {
	type keepOffset struct {
		Start  float64
		End    float64
		Offset float64
	}

	var offsets []keepOffset
	outputOffset := 0.0
	for _, k := range keepRanges {
		offsets = append(offsets, keepOffset{Start: k.Start, End: k.End, Offset: outputOffset})
		outputOffset += k.End - k.Start
	}

	var result []remappedChapter
	for _, ch := range chapters {
		chStart, err := strconv.ParseFloat(ch.StartTime, 64)
		if err != nil {
			continue
		}
		chEnd, err := strconv.ParseFloat(ch.EndTime, 64)
		if err != nil {
			continue
		}
		title := ch.Tags["title"]

		var newStart *float64
		var newEnd float64

		for _, ko := range offsets {
			interStart := math.Max(chStart, ko.Start)
			interEnd := math.Min(chEnd, ko.End)
			if interStart >= interEnd {
				continue
			}
			mappedStart := ko.Offset + (interStart - ko.Start)
			mappedEnd := ko.Offset + (interEnd - ko.Start)

			if newStart == nil {
				newStart = &mappedStart
			}
			newEnd = mappedEnd
		}

		if newStart != nil && newEnd > *newStart {
			result = append(result, remappedChapter{Start: *newStart, End: newEnd, Title: title})
		}
	}
	return result
}

func doTrimmerExport(ctx context.Context, w fyne.Window, sourcePath, outPath string, totalDuration float64,
	chapters []trimmerChapter, cuts []cutRegion,
	progress *widget.ProgressBar, statusLabel *widget.Label, exportBtn *widget.Button, cancelBtn *widget.Button) {
	defer fyne.Do(func() {
		exportBtn.Enable()
		cancelBtn.Hide()
	})

	success := false
	defer func() {
		if !success {
			cleanupFile(outPath)
		}
	}()

	tmpDir, err := os.MkdirTemp("", "m4b_trim_")
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
		})
		return
	}
	defer os.RemoveAll(tmpDir)

	keepRanges := computeKeepRanges(totalDuration, cuts)
	if len(keepRanges) == 0 {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("all audio would be cut — nothing to export"), w)
			progress.Hide()
		})
		return
	}

	totalSegs := len(keepRanges)
	segFiles := make([]string, 0, totalSegs)

	for i, k := range keepRanges {
		segPath := filepath.Join(tmpDir, fmt.Sprintf("seg_%04d.m4a", i))
		segFiles = append(segFiles, segPath)

		err := runFFmpegCtx(ctx, "-y", "-i", sourcePath,
			"-ss", fmt.Sprintf("%f", k.Start),
			"-to", fmt.Sprintf("%f", k.End),
			"-c", "copy", "-vn", segPath)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("audio trimmer: segment extraction cancelled: %v", ctx.Err())
				fyne.Do(func() {
					progress.Hide()
					statusLabel.SetText("Cancelled")
				})
				return
			}
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("segment extraction failed: %w", err), w)
				progress.Hide()
			})
			return
		}

		fyne.Do(func() {
			progress.SetValue(float64(i+1) / float64(totalSegs) * 0.9)
			statusLabel.SetText(fmt.Sprintf("Extracted keep segment %d/%d", i+1, totalSegs))
		})
	}

	// Build FFMETADATA
	metadataPath := filepath.Join(tmpDir, "FFMETADATA.txt")
	remapped := remapChapters(chapters, keepRanges)

	var meta strings.Builder
	meta.WriteString(";FFMETADATA1\n")
	for _, ch := range remapped {
		startMs := int(ch.Start * 1000)
		endMs := int(ch.End * 1000)
		fmt.Fprintf(&meta, "\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", startMs, endMs, escapeFFMeta(ch.Title))
	}
	if err := os.WriteFile(metadataPath, []byte(meta.String()), 0644); err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
		})
		return
	}

	// Concat list
	concatPath := filepath.Join(tmpDir, "concat.txt")
	var concat strings.Builder
	for _, seg := range segFiles {
		fmt.Fprintf(&concat, "file '%s'\n", escapeConcatPath(seg))
	}
	if err := os.WriteFile(concatPath, []byte(concat.String()), 0644); err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
		})
		return
	}

	// Concatenate
	err = runFFmpegCtx(ctx, "-y",
		"-f", "concat", "-safe", "0", "-i", concatPath,
		"-i", metadataPath,
		"-map_metadata", "1",
		"-c", "copy",
		outPath)
	if err != nil {
		if ctx.Err() != nil {
			log.Printf("audio trimmer: concat cancelled: %v", ctx.Err())
			fyne.Do(func() {
				progress.Hide()
				statusLabel.SetText("Cancelled")
			})
			return
		}
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("concat failed: %w", err), w)
			progress.Hide()
		})
		return
	}

	success = true
	fyne.Do(func() {
		progress.SetValue(1.0)
		cutTotal := 0.0
		for _, c := range cuts {
			cutTotal += c.End - c.Start
		}
		statusLabel.SetText(fmt.Sprintf("Done — saved to %s", filepath.Base(outPath)))
		dialog.ShowInformation("Done",
			fmt.Sprintf("Trimmed file saved to:\n%s\n\nRemoved %s of audio across %d cut(s).",
				outPath, formatHHMMSS(cutTotal), len(cuts)), w)
	})
}
