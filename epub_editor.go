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
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// editorSizedTheme wraps a base theme and overrides only the text size,
// allowing the EPUB editor to have an independently adjustable font size.
type editorSizedTheme struct {
	fyne.Theme
	size float32
}

func (t *editorSizedTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameText {
		return t.size
	}
	return t.Theme.Size(name)
}

func ShowEpubEditor(a fyne.App, picker fyne.Window) {
	w := a.NewWindow("EPUB Text Editor")
	w.Resize(fyne.NewSize(700, 620))

	var sourcePath string
	var book *EpubBook
	var spineItems []SpineItemInfo
	var currentPage int = -1
	originalTexts := make(map[int]string)
	// editedTexts is modified on the UI thread only; the export
	// goroutine receives a snapshot copy to avoid data races.
	editedTexts := make(map[int]string)

	fileLabel := widget.NewLabel("No file loaded")
	fileLabel.Wrapping = fyne.TextTruncate

	statusLabel := widget.NewLabel("")
	progress := widget.NewProgressBar()
	progress.Hide()

	editor := widget.NewMultiLineEntry()
	editor.Wrapping = fyne.TextWrapWord
	editor.Disable()

	pageLabel := widget.NewLabel("No file loaded")
	pageLabel.Alignment = fyne.TextAlignCenter

	modifiedLabel := widget.NewLabel("No chapters modified")
	modifiedLabel.Importance = widget.LowImportance

	slider := widget.NewSlider(0, 0)
	slider.Step = 1

	updateModifiedLabel := func() {
		count := len(editedTexts)
		if count == 0 {
			modifiedLabel.SetText("No chapters modified")
			modifiedLabel.Importance = widget.LowImportance
		} else {
			s := "s"
			if count == 1 {
				s = ""
			}
			modifiedLabel.SetText(fmt.Sprintf("%d chapter%s modified", count, s))
			modifiedLabel.Importance = widget.MediumImportance
		}
		modifiedLabel.Refresh()
	}

	saveCurrentEdits := func() {
		if currentPage < 0 || len(spineItems) == 0 {
			return
		}
		currentText := editor.Text
		original := originalTexts[currentPage]
		if currentText != original {
			editedTexts[currentPage] = currentText
		} else {
			delete(editedTexts, currentPage)
		}
	}

	var showPage func(int)
	var prevBtn, nextBtn *widget.Button

	showPage = func(index int) {
		saveCurrentEdits()

		if len(spineItems) == 0 {
			return
		}
		if index < 0 {
			index = 0
		}
		if index >= len(spineItems) {
			index = len(spineItems) - 1
		}
		currentPage = index
		slider.SetValue(float64(index))

		item := spineItems[index]
		total := len(spineItems)
		pageLabel.SetText(fmt.Sprintf("Chapter %d of %d: %s", index+1, total, item.Title))

		if prevBtn != nil {
			if index > 0 {
				prevBtn.Enable()
			} else {
				prevBtn.Disable()
			}
		}
		if nextBtn != nil {
			if index < total-1 {
				nextBtn.Enable()
			} else {
				nextBtn.Disable()
			}
		}

		// Cache original text
		if _, ok := originalTexts[index]; !ok {
			content := book.GetContent(item.ID)
			if content != nil {
				originalTexts[index] = ExtractText(content)
			} else {
				originalTexts[index] = "(Unable to load page content)"
			}
		}

		// Show edited version if available
		text := originalTexts[index]
		if edited, ok := editedTexts[index]; ok {
			text = edited
		}

		editor.SetText(text)
		updateModifiedLabel()
	}

	prevBtn = widget.NewButton("<< Prev", func() {
		if currentPage > 0 {
			showPage(currentPage - 1)
		}
	})
	prevBtn.Disable()

	nextBtn = widget.NewButton("Next >>", func() {
		if currentPage < len(spineItems)-1 {
			showPage(currentPage + 1)
		}
	})
	nextBtn.Disable()

	slider.OnChanged = func(val float64) {
		idx := int(val)
		if idx != currentPage && len(spineItems) > 0 {
			showPage(idx)
		}
	}

	revertBtn := widget.NewButton("Revert Chapter", func() {
		if currentPage < 0 {
			return
		}
		delete(editedTexts, currentPage)
		text := originalTexts[currentPage]
		editor.SetText(text)
		updateModifiedLabel()
	})
	revertBtn.Disable()

	exportBtn := widget.NewButton("Export Edited EPUB", nil)
	exportBtn.Disable()

	cancelBtn := widget.NewButton("Cancel", nil)
	cancelBtn.Hide()

	openBtn := widget.NewButton("Open EPUB File", func() {
		saveCurrentEdits()

		if len(editedTexts) > 0 {
			dialog.ShowConfirm("Unsaved Changes",
				fmt.Sprintf("You have %d modified chapter(s). Opening a new file will discard these changes. Continue?", len(editedTexts)),
				func(ok bool) {
					if !ok {
						return
					}
					// Actually open
					showOpenDialog(a, w, &sourcePath, &book, &spineItems, &currentPage,
						originalTexts, editedTexts, fileLabel, statusLabel,
						editor, slider, revertBtn, exportBtn, showPage, updateModifiedLabel)
				}, w)
			return
		}

		showOpenDialog(a, w, &sourcePath, &book, &spineItems, &currentPage,
			originalTexts, editedTexts, fileLabel, statusLabel,
			editor, slider, revertBtn, exportBtn, showPage, updateModifiedLabel)
	})

	exportBtn.OnTapped = func() {
		saveCurrentEdits()

		if len(editedTexts) == 0 {
			dialog.ShowInformation("No Changes", "No chapters have been modified.", w)
			return
		}

		count := len(editedTexts)
		s := "s"
		if count == 1 {
			s = ""
		}

		dialog.ShowConfirm("Confirm",
			fmt.Sprintf("Export with %d modified chapter%s?", count, s),
			func(ok bool) {
				if !ok {
					return
				}

				defaultName := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath)) + "_edited.epub"
				fd := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
					if err != nil || writer == nil {
						return
					}
					outPath := uriPath(writer.URI().Path())
					writer.Close()

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

					// Snapshot editedTexts so the goroutine has its own copy
					// and won't race with the UI modifying the map.
					editsCopy := make(map[int]string, len(editedTexts))
					for k, v := range editedTexts {
						editsCopy[k] = v
					}
					go func() {
						defer cancel()
						doEpubEdit(ctx, w, sourcePath, outPath, spineItems, editsCopy, progress, statusLabel, exportBtn, cancelBtn)
					}()
				}, w)
				fd.SetFileName(defaultName)
				fd.SetFilter(storage.NewExtensionFileFilter([]string{".epub"}))
				fd.Show()
			}, w)
	}

	// ── Font size controls ──────────────────────────────────────
	const fontMin float32 = 8
	const fontMax float32 = 40

	eTheme := &editorSizedTheme{
		Theme: a.Settings().Theme(),
		size:  a.Settings().Theme().Size(theme.SizeNameText),
	}

	var themedEditor *container.ThemeOverride

	smallerBtn := widget.NewButton("A-", func() {
		if eTheme.size > fontMin {
			eTheme.size -= 2
			themedEditor.Refresh()
		}
	})
	largerBtn := widget.NewButton("A+", func() {
		if eTheme.size < fontMax {
			eTheme.size += 2
			themedEditor.Refresh()
		}
	})

	// Layout
	topBar := container.NewBorder(nil, nil, openBtn, nil, fileLabel)

	editorScroll := container.NewScroll(editor)
	editorScroll.SetMinSize(fyne.NewSize(0, 300))
	themedEditor = container.NewThemeOverride(editorScroll, eTheme)

	navBar := container.NewBorder(nil, nil, prevBtn, nextBtn, pageLabel)

	editBar := container.NewHBox(revertBtn, modifiedLabel, layout.NewSpacer(), smallerBtn, largerBtn)

	content := container.NewBorder(
		container.NewVBox(topBar),
		container.NewVBox(navBar, slider, editBar, exportBtn, cancelBtn, progress, statusLabel),
		nil, nil,
		themedEditor,
	)

	w.SetContent(content)
	w.SetOnClosed(func() {
		picker.Show()
	})
	w.Show()
}

