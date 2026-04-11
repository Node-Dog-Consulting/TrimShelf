package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/ncruces/zenity"
)

// pickMultipleFiles opens a native multi-file-open dialog in a background
// goroutine and calls onPick with the chosen paths on the Fyne UI thread.
func pickMultipleFiles(filters zenity.FileFilters, onPick func(paths []string)) {
	go func() {
		paths, err := zenity.SelectFileMultiple(filters)
		if err != nil || len(paths) == 0 {
			return
		}
		fyne.Do(func() { onPick(paths) })
	}()
}

func ShowVideoMerger(a fyne.App, picker fyne.Window) {
	if err := checkFFmpeg(); err != nil {
		dialog.ShowError(err, picker)
		picker.Show()
		return
	}

	w := a.NewWindow("Video Merger")
	w.Resize(fyne.NewSize(700, 550))

	var files []string
	var selectedIndex int = -1
	var dragSourceIndex int = -1

	progressLabel := widget.NewLabel("")
	progressLabel.Alignment = fyne.TextAlignCenter
	progress := widget.NewProgressBar()
	progress.Hide()

	outputLabel := widget.NewLabel("Output: [add files first]")

	fileList := widget.NewList(
		func() int { return len(files) },
		func() fyne.CanvasObject {
			return widget.NewLabel("placeholder_long_filename_for_template.mp4")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(files) {
				return
			}
			label, ok := obj.(*widget.Label)
			if !ok {
				return
			}
			prefix := fmt.Sprintf("%d. ", id+1)
			label.SetText(prefix + filepath.Base(files[id]))
		},
	)
	fileList.OnSelected = func(id widget.ListItemID) {
		selectedIndex = id
	}

	updateOutputLabel := func() {
		if len(files) == 0 {
			outputLabel.SetText("Output: [add files first]")
			return
		}
		first := files[0]
		ext := filepath.Ext(first)
		stem := strings.TrimSuffix(filepath.Base(first), ext)
		outPath := filepath.Join(filepath.Dir(first), stem+"_merged"+ext)
		outputLabel.SetText(fmt.Sprintf("Output: %s", outPath))
	}

	addBtn := widget.NewButton("Add Files...", func() {
		pickMultipleFiles(filterVideo, func(paths []string) {
			files = append(files, paths...)
			fileList.Refresh()
			updateOutputLabel()
		})
	})

	removeBtn := widget.NewButton("Remove", func() {
		if selectedIndex >= 0 && selectedIndex < len(files) {
			files = append(files[:selectedIndex], files[selectedIndex+1:]...)
			selectedIndex = -1
			fileList.UnselectAll()
			fileList.Refresh()
			updateOutputLabel()
		}
	})

	clearBtn := widget.NewButton("Clear All", func() {
		if len(files) == 0 {
			return
		}
		dialog.ShowConfirm("Confirm", "Remove all files from the list?", func(ok bool) {
			if ok {
				files = nil
				selectedIndex = -1
				fileList.UnselectAll()
				fileList.Refresh()
				updateOutputLabel()
			}
		}, w)
	})

	moveUpBtn := widget.NewButton("Move Up", func() {
		if selectedIndex > 0 && selectedIndex < len(files) {
			files[selectedIndex], files[selectedIndex-1] = files[selectedIndex-1], files[selectedIndex]
			selectedIndex--
			fileList.Select(selectedIndex)
			fileList.Refresh()
		}
	})

	moveDownBtn := widget.NewButton("Move Down", func() {
		if selectedIndex >= 0 && selectedIndex < len(files)-1 {
			files[selectedIndex], files[selectedIndex+1] = files[selectedIndex+1], files[selectedIndex]
			selectedIndex++
			fileList.Select(selectedIndex)
			fileList.Refresh()
		}
	})

	// Drag-and-drop: "Grab" picks up the selected item, "Drop Here" places it
	grabBtn := widget.NewButton("Grab", func() {
		if selectedIndex >= 0 && selectedIndex < len(files) {
			dragSourceIndex = selectedIndex
		}
	})

	dropBtn := widget.NewButton("Drop Here", func() {
		if dragSourceIndex < 0 || dragSourceIndex >= len(files) {
			return
		}
		if selectedIndex < 0 || selectedIndex >= len(files) {
			return
		}
		if dragSourceIndex == selectedIndex {
			dragSourceIndex = -1
			return
		}
		// Remove from source and insert at target
		item := files[dragSourceIndex]
		files = append(files[:dragSourceIndex], files[dragSourceIndex+1:]...)
		target := selectedIndex
		if dragSourceIndex < selectedIndex {
			target--
		}
		// Insert at target position
		files = append(files[:target+1], files[target:]...)
		files[target] = item
		dragSourceIndex = -1
		selectedIndex = target
		fileList.Select(target)
		fileList.Refresh()
	})

	// Encoding options
	gpuEncoder, gpuName := detectGPUEncoder()
	encodingOptions := []string{"Stream Copy (fast, same codec required)"}
	encodingValues := []string{"copy"}
	if gpuEncoder != "" {
		encodingOptions = append(encodingOptions, fmt.Sprintf("GPU Re-encode - %s", gpuName))
		encodingValues = append(encodingValues, "gpu")
	}
	encodingOptions = append(encodingOptions, "CPU Re-encode (universal, slower)")
	encodingValues = append(encodingValues, "cpu")

	encodeSelect := widget.NewSelect(encodingOptions, nil)
	encodeSelect.SetSelectedIndex(0)

	cancelBtn := widget.NewButton("Cancel", nil)
	cancelBtn.Hide()

	var mergeBtn *widget.Button
	mergeBtn = widget.NewButton("Merge Videos", func() {
		if len(files) < 2 {
			dialog.ShowError(fmt.Errorf("please add at least 2 video files"), w)
			return
		}

		first := files[0]
		ext := filepath.Ext(first)
		stem := strings.TrimSuffix(filepath.Base(first), ext)
		outPath := filepath.Join(filepath.Dir(first), stem+"_merged"+ext)

		outDir := filepath.Dir(outPath)
		if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
			dialog.ShowError(fmt.Errorf("output directory does not exist: %s", outDir), w)
			return
		}

		encMode := "copy"
		if idx := encodeSelect.SelectedIndex(); idx >= 0 && idx < len(encodingValues) {
			encMode = encodingValues[idx]
		}

		startMerge := func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancelBtn.Show()
			cancelBtn.OnTapped = func() {
				cancel()
				cancelBtn.Disable()
				progressLabel.SetText("Cancelling...")
			}
			doVideoMerge(ctx, cancel, w, files, outPath, encMode, gpuEncoder,
				mergeBtn, cancelBtn, progress, progressLabel)
		}

		if _, err := os.Stat(outPath); err == nil {
			dialog.ShowConfirm("Confirm",
				fmt.Sprintf("Output file exists:\n%s\n\nOverwrite?", outPath),
				func(ok bool) {
					if ok {
						startMerge()
					}
				}, w)
			return
		}

		startMerge()
	})

	// Layout
	fileGroup := container.NewBorder(nil, nil, nil, addBtn,
		widget.NewLabel("Video Files:"))

	orderBtns := container.NewVBox(
		moveUpBtn,
		moveDownBtn,
		widget.NewSeparator(),
		grabBtn,
		dropBtn,
		widget.NewSeparator(),
		removeBtn,
		clearBtn,
		layout.NewSpacer(),
	)

	fileListContainer := container.NewBorder(nil, nil, nil, orderBtns, fileList)

	encodeRow := container.NewBorder(nil, nil, widget.NewLabel("Encoding:"), nil, encodeSelect)

	hintLabel := widget.NewLabel("1) Add video files with 'Add Files...'\n2) Reorder with Move Up/Down or Grab + Drop Here\n3) Click 'Merge Videos' to combine them")
	hintLabel.Importance = widget.LowImportance

	content := container.NewVBox(
		fileGroup,
		fileListContainer,
		widget.NewSeparator(),
		hintLabel,
		outputLabel,
		encodeRow,
		mergeBtn,
		cancelBtn,
		progressLabel,
		progress,
	)

	w.SetContent(container.NewPadded(content))
	w.SetOnClosed(func() {
		picker.Show()
	})
	w.Show()
}

