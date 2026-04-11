package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type chapterInfo struct {
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

type ffprobeChapters struct {
	Chapters []chapterInfo `json:"chapters"`
}

func ShowChapterExtractor(a fyne.App, picker fyne.Window) {
	if err := checkFFmpeg(); err != nil {
		dialog.ShowError(err, picker)
		picker.Show()
		return
	}

	w := a.NewWindow("M4B Chapter Extractor")
	w.Resize(fyne.NewSize(700, 560))

	var sourcePath string
	var chapters []chapterInfo
	var checked []bool

	fileLabel := widget.NewLabel("No file loaded")
	fileLabel.Wrapping = fyne.TextTruncate

	statusLabel := widget.NewLabel("")
	progress := widget.NewProgressBar()
	progress.Hide()

	// Chapter list
	chapterList := widget.NewList(
		func() int {
			return len(chapters)
		},
		func() fyne.CanvasObject {
			check := widget.NewCheck("", nil)
			title := widget.NewLabel("Chapter Title")
			title.Wrapping = fyne.TextTruncate
			dur := widget.NewLabel("00:00")
			return container.NewBorder(nil, nil, check, dur, title)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(chapters) {
				return
			}
			c, ok := obj.(*fyne.Container)
			if !ok || len(c.Objects) < 3 {
				return
			}
			check, _ := c.Objects[1].(*widget.Check)
			title, _ := c.Objects[0].(*widget.Label)
			dur, _ := c.Objects[2].(*widget.Label)
			if check == nil || title == nil || dur == nil {
				return
			}

			ch := chapters[id]
			t := ch.Tags["title"]
			if t == "" {
				t = fmt.Sprintf("Chapter %d", id+1)
			}

			check.OnChanged = nil
			check.SetChecked(checked[id])
			check.OnChanged = func(b bool) {
				checked[id] = b
			}
			title.SetText(fmt.Sprintf("%d. %s", id+1, t))

			start, errS := strconv.ParseFloat(ch.StartTime, 64)
			end, errE := strconv.ParseFloat(ch.EndTime, 64)
			if errS != nil || errE != nil {
				dur.SetText("(?:??)")
			} else {
				dur.SetText(fmt.Sprintf("(%s)", formatDuration(end-start)))
			}
		},
	)

	selectAllBtn := widget.NewButton("Select All", func() {
		for i := range checked {
			checked[i] = true
		}
		chapterList.Refresh()
	})
	deselectAllBtn := widget.NewButton("Deselect All", func() {
		for i := range checked {
			checked[i] = false
		}
		chapterList.Refresh()
	})

	extractBtn := widget.NewButton("Extract Selected Chapters", nil)
	extractBtn.Disable()

	cancelBtn := widget.NewButton("Cancel", nil)
	cancelBtn.Hide()

	openBtn := widget.NewButton("Open M4B File", func() {
		pickFile(filterM4B, func(path string) {
			sourcePath = path
			fileLabel.SetText(filepath.Base(path))

			// Load chapters via ffprobe
			out, err := runFFprobe("-v", "quiet", "-print_format", "json", "-show_chapters", path)
			if err != nil {
				dialog.ShowError(fmt.Errorf("ffprobe failed: %w", err), w)
				return
			}

			var data ffprobeChapters
			if err := json.Unmarshal(out, &data); err != nil {
				dialog.ShowError(fmt.Errorf("failed to parse ffprobe output: %w", err), w)
				return
			}

			if len(data.Chapters) == 0 {
				dialog.ShowInformation("No Chapters", "This file contains no chapter metadata.", w)
				return
			}

			chapters = data.Chapters
			checked = make([]bool, len(chapters))
			chapterList.Refresh()
			extractBtn.Enable()
			statusLabel.SetText(fmt.Sprintf("%d chapters loaded", len(chapters)))
		})
	})

	extractBtn.OnTapped = func() {
		// Collect selected
		var selected []int
		for i, c := range checked {
			if c {
				selected = append(selected, i)
			}
		}
		if len(selected) == 0 {
			dialog.ShowInformation("Nothing selected", "Select at least one chapter to extract.", w)
			return
		}

		defaultName := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath)) + "_extracted.m4b"
		pickSaveFile(defaultName, filterM4BSave, func(outPath string) {
			extractBtn.Disable()
			cancelBtn.Show()
			progress.Show()
			progress.SetValue(0)
			statusLabel.SetText("Extracting...")

			ctx, cancel := context.WithCancel(context.Background())
			cancelBtn.OnTapped = func() {
				cancel()
				cancelBtn.Disable()
				statusLabel.SetText("Cancelling...")
			}

			go func() {
				defer cancel()
				doExtractChapters(ctx, w, sourcePath, outPath, chapters, selected, progress, statusLabel, extractBtn, cancelBtn)
			}()
		})
	}

	// Layout
	topBar := container.NewBorder(nil, nil, openBtn, nil, fileLabel)
	btnBar := container.NewHBox(selectAllBtn, deselectAllBtn)

	content := container.NewBorder(
		container.NewVBox(topBar),
		container.NewVBox(btnBar, extractBtn, cancelBtn, progress, statusLabel),
		nil, nil,
		chapterList,
	)

	w.SetContent(content)
	w.SetOnClosed(func() {
		picker.Show()
	})
	w.Show()
}

