package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

func main() {
	a := app.NewWithID("com.trimshelf.app")

	windowTitle := "TrimShelf"
	if version != "dev" {
		windowTitle = "TrimShelf " + version
	}
	picker := a.NewWindow(windowTitle)
	picker.SetFixedSize(true)

	title := widget.NewLabelWithStyle("Choose a tool:", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	btn1 := widget.NewButton("M4B Chapter Extractor", func() {
		picker.Hide()
		ShowChapterExtractor(a, picker)
	})
	btn2 := widget.NewButton("M4B Audio Trimmer", func() {
		picker.Hide()
		ShowAudioTrimmer(a, picker)
	})
	btn3 := widget.NewButton("Video Trimmer", func() {
		picker.Hide()
		ShowVideoTrimmer(a, picker)
	})
	btn4 := widget.NewButton("EPUB Chapter Cutter", func() {
		picker.Hide()
		ShowEpubCutter(a, picker)
	})
	btn5 := widget.NewButton("EPUB Text Editor", func() {
		picker.Hide()
		ShowEpubEditor(a, picker)
	})
	btn6 := widget.NewButton("M4B Tag Editor", func() {
		picker.Hide()
		ShowTagEditor(a, picker)
	})
	btn7 := widget.NewButton("Video Merger", func() {
		picker.Hide()
		ShowVideoMerger(a, picker)
	})
	btn8 := widget.NewButton("EPUB Tag Editor", func() {
		picker.Hide()
		ShowEpubTagEditor(a, picker)
	})

	updateBtn := widget.NewButton("Check for Updates", func() {
		go showUpdateDialogManual(picker)
	})
	updateBtn.Importance = widget.LowImportance

	content := container.NewVBox(
		layout.NewSpacer(),
		title,
		widget.NewSeparator(),
		btn1, btn2, btn3, btn4, btn5, btn6, btn7, btn8,
		widget.NewSeparator(),
		updateBtn,
		layout.NewSpacer(),
	)

	picker.SetContent(container.NewPadded(content))
	picker.Resize(fyne.NewSize(320, 470))
	picker.CenterOnScreen()
	picker.SetCloseIntercept(func() {
		a.Quit()
	})

	go checkAndShowUpdate(picker)

	picker.ShowAndRun()
}
