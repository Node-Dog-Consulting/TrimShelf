package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

const githubRepo = "Node-Dog-Consulting/TrimShelf"

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Body       string        `json:"body"`
	Assets     []githubAsset `json:"assets"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func fetchLatestRelease() (*githubRelease, error) {
	// Use the list endpoint so pre-releases are included.
	// /releases/latest only returns fully-published (non-prerelease) releases
	// and returns 404 when none exist.
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=10", githubRepo)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned status %d", resp.StatusCode)
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	// Pick the first non-draft release (list is newest-first).
	for i := range releases {
		if !releases[i].Draft {
			return &releases[i], nil
		}
	}
	return nil, fmt.Errorf("no releases found")
}

// isNewerVersion returns true if latest represents a newer semver than current.
// Dev builds (version starts with "dev") are never considered outdated.
func isNewerVersion(current, latest string) bool {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")
	if strings.HasPrefix(current, "dev") {
		return false
	}
	cv := parseSemver(current)
	lv := parseSemver(latest)
	maxLen := len(cv)
	if len(lv) > maxLen {
		maxLen = len(lv)
	}
	for i := 0; i < maxLen; i++ {
		var c, l int
		if i < len(cv) {
			c = cv[i]
		}
		if i < len(lv) {
			l = lv[i]
		}
		if l > c {
			return true
		}
		if l < c {
			return false
		}
	}
	return false
}

func parseSemver(v string) []int {
	parts := strings.Split(v, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		fmt.Sscanf(p, "%d", &nums[i])
	}
	return nums
}

// platformAsset returns the release asset for the current OS, or nil if none.
func platformAsset(release *githubRelease) *githubAsset {
	var target string
	switch runtime.GOOS {
	case "darwin":
		target = "TrimShelf.dmg"
	case "windows":
		target = "TrimShelf-Setup.exe"
	case "linux":
		target = "TrimShelf-Linux-amd64.zip"
	default:
		return nil
	}
	for i := range release.Assets {
		if release.Assets[i].Name == target {
			return &release.Assets[i]
		}
	}
	return nil
}

// checkAndShowUpdate fetches the latest GitHub release and, if a newer version
// exists, shows an update dialog. Must be called in a goroutine.
// It sleeps briefly on startup so the main window is fully shown before any
// dialog is presented (Fyne drops dialogs shown before the canvas is ready).
func checkAndShowUpdate(win fyne.Window) {
	time.Sleep(1 * time.Second)
	showUpdateDialog(win, true)
}

// showUpdateDialogManual is the entry point for a user-triggered update check.
// It shows errors rather than silently ignoring them.
func showUpdateDialogManual(win fyne.Window) {
	showUpdateDialog(win, false)
}

func showUpdateDialog(win fyne.Window, silent bool) {
	release, err := fetchLatestRelease()
	if err != nil {
		if !silent {
			dialog.ShowError(fmt.Errorf("could not check for updates: %w", err), win)
		}
		return
	}
	if !isNewerVersion(version, release.TagName) {
		if !silent {
			dialog.ShowInformation("No Updates", fmt.Sprintf("You're up to date! (%s)", version), win)
		}
		return
	}

	asset := platformAsset(release)

	notes := strings.TrimSpace(release.Body)
	if len(notes) > 500 {
		notes = notes[:500] + "..."
	}

	heading := widget.NewLabel(fmt.Sprintf("Version %s is available!", release.TagName))
	heading.TextStyle = fyne.TextStyle{Bold: true}

	current := widget.NewLabel(fmt.Sprintf("Currently installed: %s", version))
	current.TextStyle = fyne.TextStyle{Italic: true}

	notesLabel := widget.NewLabel(notes)
	notesLabel.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(heading, current, widget.NewSeparator(), notesLabel)

	if asset != nil {
		d := dialog.NewCustomConfirm(
			"Update Available",
			"Update Now",
			"Skip",
			content,
			func(ok bool) {
				if ok {
					go downloadAndApplyUpdate(asset, win)
				}
			},
			win,
		)
		d.Show()
	} else {
		dialog.ShowCustom("Update Available", "Close", content, win)
	}
}

// downloadAndApplyUpdate downloads the release asset and applies the update.
func downloadAndApplyUpdate(asset *githubAsset, win fyne.Window) {
	ctx, cancel := context.WithCancel(context.Background())

	progress := widget.NewProgressBar()
	statusLabel := widget.NewLabel("Downloading…")
	progressContent := container.NewVBox(statusLabel, progress)

	dlDialog := dialog.NewCustom("Downloading Update", "Cancel", progressContent, win)
	dlDialog.SetOnClosed(cancel)
	dlDialog.Show()

	tmpDir, err := os.MkdirTemp("", "trimshelf-update-*")
	if err != nil {
		dlDialog.Hide()
		dialog.ShowError(fmt.Errorf("failed to create temp directory: %w", err), win)
		return
	}

	destPath := filepath.Join(tmpDir, asset.Name)
	err = downloadWithProgress(ctx, asset.BrowserDownloadURL, destPath, asset.Size, func(pct float64) {
		progress.SetValue(pct)
	})

	if ctx.Err() != nil {
		// User cancelled
		os.RemoveAll(tmpDir)
		return
	}

	dlDialog.Hide()

	if err != nil {
		dialog.ShowError(fmt.Errorf("download failed: %w", err), win)
		os.RemoveAll(tmpDir)
		return
	}

	switch runtime.GOOS {
	case "darwin":
		applyUpdateMacOS(destPath, tmpDir, win)
	case "windows":
		applyUpdateWindows(destPath, win)
	case "linux":
		applyUpdateLinux(destPath, win)
	}
}

func downloadWithProgress(ctx context.Context, url, dest string, totalSize int64, progress func(float64)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	size := totalSize
	if size == 0 && resp.ContentLength > 0 {
		size = resp.ContentLength
	}

	buf := make([]byte, 32*1024)
	var downloaded int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			downloaded += int64(n)
			if size > 0 {
				progress(float64(downloaded) / float64(size))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

// applyUpdateMacOS mounts the DMG and copies the new .app over the running one,
// then relaunches. Falls back to opening the DMG for manual installation.
func applyUpdateMacOS(dmgPath, tmpDir string, win fyne.Window) {
	exePath, err := os.Executable()
	if err != nil {
		openDMGFallback(dmgPath, win)
		return
	}

	// Resolve symlinks to get the real executable path inside the .app bundle
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		openDMGFallback(dmgPath, win)
		return
	}

	// Walk up to find the .app bundle root (ends with .app)
	appBundle := ""
	dir := exePath
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		if strings.HasSuffix(dir, ".app") {
			appBundle = dir
			break
		}
		dir = parent
	}

	if appBundle == "" {
		// Not running inside a .app bundle (e.g. running bare binary)
		openDMGFallback(dmgPath, win)
		return
	}

	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		openDMGFallback(dmgPath, win)
		return
	}

	// Mount the DMG silently
	mountOut, err := exec.Command(
		"hdiutil", "attach", dmgPath,
		"-mountpoint", mountPoint,
		"-nobrowse", "-quiet",
	).Output()
	if err != nil {
		openDMGFallback(dmgPath, win)
		return
	}
	_ = mountOut

	// Find the .app inside the mounted DMG
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		exec.Command("hdiutil", "detach", mountPoint, "-quiet").Run()
		openDMGFallback(dmgPath, win)
		return
	}
	newApp := ""
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".app") {
			newApp = filepath.Join(mountPoint, e.Name())
			break
		}
	}
	if newApp == "" {
		exec.Command("hdiutil", "detach", mountPoint, "-quiet").Run()
		openDMGFallback(dmgPath, win)
		return
	}

	// Write a small shell script that runs after we quit:
	// wait for us to exit, replace the .app, relaunch
	appDir := filepath.Dir(appBundle)
	script := fmt.Sprintf(`#!/bin/sh
