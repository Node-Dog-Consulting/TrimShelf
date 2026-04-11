package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"github.com/ncruces/zenity"
)

// File-picker filter sets used by all tools.
var (
	filterM4B    = zenity.FileFilters{{Name: "M4B/M4A Audio", Patterns: []string{"*.m4b", "*.m4a"}}}
	filterM4BSave = zenity.FileFilters{{Name: "M4B Audio", Patterns: []string{"*.m4b"}}}
	filterVideo  = zenity.FileFilters{{Name: "Video Files", Patterns: []string{"*.mp4", "*.mkv", "*.avi", "*.mov", "*.wmv", "*.flv", "*.webm", "*.m4v", "*.mpeg", "*.mpg"}}}
	filterEPUB   = zenity.FileFilters{{Name: "EPUB", Patterns: []string{"*.epub"}}}
	filterImage  = zenity.FileFilters{{Name: "Images", Patterns: []string{"*.jpg", "*.jpeg", "*.png"}}}
)

// pickFile opens a native file-open dialog in a background goroutine and
// calls onPick with the chosen path on the Fyne UI thread. Cancellation is
// silently ignored.
func pickFile(filters zenity.FileFilters, onPick func(path string)) {
	go func() {
		path, err := zenity.SelectFile(filters)
		if err != nil || path == "" {
			return
		}
		fyne.Do(func() { onPick(path) })
	}()
}

// pickSaveFile opens a native file-save dialog in a background goroutine and
// calls onPick with the chosen path on the Fyne UI thread. Cancellation is
// silently ignored.
func pickSaveFile(defaultName string, filters zenity.FileFilters, onPick func(path string)) {
	go func() {
		path, err := zenity.SelectFileSave(zenity.Filename(defaultName), filters)
		if err != nil || path == "" {
			return
		}
		fyne.Do(func() { onPick(path) })
	}()
}

func init() {
	// macOS GUI apps launched from Finder or Spotlight inherit a minimal PATH
	// (/usr/bin:/bin:/usr/sbin:/sbin) that does not include Homebrew or other
	// common tool locations. Prepend the most common binary directories so
	// that exec.Command and exec.LookPath find ffmpeg, ffprobe, ffplay, etc.
	if runtime.GOOS == "darwin" {
		extra := strings.Join([]string{
			"/opt/homebrew/bin",  // Homebrew – Apple Silicon
			"/opt/homebrew/sbin",
			"/usr/local/bin",     // Homebrew – Intel / manual installs
			"/usr/local/sbin",
			"/opt/local/bin",     // MacPorts
		}, ":")
		cur := os.Getenv("PATH")
		if cur != "" {
			os.Setenv("PATH", extra+":"+cur)
		} else {
			os.Setenv("PATH", extra)
		}
	}
}

// debugLog controls whether FFmpeg/FFprobe commands are logged.
// Enable by setting CLIPPY_DEBUG=1 in the environment.
var debugLog = os.Getenv("CLIPPY_DEBUG") == "1"

// cleanupFile removes a file, logging any error. Used in deferred cleanup
// paths where the error cannot be surfaced to the user.
func cleanupFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("cleanup: failed to remove %s: %v", path, err)
	}
}

func formatDuration(seconds float64) string {
	s := int(seconds)
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

func formatHHMMSS(seconds float64) string {
	s := int(seconds)
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, sec)
}

func formatTimeMs(ms float64) string {
	seconds := ms / 1000.0
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := seconds - float64(h*3600+m*60)
	return fmt.Sprintf("%02d:%02d:%06.3f", h, m, s)
}

func findBinary(name string) string {
	binaryName := name
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	// Check next to the executable first
	if exePath, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exePath), binaryName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Check PATH as inherited by the process
	if p, err := exec.LookPath(name); err == nil {
		return p
	}

	// macOS GUI apps launched from Finder/Spotlight don't inherit the shell
	// PATH, so Homebrew binaries in /opt/homebrew/bin (Apple Silicon) or
	// /usr/local/bin (Intel) are invisible to exec.LookPath. Search the most
	// common install locations explicitly.
	if runtime.GOOS == "darwin" {
		darwinPaths := []string{
			"/opt/homebrew/bin",    // Apple Silicon Homebrew
			"/usr/local/bin",       // Intel Homebrew / manual installs
			"/opt/local/bin",       // MacPorts
			"/usr/bin",             // system
		}
		for _, dir := range darwinPaths {
			candidate := filepath.Join(dir, binaryName)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}

	return name
}

func findFFmpeg() string  { return findBinary("ffmpeg") }
func findFFprobe() string { return findBinary("ffprobe") }
func findFFplay() string  { return findBinary("ffplay") }

// escapeFFMeta escapes a string for use as an FFMETADATA value.
// Per the spec, \, =, ;, #, and newlines must be backslash-escaped.
func escapeFFMeta(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "=", "\\=")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, "#", "\\#")
	s = strings.ReplaceAll(s, "\n", "\\\n")
	return s
}

// uriPath converts the path component of a Fyne file URI to a local
// filesystem path. On Windows, Fyne follows RFC 3986 and returns paths like
// /C:/Users/... — the leading slash must be stripped so that Go's filepath
// functions treat the drive letter as the volume root rather than producing
// a root-relative path (\C:\Users\...) with an illegal colon component.
// The check is safe on macOS/Linux because valid Unix paths never have a
// colon as the third character.
func uriPath(p string) string {
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.Clean(p)
}

// escapeConcatPath escapes a path for use in an FFmpeg concat demuxer file.
// Single quotes in the path must be escaped as '\'' per the concat format.
// Forward slashes are used unconditionally because FFmpeg's concat demuxer
// does not handle Windows backslashes even with -safe 0.
func escapeConcatPath(p string) string {
	p = filepath.ToSlash(p)
	return strings.ReplaceAll(p, "'", "'\\''")
}

// checkFFmpeg verifies that ffmpeg and ffprobe are available.
// Returns an error with a user-friendly message if either is missing.
func checkFFmpeg() error {
	if _, err := exec.LookPath(findFFmpeg()); err != nil {
		return fmt.Errorf("FFmpeg not found.\n\nPlease install FFmpeg and ensure it is in your PATH.\nhttps://ffmpeg.org/download.html")
	}
	if _, err := exec.LookPath(findFFprobe()); err != nil {
		return fmt.Errorf("FFprobe not found.\n\nPlease install FFmpeg (includes ffprobe) and ensure it is in your PATH.\nhttps://ffmpeg.org/download.html")
	}
	return nil
}

func runFFprobe(args ...string) ([]byte, error) {
	bin := findFFprobe()
	if debugLog {
		log.Printf("[debug] exec: %s %s", bin, strings.Join(args, " "))
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return out, nil
}

func streamCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func runFFmpeg(args ...string) error {
	return runFFmpegCtx(context.Background(), args...)
}

func runFFmpegCtx(ctx context.Context, args ...string) error {
	bin := findFFmpeg()
	if debugLog {
		log.Printf("[debug] exec: %s %s", bin, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		// Return last 500 chars of output for debugging.
		// If there is no output (e.g. FFmpeg failed to open a file on Windows),
		// fall back to the Go error so the dialog is never blank.
		s := strings.TrimSpace(string(out))
		if len(s) > 500 {
			s = s[len(s)-500:]
		}
		if s == "" {
			return fmt.Errorf("ffmpeg: %w", err)
		}
		return fmt.Errorf("ffmpeg failed: %s", s)
	}
	return nil
}
