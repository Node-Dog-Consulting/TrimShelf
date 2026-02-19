# TrimShelf builds in two modes:
#   Standard (no tags)  — 4 tools: chapter extractor, audio trimmer, epub cutter, epub editor
#   MPV     (-tags mpv) — all 5 tools including video trimmer (requires libmpv)
.PHONY: build build-mpv run run-mpv clean

# Build without mpv support
build:
	CGO_ENABLED=1 go build -o trimshelf .
	xattr -cr trimshelf
	codesign --force --sign - trimshelf

# Build with mpv support (all 5 tools including video trimmer)
build-mpv:
	CGO_ENABLED=1 go build -tags mpv -o trimshelf .
	xattr -cr trimshelf
	codesign --force --sign - trimshelf

# Run without mpv
run:
	go run .

# Run with mpv
run-mpv:
	go run -tags mpv .

# Package as macOS .app bundle (requires fyne CLI: go install fyne.io/fyne/v2/cmd/fyne@latest)
build-mac-app:
	fyne package -os darwin -name TrimShelf

# Build for Windows (requires cross-compiler or Windows host)
build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build -o trimshelf.exe .

# Build for Linux
build-linux:
	CGO_ENABLED=1 go build -o trimshelf .

clean:
	rm -f trimshelf trimshelf.exe
	rm -rf TrimShelf.app