func doExtractChapters(ctx context.Context, w fyne.Window, sourcePath, outPath string, chapters []chapterInfo, selected []int, progress *widget.ProgressBar, statusLabel *widget.Label, extractBtn *widget.Button, cancelBtn *widget.Button) {
	defer fyne.Do(func() {
		extractBtn.Enable()
		cancelBtn.Hide()
	})

	success := false
	defer func() {
		if !success {
			cleanupFile(outPath)
		}
	}()

	tmpDir, err := os.MkdirTemp("", "m4b_extract_")
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
		})
		return
	}
	defer os.RemoveAll(tmpDir)

	total := len(selected)
	segFiles := make([]string, 0, total)

	for i, idx := range selected {
		ch := chapters[idx]
		segPath := filepath.Join(tmpDir, fmt.Sprintf("seg_%04d.m4a", i))
		segFiles = append(segFiles, segPath)

		err := runFFmpegCtx(ctx, "-y", "-i", sourcePath,
			"-ss", ch.StartTime, "-to", ch.EndTime,
			"-c", "copy", "-vn", segPath)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("chapter extractor: segment extraction cancelled: %v", ctx.Err())
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
			progress.SetValue(float64(i+1) / float64(total) * 0.9)
			statusLabel.SetText(fmt.Sprintf("Extracted segment %d/%d", i+1, total))
		})
	}

	// Build FFMETADATA
	metadataPath := filepath.Join(tmpDir, "FFMETADATA.txt")
	var meta strings.Builder
	meta.WriteString(";FFMETADATA1\n")
	// cursorMs tracks the output timeline position. Chapters with
	// unparseable timestamps are skipped, which keeps cursorMs in sync
	// because both the segment file and the metadata entry are omitted.
	cursorMs := 0
	for i, idx := range selected {
		ch := chapters[idx]
		start, err := strconv.ParseFloat(ch.StartTime, 64)
		if err != nil {
			continue
		}
		end, err := strconv.ParseFloat(ch.EndTime, 64)
		if err != nil {
			continue
		}
		durationMs := int((end - start) * 1000)
		title := ch.Tags["title"]
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		startMs := cursorMs
		endMs := cursorMs + durationMs
		fmt.Fprintf(&meta, "\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", startMs, endMs, escapeFFMeta(title))
		cursorMs = endMs
	}
	if err := os.WriteFile(metadataPath, []byte(meta.String()), 0644); err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
		})
		return
	}

	// Build concat list
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
			log.Printf("chapter extractor: concat cancelled: %v", ctx.Err())
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
		statusLabel.SetText(fmt.Sprintf("Done — saved to %s", filepath.Base(outPath)))
		dialog.ShowInformation("Done", fmt.Sprintf("Extracted %d chapter(s) to:\n%s", total, outPath), w)
	})
}