func showOpenDialog(a fyne.App, w fyne.Window, sourcePath *string, book **EpubBook,
	spineItems *[]SpineItemInfo, currentPage *int,
	originalTexts, editedTexts map[int]string,
	fileLabel, statusLabel *widget.Label,
	editor *widget.Entry, slider *widget.Slider,
	revertBtn, exportBtn *widget.Button,
	showPage func(int), updateModifiedLabel func()) {

	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		reader.Close()
		path := uriPath(reader.URI().Path())

		b, err := ReadEpub(path)
		if err != nil {
			dialog.ShowError(fmt.Errorf("failed to read EPUB: %w", err), w)
			return
		}

		items := b.GetSpineItems()
		if len(items) == 0 {
			dialog.ShowInformation("No Content", "This EPUB contains no spine items.", w)
			return
		}

		*sourcePath = path
		*book = b
		*spineItems = items
		*currentPage = -1

		// Clear caches
		for k := range originalTexts {
			delete(originalTexts, k)
		}
		for k := range editedTexts {
			delete(editedTexts, k)
		}

		fileLabel.SetText(filepath.Base(path))

		maxIdx := float64(len(items) - 1)
		slider.Max = maxIdx
		slider.SetValue(0)

		editor.Enable()
		revertBtn.Enable()
		exportBtn.Enable()
		statusLabel.SetText(fmt.Sprintf("%d chapters loaded", len(items)))

		showPage(0)
	}, w)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".epub"}))
	fd.Show()
}