sleep 1
rm -rf %q
cp -R %q %q
xattr -rc %q 2>/dev/null || true
hdiutil detach %q -quiet 2>/dev/null || true
open %q
`,
		appBundle,
		newApp, appDir,
		filepath.Join(appDir, filepath.Base(newApp)),
		mountPoint,
		filepath.Join(appDir, filepath.Base(newApp)),
	)

	scriptPath := filepath.Join(tmpDir, "apply_update.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		exec.Command("hdiutil", "detach", mountPoint, "-quiet").Run()
		openDMGFallback(dmgPath, win)
		return
	}

	if err := exec.Command("sh", scriptPath).Start(); err != nil {
		exec.Command("hdiutil", "detach", mountPoint, "-quiet").Run()
		openDMGFallback(dmgPath, win)
		return
	}

	// Quit so the script can replace the .app
	fyne.CurrentApp().Quit()
}

func openDMGFallback(dmgPath string, win fyne.Window) {
	exec.Command("open", dmgPath).Start()
	dialog.ShowInformation(
		"Update Ready",
		"The update DMG has been opened.\n\nDrag TrimShelf to the Applications folder to complete the update, then relaunch the app.",
		win,
	)
}

// applyUpdateWindows launches the installer and exits so it can replace the app.
func applyUpdateWindows(installerPath string, win fyne.Window) {
	if err := exec.Command(installerPath).Start(); err != nil {
		dialog.ShowError(fmt.Errorf("could not launch installer: %w", err), win)
		return
	}
	time.Sleep(500 * time.Millisecond)
	fyne.CurrentApp().Quit()
}

// applyUpdateLinux extracts the binary from the zip, replaces the running binary
// via a shell script, and relaunches.
func applyUpdateLinux(zipPath string, win fyne.Window) {
	exePath, err := os.Executable()
	if err != nil {
		dialog.ShowError(fmt.Errorf("could not determine executable path: %w", err), win)
		return
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		dialog.ShowError(fmt.Errorf("could not resolve executable path: %w", err), win)
		return
	}

	binData, err := extractFromZip(zipPath, "trimshelf")
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to extract update: %w", err), win)
		return
	}

	tmpBin := exePath + ".new"
	if err := os.WriteFile(tmpBin, binData, 0755); err != nil {
		dialog.ShowError(fmt.Errorf("failed to write update: %w", err), win)
		return
	}

	// Replace and relaunch after we exit
	script := fmt.Sprintf("sleep 1 && mv %q %q && exec %q", tmpBin, exePath, exePath)
	if err := exec.Command("sh", "-c", script).Start(); err != nil {
		os.Remove(tmpBin)
		dialog.ShowError(fmt.Errorf("failed to launch updater: %w", err), win)
		return
	}

	fyne.CurrentApp().Quit()
}

func extractFromZip(zipPath, name string) ([]byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == name || strings.HasSuffix(f.Name, "/"+name) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("file %q not found in zip", name)
}
