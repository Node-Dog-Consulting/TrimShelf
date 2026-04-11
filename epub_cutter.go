package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func ShowEpubCutter(a fyne.App, picker fyne.Window) {
	w := a.NewWindow("EPUB Chapter Cutter")
	w.Resize(fyne.NewSize(700, 560))

	var sourcePath string
	var spineItems []SpineItemInfo
	var checked []bool

	fileLabel := widget.NewLabel("No file loaded")
	fileLabel.Wrapping = fyne.TextTruncate

	statusLabel := widget.NewLabel("")
	progress := widget.NewProgressBar()
	progress.Hide()

	chapterList := widget.NewList(
		func() int {
			return len(spineItems)
		},
		func() fyne.CanvasObject {
			check := widget.NewCheck("", nil)
			title := widget.NewLabel("Chapter Title")
			title.Wrapping = fyne.TextTruncate
			href := widget.NewLabel("file.xhtml")
			href.Importance = widget.LowImportance
			return container.NewBorder(nil, nil, check, href, title)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(spineItems) {
				return
			}
			c, ok := obj.(*fyne.Container)
			if !ok || len(c.Objects) < 3 {
				return
			}
			check, _ := c.Objects[1].(*widget.Check)
			title, _ := c.Objects[0].(*widget.Label)
			href, _ := c.Objects[2].(*widget.Label)
			if check == nil || title == nil || href == nil {
				return
			}

			item := spineItems[id]
			check.OnChanged = nil
			check.SetChecked(checked[id])
			check.OnChanged = func(b bool) {
				checked[id] = b
			}
			title.SetText(fmt.Sprintf("%d. %s", id+1, item.Title))
			href.SetText(item.Href)
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

	exportBtn := widget.NewButton("Export with Chapters Removed", nil)
	exportBtn.Disable()

	cancelBtn := widget.NewButton("Cancel", nil)
	cancelBtn.Hide()

	openBtn := widget.NewButton("Open EPUB File", func() {
		pickFile(filterEPUB, func(path string) {
			book, err := ReadEpub(path)
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to read EPUB: %w", err), w)
				return
			}

			items := book.GetSpineItems()
			if len(items) == 0 {
				dialog.ShowInformation("No Chapters", "This EPUB contains no spine items.", w)
				return
			}

			sourcePath = path
			spineItems = items
			checked = make([]bool, len(items))
			fileLabel.SetText(filepath.Base(path))
			chapterList.Refresh()
			exportBtn.Enable()
			statusLabel.SetText(fmt.Sprintf("%d chapters loaded", len(items)))
		})
	})

	exportBtn.OnTapped = func() {
		var selectedIDs []string
		for i, c := range checked {
			if c {
				selectedIDs = append(selectedIDs, spineItems[i].ID)
			}
		}
		if len(selectedIDs) == 0 {
			dialog.ShowInformation("Nothing selected", "Select at least one chapter to remove.", w)
			return
		}

		dialog.ShowConfirm("Confirm",
			fmt.Sprintf("Remove %d of %d chapters?", len(selectedIDs), len(spineItems)),
			func(ok bool) {
				if !ok {
					return
				}

				defaultName := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath)) + "_cut.epub"
				pickSaveFile(defaultName, filterEPUB, func(outPath string) {
					exportBtn.Disable()
					cancelBtn.Show()
					progress.Show()
					progress.SetValue(0)
					statusLabel.SetText("Exporting...")

					ctx, cancel := context.WithCancel(context.Background())
					cancelBtn.OnTapped = func() {
						cancel()
						cancelBtn.Disable()
						statusLabel.SetText("Cancelling...")
					}

					go func() {
						defer cancel()
						doEpubCut(ctx, w, sourcePath, outPath, selectedIDs, progress, statusLabel, exportBtn, cancelBtn)
					}()
				})
			}, w)
	}

	topBar := container.NewBorder(nil, nil, openBtn, nil, fileLabel)
	btnBar := container.NewHBox(selectAllBtn, deselectAllBtn)

	content := container.NewBorder(
		container.NewVBox(topBar),
		container.NewVBox(btnBar, exportBtn, cancelBtn, progress, statusLabel),
		nil, nil,
		chapterList,
	)

	w.SetContent(content)
	w.SetOnClosed(func() {
		picker.Show()
	})
	w.Show()
}

func doEpubCut(ctx context.Context, w fyne.Window, sourcePath, outPath string, removeIDs []string, progress *widget.ProgressBar, statusLabel *widget.Label, exportBtn *widget.Button, cancelBtn *widget.Button) {
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

	fyne.Do(func() {
		progress.SetValue(0.1)
		statusLabel.SetText("Reading EPUB...")
	})

	book, err := ReadEpub(sourcePath)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
		})
		return
	}

	fyne.Do(func() {
		progress.SetValue(0.3)
		statusLabel.SetText("Filtering spine...")
	})

	ids := make(map[string]bool)
	for _, id := range removeIDs {
		ids[id] = true
	}
	book.RemoveItems(ids)

	if ctx.Err() != nil {
		log.Printf("epub cutter: export cancelled before write: %v", ctx.Err())
		fyne.Do(func() {
			progress.Hide()
			statusLabel.SetText("Cancelled")
		})
		return
	}

	fyne.Do(func() {
		progress.SetValue(0.7)
		statusLabel.SetText("Writing EPUB...")
	})

	if err := book.WriteEpub(outPath); err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
			statusLabel.SetText("Export failed")
		})
		return
	}

	success = true
	fyne.Do(func() {
		progress.SetValue(1.0)
		statusLabel.SetText(fmt.Sprintf("Done — saved to %s", filepath.Base(outPath)))
		dialog.ShowInformation("Done", fmt.Sprintf("Removed %d chapter(s).\nSaved to:\n%s", len(removeIDs), outPath), w)
	})
}