func doEpubEdit(ctx context.Context, w fyne.Window, sourcePath, outPath string, spineItems []SpineItemInfo, editedTexts map[int]string,
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

	fyne.Do(func() {
		progress.SetValue(0.1)
		statusLabel.SetText("Reading EPUB...")
	})

	book, err := ReadEpub(sourcePath)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(err, w)
			progress.Hide()
			statusLabel.SetText("Export failed")
		})
		return
	}

	modifiedCount := len(editedTexts)
	total := len(spineItems)

	for i, item := range spineItems {
		if ctx.Err() != nil {
			log.Printf("epub editor: chapter update cancelled: %v", ctx.Err())
			fyne.Do(func() {
				progress.Hide()
				statusLabel.SetText("Cancelled")
			})
			return
		}

		editedText, ok := editedTexts[i]
		if !ok {
			continue
		}

		originalContent := book.GetContent(item.ID)
		if originalContent == nil {
			continue
		}

		newContent := RebuildBody(originalContent, editedText)
		book.SetContent(item.ID, newContent)

		fyne.Do(func() {
			pct := 0.1 + (float64(i)/float64(total))*0.8
			progress.SetValue(pct)
			statusLabel.SetText(fmt.Sprintf("Updating chapter %d...", i+1))
		})
	}

	if ctx.Err() != nil {
		log.Printf("epub editor: export cancelled before write: %v", ctx.Err())
		fyne.Do(func() {
			progress.Hide()
			statusLabel.SetText("Cancelled")
		})
		return
	}

	fyne.Do(func() {
		progress.SetValue(0.9)
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
		s := "s"
		if modifiedCount == 1 {
			s = ""
		}
		statusLabel.SetText(fmt.Sprintf("Done — saved to %s", filepath.Base(outPath)))
		dialog.ShowInformation("Done",
			fmt.Sprintf("Edited %d chapter%s.\nSaved to:\n%s", modifiedCount, s, outPath), w)
	})
}
