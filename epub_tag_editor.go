package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func ShowEpubTagEditor(a fyne.App, picker fyne.Window) {
	w := a.NewWindow("EPUB Tag Editor")
	w.Resize(fyne.NewSize(660, 480))

	var sourcePath string
	var coverData []byte
	var coverChanged bool

	fileLabel := widget.NewLabel("No file loaded")
	fileLabel.Wrapping = fyne.TextTruncate

	titleEntry := widget.NewEntry()
	creatorEntry := widget.NewEntry()
	publisherEntry := widget.NewEntry()
	descEntry := widget.NewMultiLineEntry()
	descEntry.SetMinRowsVisible(3)

	statusLabel := widget.NewLabel("")
	progress := widget.NewProgressBar()
	progress.Hide()

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

	openBtn := widget.NewButton("Open EPUB File", func() {
		pickFile(filterEPUB, func(path string) {
			sourcePath = path
			fileLabel.SetText(filepath.Base(path))

			coverData = nil
			coverChanged = false
			coverCanvas.Resource = nil
			coverCanvas.Refresh()
			titleEntry.SetText("")
			creatorEntry.SetText("")
			publisherEntry.SetText("")
			descEntry.SetText("")
			statusLabel.SetText("Loading…")

			b, err := ReadEpub(path)
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to read EPUB: %w", err), w)
				statusLabel.SetText("")
				return
			}

			meta := b.GetMetadata()
			titleEntry.SetText(meta.Title)
			creatorEntry.SetText(meta.Creator)
			publisherEntry.SetText(meta.Publisher)
			descEntry.SetText(meta.Description)

			imgData, _ := b.GetCoverImage()
			if imgData != nil {
				coverData = imgData
				coverCanvas.Resource = fyne.NewStaticResource("cover", coverData)
				coverCanvas.Refresh()
				statusLabel.SetText("Metadata and cover loaded")
			} else {
				statusLabel.SetText("Metadata loaded (no cover art)")
			}

			saveBtn.Enable()
		})
	})

	saveBtn.OnTapped = func() {
		defaultName := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath)) + "_tagged.epub"
		pickSaveFile(defaultName, filterEPUB, func(outPath string) {
			saveBtn.Disable()
			progress.Show()
			progress.SetValue(0)
			statusLabel.SetText("Saving…")

			meta := EpubMetadata{
				Title:       titleEntry.Text,
				Creator:     creatorEntry.Text,
				Publisher:   publisherEntry.Text,
				Description: descEntry.Text,
			}
			go func() {
				doEpubTagSave(w, sourcePath, outPath, meta, coverData, coverChanged,
					progress, statusLabel, saveBtn)
			}()
		})
	}

	leftPanel := container.NewVBox(
		coverCanvas,
		replaceCoverBtn,
	)

	form := widget.NewForm(
		widget.NewFormItem("Title", titleEntry),
		widget.NewFormItem("Author", creatorEntry),
		widget.NewFormItem("Publisher", publisherEntry),
		widget.NewFormItem("Description", descEntry),
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

func doEpubTagSave(
	w fyne.Window,
	sourcePath, outPath string,
	meta EpubMetadata,
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

	fyne.Do(func() { progress.SetValue(0.2) })

	b, err := ReadEpub(sourcePath)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("failed to read EPUB: %w", err), w)
			progress.Hide()
			statusLabel.SetText("Save failed")
		})
		return
	}

	b.SetMetadata(meta)

	fyne.Do(func() { progress.SetValue(0.5) })

	if coverChanged && len(coverData) > 0 {
		mediaType := "image/jpeg"
		if len(coverData) > 3 && coverData[0] == 0x89 && coverData[1] == 'P' && coverData[2] == 'N' {
			mediaType = "image/png"
		}
		b.SetCoverImage(coverData, mediaType)
	}

	fyne.Do(func() { progress.SetValue(0.8) })

	if err := b.WriteEpub(outPath); err != nil {
		fyne.Do(func() {
			dialog.ShowError(fmt.Errorf("failed to write EPUB: %w", err), w)
			progress.Hide()
			statusLabel.SetText("Save failed")
		})
		return
	}

	success = true
	fyne.Do(func() {
		progress.SetValue(1.0)
		statusLabel.SetText(fmt.Sprintf("Saved to %s", filepath.Base(outPath)))
		dialog.ShowInformation("Done", fmt.Sprintf("Tagged EPUB saved to:\n%s", outPath), w)
	})
}