func doVideoMerge(ctx context.Context, cancel context.CancelFunc, w fyne.Window,
	files []string, outputPath string, encMode string, gpuEncoder string,
	mergeBtn *widget.Button, cancelBtn *widget.Button,
	progress *widget.ProgressBar, progressLabel *widget.Label) {

	mergeBtn.Disable()
	progress.Show()
	progress.SetValue(0)
	progressLabel.SetText("Processing...")

	go func() {
		defer cancel()
		defer fyne.Do(func() {
			mergeBtn.Enable()
			cancelBtn.Hide()
		})

		success := false
		defer func() {
			if !success {
				cleanupFile(outputPath)
			}
		}()

		tmpDir, err := os.MkdirTemp("", "video_merge_")
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(err, w)
				progress.Hide()
			})
			return
		}
		defer os.RemoveAll(tmpDir)

		if encMode == "copy" {
			// Direct concat demuxer - fast but requires same codec/params
			fyne.Do(func() {
				progressLabel.SetText("Concatenating...")
				progress.SetValue(0.5)
			})

			concatPath := filepath.Join(tmpDir, "concat.txt")
			var concat strings.Builder
			for _, f := range files {
				fmt.Fprintf(&concat, "file '%s'\n", escapeConcatPath(f))
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
					log.Printf("video merger: concat cancelled: %v", ctx.Err())
					fyne.Do(func() {
						progress.Hide()
						progressLabel.SetText("Cancelled")
					})
					return
				}
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("merge failed: %w", err), w)
					progress.Hide()
				})
				return
			}
		} else {
			// Re-encode each file to a common format, then concat
			var parts []string
			for i, f := range files {
				fyne.Do(func() {
					progressLabel.SetText(fmt.Sprintf("Re-encoding file %d/%d...", i+1, len(files)))
					progress.SetValue(float64(i) / float64(len(files)) * 0.9)
				})

				partPath := filepath.Join(tmpDir, fmt.Sprintf("part%d.ts", i))
				var args []string
				args = append(args, "-y", "-i", f)

				switch encMode {
				case "gpu":
					args = append(args, "-c:v", gpuEncoder, "-c:a", "aac",
						"-bsf:v", "h264_mp4toannexb", "-f", "mpegts", partPath)
				default:
					args = append(args, "-c:v", "libx264", "-c:a", "aac",
						"-bsf:v", "h264_mp4toannexb", "-f", "mpegts", partPath)
				}

				if err := runFFmpegCtx(ctx, args...); err != nil {
					if ctx.Err() != nil {
						log.Printf("video merger: re-encode cancelled: %v", ctx.Err())
						fyne.Do(func() {
							progress.Hide()
							progressLabel.SetText("Cancelled")
						})
						return
					}
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("failed to re-encode file %d (%s): %w",
							i+1, filepath.Base(f), err), w)
						progress.Hide()
					})
					return
				}
				parts = append(parts, partPath)
			}

			fyne.Do(func() {
				progressLabel.SetText("Finalizing...")
				progress.SetValue(0.9)
			})

			// Concat using the concat protocol for TS files
			concatInput := "concat:" + strings.Join(parts, "|")
			err := runFFmpegCtx(ctx, "-y", "-i", concatInput,
				"-c", "copy", "-bsf:a", "aac_adtstoasc", outputPath)
			if err != nil {
				if ctx.Err() != nil {
					log.Printf("video merger: final concat cancelled: %v", ctx.Err())
					fyne.Do(func() {
						progress.Hide()
						progressLabel.SetText("Cancelled")
					})
					return
				}
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("final merge failed: %w", err), w)
					progress.Hide()
				})
				return
			}
		}

		success = true
		fyne.Do(func() {
			progress.SetValue(1.0)
			progressLabel.SetText("Done!")
			dialog.ShowInformation("Success",
				fmt.Sprintf("Videos merged!\n\nSaved to:\n%s", outputPath), w)
		})
	}()
}
