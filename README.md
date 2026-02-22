# TrimShelf

A cross-platform desktop toolkit for trimming and editing audiobooks, video files, and ebooks. TrimShelf bundles six focused tools into a single lightweight app.

![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux-blue)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8)
![License](https://img.shields.io/badge/license-see%20LICENSE-green)

---

## Tools

### M4B Chapter Extractor
Select individual chapters from an M4B audiobook and export them as a new M4B file. Chapter metadata is preserved in the output.

- Opens `.m4b` / `.m4a` files
- Displays all chapters with checkboxes for individual selection
- Select All / Deselect All shortcuts
- Exports selected chapters as a single `.m4b` with rebuilt chapter metadata
- Cancellable with progress feedback

### M4B Audio Trimmer
Remove unwanted sections from an M4B audiobook using an interactive timeline. Cut regions are marked visually and the chapter map is remapped to the trimmed timeline.

- Opens `.m4b` / `.m4a` files
- Real-time playback via FFplay with a live playhead
- Timeline visualization — cut regions shown in red
- Mark cut start/end points; add, review, and delete cut regions
- Skip controls: ±1 s and ±10 s
- Exports a trimmed `.m4b` with chapter timestamps adjusted to the new timeline
- Cancellable with progress feedback

### Video Trimmer
Remove unwanted sections from a video file with frame-accurate cutting and hardware-accelerated re-encoding options. Powered by an embedded MPV video player.

> **Requires a build with MPV support.** See [Building](#building).

- Supports `.mp4`, `.mkv`, `.avi`, `.mov`, `.wmv`, `.flv`, `.webm`, `.m4v`, `.mpeg`, `.mpg`
- Full in-app video preview with scrubbing
- Frame stepping and time-skip controls
- Mark IN / Mark OUT to define cut regions; manage the cut list before exporting
- Three encoding modes:
  - **Stream copy** — fast, keyframe-aligned (no re-encode)
  - **GPU re-encode** — frame-accurate, hardware accelerated (Apple VideoToolbox, NVIDIA NVENC, AMD AMF, Intel QuickSync detected automatically)
  - **CPU re-encode** — frame-accurate, software encoder
- Output saved alongside the source file with a `_trimmed` suffix

### M4B Tag Editor
Edit the metadata tags and cover art of an M4B audiobook without re-encoding the audio.

- Opens `.m4b` / `.m4a` files
- Displays and edits **Title** and **Artist** tags
- Shows the embedded cover art; replace it with any `.jpg` or `.png` image
- Preserves all existing chapter metadata in the output
- Exports a new `_tagged.m4b` with updated tags and cover via stream copy (no quality loss)

### EPUB Chapter Cutter
Remove selected chapters from an EPUB ebook while keeping the rest of the document intact.

- Opens `.epub` files
- Lists all spine items (chapters) with checkboxes
- Select All / Deselect All shortcuts
- Exports a new `.epub` with selected chapters removed from the spine
- Confirmation prompt before export

### EPUB Text Editor
Edit the plain text content of individual chapters within an EPUB ebook.

- Opens `.epub` files
- Navigate chapters with Previous / Next buttons or a slider
- Edit chapter text in a multi-line editor
- Revert individual chapters to their original content
- Only modified chapters are rewritten in the output
- Exports a new `_edited.epub`; warns about unsaved changes before opening a new file

---

## Requirements

| Dependency | Required for | Notes |
|------------|-------------|-------|
| [FFmpeg](https://ffmpeg.org/download.html) | M4B Chapter Extractor, M4B Audio Trimmer, Video Trimmer | `ffmpeg` and `ffprobe` must be on `PATH` or placed next to the TrimShelf binary |
| [FFplay](https://ffmpeg.org/ffplay.html) | M4B Audio Trimmer (playback) | Included with most FFmpeg distributions |
| [libmpv](https://mpv.io) | Video Trimmer | Required only for MPV builds |

The EPUB tools have no external dependencies.

---

## Installation

### macOS
1. Download `TrimShelf.dmg` from the [latest release](https://github.com/Node-Dog-Consulting/clippy/releases/latest).
2. Open the DMG and drag **TrimShelf** to your Applications folder.
3. Launch TrimShelf from Applications or Spotlight.

### Windows
1. Download `TrimShelf-Setup.exe` from the [latest release](https://github.com/Node-Dog-Consulting/clippy/releases/latest).
2. Run the installer (administrator privileges required).
3. Launch TrimShelf from the Start Menu or the optional desktop shortcut.

### Linux
1. Download `TrimShelf-Linux-amd64.zip` from the [latest release](https://github.com/Node-Dog-Consulting/clippy/releases/latest).
2. Extract the archive.
3. Make the binary executable and run it:
   ```bash
   chmod +x trimshelf
   ./trimshelf
   ```

---

## Updates

TrimShelf checks for updates automatically at launch. If a newer version is available a dialog will appear with the release notes and an **Update Now** button.

You can also check manually at any time using the **Check for Updates** button at the bottom of the main window.

- **macOS** — downloads the new DMG and opens it; drag TrimShelf to Applications to complete the update.
- **Windows** — downloads and launches the new installer; TrimShelf closes automatically so the installer can replace it.
- **Linux** — downloads the new binary, replaces the current one, and relaunches.

---

## Building

### Prerequisites

- Go 1.25+
- A C compiler (`gcc` on Linux/Windows, Xcode CLT on macOS) — required for CGO
- `libmpv` development headers (only for the MPV / Video Trimmer build)

### Standard build (4 tools, no Video Trimmer)

```bash
make build
```

### Full build with Video Trimmer (requires libmpv)

**macOS (Homebrew)**
```bash
brew install mpv
make build-mpv
```

**Linux (apt)**
```bash
sudo apt install libmpv-dev
make build-mpv
```

**Windows**
Download `mpv-dev` from [shinchiro/mpv-winbuild-cmake](https://github.com/shinchiro/mpv-winbuild-cmake/releases) and set `CGO_CFLAGS` / `CGO_LDFLAGS` accordingly, then:
```bash
make build-mpv
```

### Run without building a binary

```bash
make run        # standard
make run-mpv    # with Video Trimmer
```

### Cross-platform builds

```bash
make build-windows   # produces trimshelf.exe
make build-linux     # produces trimshelf (Linux amd64)
```

### Clean

```bash
make clean
```

---

## Development

### Project layout

```
trimshelf/
├── main.go                  # App entry point and tool picker window
├── version.go               # Version variable (set at build time via -ldflags)
├── updater.go               # GitHub release check and in-app update logic
├── chapter_extractor.go     # M4B Chapter Extractor tool
├── audio_trimmer.go         # M4B Audio Trimmer tool
├── video_trimmer.go         # Video Trimmer tool (MPV build only)
├── mpv.go                   # MPV C bindings (MPV build only)
├── mpv_stub.go              # MPV stub for non-MPV builds
├── tag_editor.go            # M4B Tag Editor tool
├── epub_cutter.go           # EPUB Chapter Cutter tool
├── epub_editor.go           # EPUB Text Editor tool
├── epub_util.go             # EPUB ZIP/XML read-write utilities
├── util.go                  # Shared utilities (FFmpeg helpers, path handling)
├── assets/
│   ├── trimshelf.png        # App icon (source)
│   └── icon.icns            # macOS icon bundle
├── packaging/
│   ├── Info.plist           # macOS bundle metadata
│   ├── entitlements.plist   # macOS code signing entitlements
│   ├── installer.iss        # Windows Inno Setup script
│   └── dmg_ds_store         # macOS DMG layout
└── .github/workflows/
    └── build.yml            # CI/CD: build, sign, notarize, release
```

### Running tests

```bash
go test ./...
go test -v -tags mpv ./...   # include MPV build
```

### Versioning and releases

Versions are injected at build time:
```bash
go build -ldflags "-X main.version=v1.2.3" .
```

Untagged builds default to `dev`. The CI pipeline sets the version from the Git tag (`v*`) or falls back to `dev-<short SHA>`.

To publish a release, push a tag:
```bash
git tag v1.2.3
git push origin v1.2.3
```

The CI workflow will build signed/notarized artifacts for macOS, Windows, and Linux and publish them as a GitHub Release automatically.

---

## CI / CD

Builds run on self-hosted runners via `.github/workflows/build.yml`:

| Platform | Runner | Artifact |
|----------|--------|----------|
| macOS (ARM64) | self-hosted macOS | `TrimShelf.dmg` (signed + notarized) |
| Windows (amd64) | self-hosted Windows | `TrimShelf-Setup.exe` |
| Linux (amd64) | self-hosted Linux | `TrimShelf-Linux-amd64.zip` |

Releases are created automatically when a `v*` tag is pushed. The release notes are generated from commits since the previous release tag.

---

## License

See [LICENSE](LICENSE).
