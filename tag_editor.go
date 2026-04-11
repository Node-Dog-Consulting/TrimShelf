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
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type tagEditorProbeResult struct {
	Format struct {
		Tags map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType   string `json:"codec_type"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Chapters []struct {
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

func ShowTagEditor(a fyne.App, picker fyne.Window) {
	if err := checkFFmpeg(); err != nil {
		dialog.ShowError(err, picker)
		picker.Show()
		return
	}

	w := a.NewWindow("M4B Tag Editor")
	w.Resize(fyne.NewSize(660, 420))

	var sourcePath string
	var probeData tagEditorProbeResult
	var coverData []byte
	var coverChanged bool

	fileLabel := widget.NewLabel("No file loaded")
	fileLabel.Wrapping = fyne.TextTruncate

	titleEntry := widget.NewEntry()
	artistEntry := widget.NewEntry()
	statusLabel := widget.NewLabel("")
	progress := widget.NewProgressBar()
	progress.Hide()

	// Cover image display
	coverCanvas := canvas.NewImageFromResource(nil)
	coverCanvas.FillMode = canvas.ImageFillContain
	coverCanvas.SetMinSize(fyne.NewSize(200, 200))

	replaceCoverBtn := widget.NewButton("Replace Cover…", func() {
		pickFile(filterImage, func(path string) {
			data, err := os.ReadFile(path)
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to read image: %w", err), w)
				return
			}
			coverData = data
			coverChanged = true
			coverCanvas.Resource = fyne.NewStaticResource("cover", coverData)
			coverCanvas.Refresh()
		})
	})

	saveBtn := widget.NewButton("Save As…", nil)
	saveBtn.Disable()

	openBtn := widget.NewButton("Open M4B File", func() {
		pickFile(filterM4B, func(path string) {
			sourcePath = path
			fileLabel.SetText(filepath.Base(path))

			// Reset state
			coverData = nil
			coverChanged = false
			coverCanvas.Resource = nil
			coverCanvas.Refresh()
			titleEntry.SetText("")
			artistEntry.SetText("")

			statusLabel.SetText("Loading…")

			out, err := runFFprobe("-v", "quiet", "-print_format", "json",
				"-show_format", "-show_streams", "-show_chapters", path)
			if err != nil {
				dialog.ShowError(fmt.Errorf("ffprobe failed: %w", err), w)
				statusLabel.SetText("")
				return
			}

			var data tagEditorProbeResult
			if err := json.Unmarshal(out, &data); err != nil {
				dialog.ShowError(fmt.Errorf("failed to parse ffprobe output: %w", err), w)
				statusLabel.SetText("")
				return
			}
			probeData = data

			// Populate tag fields (case-insensitive lookup)
			lowerTags := make(map[string]string, len(data.Format.Tags))
			for k, v := range data.Format.Tags {
				lowerTags[strings.ToLower(k)] = v
			}
			titleEntry.SetText(lowerTags["title"])
			artistEntry.SetText(lowerTags["artist"])

			// Detect and extract cover art
			hasCover := false
			for _, s := range data.Streams {
				if s.CodecType == "video" && s.Disposition.AttachedPic == 1 {
					hasCover = true
					break
				}
			}

			if hasCover {
				tmpFile, err := os.CreateTemp("", "cover_*.jpg")
				if err != nil {
					log.Printf("tag editor: failed to create temp cover file: %v", err)
					statusLabel.SetText("Tags loaded (cover extraction failed)")
					saveBtn.Enable()
					return
				}
				tmpFile.Close()
				tmpCoverPath := tmpFile.Name()

				err = runFFmpegCtx(context.Background(), "-y", "-i", path, "-map", "0:v:0", "-c", "copy", tmpCoverPath)
				if err != nil {
					log.Printf("tag editor: failed to extract cover: %v", err)
					cleanupFile(tmpCoverPath)
					statusLabel.SetText("Tags loaded (cover extraction failed)")
					saveBtn.Enable()
					return
				}

				data2, err := os.ReadFile(tmpCoverPath)
				cleanupFile(tmpCoverPath)
				if err != nil {
					log.Printf("tag editor: failed to read extracted cover: %v", err)
					statusLabel.SetText("Tags loaded (cover read failed)")
					saveBtn.Enable()
					return
				}
				coverData = data2
				coverCanvas.Resource = fyne.NewStaticResource("cover", coverData)
				coverCanvas.Refresh()
				statusLabel.SetText("Tags and cover loaded")
			} else {
				statusLabel.SetText("Tags loaded (no cover art)")
			}

			saveBtn.Enable()
		})
	})

	saveBtn.OnTapped = func() {
		defaultName := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath)) + "_tagged.m4b"
		pickSaveFile(defaultName, filterM4BSave, func(outPath string) {
			saveBtn.Disable()
			progress.Show()
			progress.SetValue(0)
			statusLabel.SetText("Saving…")

			go func() {
				doTagEditorSave(w, sourcePath, outPath,
					titleEntry.Text, artistEntry.Text,
					probeData, coverData, coverChanged,
					progress, statusLabel, saveBtn)
			}()
		})
	}

	// Layout
	leftPanel := container.NewVBox(
		coverCanvas,
		replaceCoverBtn,
	)

	form := widget.NewForm(
		widget.NewFormItem("Title", titleEntry),
		widget.NewFormItem("Artist", artistEntry),
	)

	panels := container.NewBorder(nil, nil, container.NewPadded(leftPanel), nil, container.NewPadded(form))

	topBar := container.NewBorder(nil, nil, openBtn, nil, fileLabel)

	bottomBar := container.NewVBox(
		progress,
		container.NewBorder(nil, nil, saveBtn, nil, statusLabel),
	)

	content := container.NewBorder(
		container.NewVBox(topBar, widget.NewSeparator()),
		bottomBar,
		nil, nil,
		panels,
	)

	w.SetContent(content)
	w.SetOnClosed(func() {
		picker.Show()
	})
	w.Show()
}

func doTagEditorSave(
	w fyne.Window,
	sourcePath, outPath string,
	title, artist string,
	probeData tagEditorProbeResult,
	coverData []byte,
	coverChanged bool,
	progress *widget.ProgressBar,
	statusLabel *widget.Label,
	saveBtn *widget.Button,
) {
	defer fyne.Do(func() {
		saveBtn.Enable()
	})

	success := false
	defer func() {
		if !success {
			cleanupFile(outPath)
		}
	}()

	tmpDir, err := os.MkdirTemp("", "m4b_tag_")
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
		})
		return
	}
	defer os.RemoveAll(tmpDir)

	// Build FFMETADATA
	metadataPath := filepath.Join(tmpDir, "FFMETADATA.txt")
	var meta strings.Builder
	meta.WriteString(";FFMETADATA1\n")
	meta.WriteString("title=" + escapeFFMeta(title) + "\n")
	meta.WriteString("artist=" + escapeFFMeta(artist) + "\n")

	for _, ch := range probeData.Chapters {
		start, err := strconv.ParseFloat(ch.StartTime, 64)
		if err != nil {
			continue
		}
		end, err := strconv.ParseFloat(ch.EndTime, 64)
		if err != nil {
			continue
		}
		startMs := int(start * 1000)
		endMs := int(end * 1000)
		chTitle := ch.Tags["title"]
		fmt.Fprintf(&meta, "\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n",
			startMs, endMs, escapeFFMeta(chTitle))
	}

	if err := os.WriteFile(metadataPath, []byte(meta.String()), 0644); err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
		})
		return
	}

	fyne.Do(func() { progress.SetValue(0.3) })

	var ffErr error
	if !coverChanged {
		ffErr = runFFmpegCtx(context.Background(),
			"-y", "-i", sourcePath,
			"-i", metadataPath,
			"-map", "0",
			"-map_metadata", "1",
			"-c", "copy",
			outPath)
	} else {
		// Write cover to temp file
		coverExt := ".jpg"
		if len(coverData) > 3 && coverData[0] == 0x89 && coverData[1] == 'P' && coverData[2] == 'N' {
			coverExt = ".png"
		}
		coverPath := filepath.Join(tmpDir, "new_cover"+coverExt)
		if err := os.WriteFile(coverPath, coverData, 0644); err != nil {
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("failed to write cover: %w", err), w)
				progress.Hide()
			})
			return
		}

		ffErr = runFFmpegCtx(context.Background(),
			"-y", "-i", sourcePath,
			"-i", coverPath,
			"-i", metadataPath,
			"-map", "0:a",
			"-map", "1:v",
			"-disposition:1", "attached_pic",
			"-map_metadata", "2",
			"-c", "copy",
			outPath)
	}

	if ffErr != nil {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("ffmpeg failed: %w", ffErr), w)
			progress.Hide()
			statusLabel.SetText("Save failed")
		})
		return
	}

	success = true
	fyne.Do(func() {
		progress.SetValue(1.0)
		statusLabel.SetText(fmt.Sprintf("Saved to %s", filepath.Base(outPath)))
		dialog.ShowInformation("Done", fmt.Sprintf("Tagged file saved to:\n%s", outPath), w)
	})
}
