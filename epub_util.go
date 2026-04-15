package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// collapseNewlines matches 3 or more consecutive newlines for collapsing.
var collapseNewlines = regexp.MustCompile(`\n{3,}`)

// ManifestItem represents an item in the OPF manifest.
type ManifestItem struct {
	ID         string
	Href       string
	MediaType  string
	Properties string
}

// EpubMetadata holds the editable metadata fields of an EPUB.
type EpubMetadata struct {
	Title       string
	Creator     string
	Publisher   string
	Description string
}

// SpineItem represents an item in the OPF spine.
type SpineItem struct {
	IDRef  string
	Linear bool
}

// TOCEntry represents a navigation entry (from NCX or nav doc).
type TOCEntry struct {
	Title    string
	Href     string
	Children []TOCEntry
}

// EpubBook represents a parsed EPUB archive.
type EpubBook struct {
	Path     string
	Manifest []ManifestItem
	Spine    []SpineItem
	TOC      []TOCEntry
	files    map[string][]byte // files loaded into memory (metadata + accessed content)
	removed  map[string]bool   // paths that have been removed
	zipPaths []string          // all file paths in the original ZIP
	opfPath  string            // path to the OPF file within the ZIP
	opfDir   string            // directory containing the OPF file
	opfRaw   []byte            // raw OPF XML
}

// getFile returns file data, loading lazily from the source ZIP if needed.
func (b *EpubBook) getFile(path string) ([]byte, error) {
	if data, ok := b.files[path]; ok {
		return data, nil
	}
	if b.removed[path] {
		return nil, fmt.Errorf("file removed: %s", path)
	}
	data, err := b.loadFromZip(path)
	if err != nil {
		return nil, err
	}
	b.files[path] = data
	return data, nil
}

// loadFromZip reads a single file from the source ZIP archive.
func (b *EpubBook) loadFromZip(path string) ([]byte, error) {
	r, err := zip.OpenReader(b.Path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name == path {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("file not found in ZIP: %s", path)
}

// OPF XML structures for parsing
type opfPackage struct {
	XMLName  xml.Name    `xml:"package"`
	Metadata opfMetadata `xml:"metadata"`
	Manifest opfManifest `xml:"manifest"`
	Spine    opfSpine    `xml:"spine"`
}

type opfMetadata struct {
	Inner []byte `xml:",innerxml"`
}

type opfManifest struct {
	Items []opfItem `xml:"item"`
}

type opfItem struct {
	ID        string `xml:"id,attr"`
	Href      string `xml:"href,attr"`
	MediaType string `xml:"media-type,attr"`
	Prop      string `xml:"properties,attr"`
}

type opfSpine struct {
	TOC      string         `xml:"toc,attr"`
	ItemRefs []opfItemRef   `xml:"itemref"`
}

type opfItemRef struct {
	IDRef  string `xml:"idref,attr"`
	Linear string `xml:"linear,attr"`
}

// NCX structures
type ncxDoc struct {
	XMLName xml.Name  `xml:"ncx"`
	NavMap  ncxNavMap `xml:"navMap"`
}

type ncxNavMap struct {
	Points []ncxNavPoint `xml:"navPoint"`
}

type ncxNavPoint struct {
	Label   ncxLabel      `xml:"navLabel"`
	Content ncxContent    `xml:"content"`
	Points  []ncxNavPoint `xml:"navPoint"`
}

type ncxLabel struct {
	Text string `xml:"text"`
}

type ncxContent struct {
	Src string `xml:"src,attr"`
}

// ReadEpub opens and parses an EPUB file.
// Only metadata files (container.xml, OPF, NCX) are loaded into memory eagerly.
// Content and binary files are loaded on demand via getFile.
func ReadEpub(epubPath string) (*EpubBook, error) {
	r, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open EPUB: %w", err)
	}
	defer r.Close()

	book := &EpubBook{
		Path:    epubPath,
		files:   make(map[string][]byte),
		removed: make(map[string]bool),
	}

	// Collect all file paths and eagerly load only container.xml
	for _, f := range r.File {
		book.zipPaths = append(book.zipPaths, f.Name)
		if f.Name == "META-INF/container.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to read %s: %w", f.Name, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("failed to read %s: %w", f.Name, err)
			}
			book.files[f.Name] = data
		}
	}

	// Find OPF path from container.xml
	containerData, ok := book.files["META-INF/container.xml"]
	if !ok {
		return nil, fmt.Errorf("missing META-INF/container.xml")
	}

	type rootfile struct {
		FullPath string `xml:"full-path,attr"`
	}
	type rootfiles struct {
		Rootfile []rootfile `xml:"rootfile"`
	}
	type containerXML struct {
		Rootfiles rootfiles `xml:"rootfiles"`
	}

	var cont containerXML
	if err := xml.Unmarshal(containerData, &cont); err != nil {
		return nil, fmt.Errorf("failed to parse container.xml: %w", err)
	}
	if len(cont.Rootfiles.Rootfile) == 0 {
		return nil, fmt.Errorf("no rootfile found in container.xml")
	}
	book.opfPath = cont.Rootfiles.Rootfile[0].FullPath
	book.opfDir = path.Dir(book.opfPath)
	if book.opfDir == "." {
		book.opfDir = ""
	}

	// Parse OPF (eagerly load into memory)
	opfData, err := book.getFile(book.opfPath)
	if err != nil {
		return nil, fmt.Errorf("OPF file not found: %s: %w", book.opfPath, err)
	}
	book.opfRaw = opfData

	var pkg opfPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, fmt.Errorf("failed to parse OPF: %w", err)
	}
	if len(pkg.Manifest.Items) == 0 && len(pkg.Spine.ItemRefs) == 0 {
		return nil, fmt.Errorf("OPF is empty (no manifest or spine)")
	}

	// Build manifest
	for _, item := range pkg.Manifest.Items {
		book.Manifest = append(book.Manifest, ManifestItem{
			ID:         item.ID,
			Href:       item.Href,
			MediaType:  item.MediaType,
			Properties: item.Prop,
		})
	}

	// Build spine
	for _, ref := range pkg.Spine.ItemRefs {
		linear := ref.Linear != "no"
		book.Spine = append(book.Spine, SpineItem{
			IDRef:  ref.IDRef,
			Linear: linear,
		})
	}

	// Parse TOC — try NCX first
	book.TOC = book.parseTOC(pkg)

	return book, nil
}

func (b *EpubBook) parseTOC(pkg opfPackage) []TOCEntry {
	// Find NCX file
	var ncxHref string
	for _, item := range pkg.Manifest.Items {
		if item.MediaType == "application/x-dtbncx+xml" {
			ncxHref = item.Href
			break
		}
	}

	if ncxHref != "" {
		fullPath := b.resolveHref(ncxHref)
		if fullPath != "" {
			if data, err := b.getFile(fullPath); err == nil {
				entries := b.parseNCX(data)
				if len(entries) > 0 {
					return entries
				}
			}
		}
	}

	// Try nav doc
	for _, item := range pkg.Manifest.Items {
		if item.MediaType == "application/xhtml+xml" {
			fullPath := b.resolveHref(item.Href)
			if fullPath == "" {
				continue
			}
			if data, err := b.getFile(fullPath); err == nil {
				entries := b.parseNavDoc(data)
				if len(entries) > 0 {
					return entries
				}
			}
		}
	}

	return nil
}

func (b *EpubBook) parseNCX(data []byte) []TOCEntry {
	var doc ncxDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return convertNavPoints(doc.NavMap.Points)
}

func convertNavPoints(points []ncxNavPoint) []TOCEntry {
	var entries []TOCEntry
	for _, p := range points {
		entry := TOCEntry{
			Title:    strings.TrimSpace(p.Label.Text),
			Href:     p.Content.Src,
			Children: convertNavPoints(p.Points),
		}
		entries = append(entries, entry)
	}
	return entries
}

func (b *EpubBook) parseNavDoc(data []byte) []TOCEntry {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}

	// Find <nav epub:type="toc">
	var navNode *html.Node
	var findNav func(*html.Node)
	findNav = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "nav" {
			for _, a := range n.Attr {
				if a.Key == "type" && a.Val == "toc" {
					navNode = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findNav(c)
			if navNode != nil {
				return
			}
		}
	}
	findNav(doc)

	if navNode == nil {
		return nil
	}

	// Find the <ol> inside the nav
	var ol *html.Node
	for c := navNode.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "ol" {
			ol = c
			break
		}
	}
	if ol == nil {
		return nil
	}

	return parseNavOL(ol)
}

func parseNavOL(ol *html.Node) []TOCEntry {
	var entries []TOCEntry
	for li := ol.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.Data != "li" {
			continue
		}
		var entry TOCEntry
		for c := li.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "a" {
				for _, a := range c.Attr {
					if a.Key == "href" {
						entry.Href = a.Val
					}
				}
				entry.Title = extractTextContent(c)
			} else if c.Type == html.ElementNode && c.Data == "ol" {
				entry.Children = parseNavOL(c)
			}
		}
		if entry.Title != "" || entry.Href != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func extractTextContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

func (b *EpubBook) resolveHref(href string) string {
	var resolved string
	if b.opfDir == "" {
		resolved = href
	} else {
		resolved = path.Join(b.opfDir, href)
	}
	// Normalize and validate to prevent directory traversal.
	resolved = path.Clean(resolved)
	if !validZipPath(resolved) {
		return ""
	}
	return resolved
}

// GetManifestItem finds a manifest item by ID.
func (b *EpubBook) GetManifestItem(id string) *ManifestItem {
	for i := range b.Manifest {
		if b.Manifest[i].ID == id {
			return &b.Manifest[i]
		}
	}
	return nil
}

// GetContent returns the raw file bytes for a manifest item.
func (b *EpubBook) GetContent(id string) []byte {
	item := b.GetManifestItem(id)
	if item == nil {
		return nil
	}
	href := b.resolveHref(item.Href)
	if href == "" {
		log.Printf("epub: invalid href for %s (%s)", id, item.Href)
		return nil
	}
	data, err := b.getFile(href)
	if err != nil {
		log.Printf("epub: failed to load content for %s (%s): %v", id, item.Href, err)
		return nil
	}
	return data
}

// SetContent replaces the content for a manifest item.
func (b *EpubBook) SetContent(id string, content []byte) {
	item := b.GetManifestItem(id)
	if item == nil {
		return
	}
	href := b.resolveHref(item.Href)
	if href == "" {
		return
	}
	b.files[href] = content
}

// RemoveItems removes items from manifest, spine, and files.
func (b *EpubBook) RemoveItems(ids map[string]bool) {
	// Collect hrefs to remove
	removeHrefs := make(map[string]bool)
	for _, item := range b.Manifest {
		if ids[item.ID] {
			href := b.resolveHref(item.Href)
			if href != "" {
				removeHrefs[href] = true
			}
		}
	}

	// Filter manifest
	var newManifest []ManifestItem
	for _, item := range b.Manifest {
		if !ids[item.ID] {
			newManifest = append(newManifest, item)
		}
	}
	b.Manifest = newManifest

	// Filter spine
	var newSpine []SpineItem
	for _, s := range b.Spine {
		if !ids[s.IDRef] {
			newSpine = append(newSpine, s)
		}
	}
	b.Spine = newSpine

	// Filter TOC
	b.TOC = filterTOC(b.TOC, removeHrefs)

	// Remove files
	for href := range removeHrefs {
		delete(b.files, href)
		b.removed[href] = true
	}
}

func filterTOC(entries []TOCEntry, removeHrefs map[string]bool) []TOCEntry {
	var filtered []TOCEntry
	for _, e := range entries {
		href := strings.SplitN(e.Href, "#", 2)[0]
		if removeHrefs[href] {
			// Promote surviving children
			children := filterTOC(e.Children, removeHrefs)
			filtered = append(filtered, children...)
		} else {
			e.Children = filterTOC(e.Children, removeHrefs)
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// CollectTOCTitles flattens the TOC into a href→title map.
func CollectTOCTitles(entries []TOCEntry) map[string]string {
	result := make(map[string]string)
	var collect func([]TOCEntry)
	collect = func(entries []TOCEntry) {
		for _, e := range entries {
			href := strings.SplitN(e.Href, "#", 2)[0]
			if href != "" {
				if _, exists := result[href]; !exists {
					result[href] = e.Title
				}
			}
			collect(e.Children)
		}
	}
	collect(entries)
	return result
}

// validZipPath checks that a path is safe for inclusion in a ZIP archive
// (no directory traversal via ".." components).
func validZipPath(name string) bool {
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return false
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return true
}

// WriteEpub writes the EPUB to a new file.
func (b *EpubBook) WriteEpub(outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	// Track whether we closed f successfully; on error paths the defer
	// ensures the handle is released.
	closed := false
	defer func() {
		if !closed {
			f.Close()
		}
	}()

	w := zip.NewWriter(f)

	// Write mimetype first, uncompressed (per EPUB spec)
	header := &zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	}
	mw, err := w.CreateHeader(header)
	if err != nil {
		return err
	}
	mw.Write([]byte("application/epub+zip"))

	// Rebuild OPF with current manifest/spine
	opfContent := b.rebuildOPF()
	b.files[b.opfPath] = opfContent

	// Rebuild NCX if present
	b.rebuildNCX()

	// Write files loaded in memory
	written := map[string]bool{"mimetype": true}
	for name, data := range b.files {
		if name == "mimetype" {
			continue
		}
		if !validZipPath(name) {
			return fmt.Errorf("unsafe path in EPUB: %s", name)
		}
		fw, err := w.Create(name)
		if err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
		if _, err := fw.Write(data); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
		written[name] = true
	}

	// Stream unloaded files directly from source ZIP (avoids loading images/fonts into memory)
	src, err := zip.OpenReader(b.Path)
	if err != nil {
		return fmt.Errorf("failed to reopen source EPUB: %w", err)
	}
	defer src.Close()
	for _, sf := range src.File {
		if written[sf.Name] || b.removed[sf.Name] {
			continue
		}
		if !validZipPath(sf.Name) {
			continue
		}
		rc, err := sf.Open()
		if err != nil {
			return fmt.Errorf("failed to read %s from source: %w", sf.Name, err)
		}
		fw, err := w.Create(sf.Name)
		if err != nil {
			rc.Close()
			return fmt.Errorf("failed to write %s: %w", sf.Name, err)
		}
		if _, err := io.Copy(fw, rc); err != nil {
			rc.Close()
			return fmt.Errorf("failed to write %s: %w", sf.Name, err)
		}
		rc.Close()
	}

	// Finalize the zip archive and close the file explicitly so we
	// can surface any flush/write errors to the caller.
	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to finalize EPUB archive: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close output file: %w", err)
	}
	closed = true
	return nil
}

func (b *EpubBook) rebuildOPF() []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")

	// Extract the package tag attributes from the original OPF
	// We need to preserve namespace declarations
	packageAttrs := extractPackageAttrs(b.opfRaw)
	fmt.Fprintf(&buf, "<package %s>\n", packageAttrs)

	// Extract original metadata block
	metaBlock := extractXMLBlock(string(b.opfRaw), "metadata")
	if metaBlock != "" {
		fmt.Fprintf(&buf, "  %s\n", metaBlock)
	}

	// Write manifest
	fmt.Fprintf(&buf, "  <manifest>\n")
	for _, item := range b.Manifest {
		if item.Properties != "" {
			fmt.Fprintf(&buf, "    <item id=%q href=%q media-type=%q properties=%q/>\n",
				item.ID, item.Href, item.MediaType, item.Properties)
		} else {
			fmt.Fprintf(&buf, "    <item id=%q href=%q media-type=%q/>\n",
				item.ID, item.Href, item.MediaType)
		}
	}
	fmt.Fprintf(&buf, "  </manifest>\n")

	// Write spine
	tocAttr := ""
	for _, item := range b.Manifest {
		if item.MediaType == "application/x-dtbncx+xml" {
			tocAttr = fmt.Sprintf(` toc=%q`, item.ID)
			break
		}
	}
	fmt.Fprintf(&buf, "  <spine%s>\n", tocAttr)
	for _, s := range b.Spine {
		linearAttr := ""
		if !s.Linear {
			linearAttr = ` linear="no"`
		}
		fmt.Fprintf(&buf, "    <itemref idref=%q%s/>\n", s.IDRef, linearAttr)
	}
	fmt.Fprintf(&buf, "  </spine>\n")

	// Extract and preserve guide block if present
	guideBlock := extractXMLBlock(string(b.opfRaw), "guide")
	if guideBlock != "" {
		fmt.Fprintf(&buf, "  %s\n", guideBlock)
	}

	fmt.Fprintf(&buf, "</package>\n")
	return buf.Bytes()
}

func extractPackageAttrs(opfRaw []byte) string {
	s := string(opfRaw)
	idx := strings.Index(s, "<package")
	if idx < 0 {
		return `xmlns="http://www.idpf.org/2007/opf" version="3.0"`
	}
	end := strings.Index(s[idx:], ">")
	if end < 0 {
		return `xmlns="http://www.idpf.org/2007/opf" version="3.0"`
	}
	tag := s[idx : idx+end+1]
	// Strip "<package" and ">"
	tag = strings.TrimPrefix(tag, "<package")
	tag = strings.TrimSuffix(tag, ">")
	tag = strings.TrimSuffix(tag, "/")
	return strings.TrimSpace(tag)
}

func extractXMLBlock(s, tagName string) string {
	// Try to find <tagName ...>...</tagName>
	openPattern := "<" + tagName
	idx := strings.Index(s, openPattern)
	if idx < 0 {
		return ""
	}

	closeTag := "</" + tagName + ">"
	closeIdx := strings.Index(s[idx:], closeTag)
	if closeIdx < 0 {
		return ""
	}

	return s[idx : idx+closeIdx+len(closeTag)]
}

func (b *EpubBook) rebuildNCX() {
	// Find NCX in manifest
	var ncxHref string
	for _, item := range b.Manifest {
		if item.MediaType == "application/x-dtbncx+xml" {
			ncxHref = item.Href
			break
		}
	}
	if ncxHref == "" {
		return
	}

	fullPath := b.resolveHref(ncxHref)
	if fullPath == "" {
		return
	}
	origData, err := b.getFile(fullPath)
	if err != nil {
		return
	}

	// Extract the head and docTitle from the original
	origStr := string(origData)
	headBlock := extractXMLBlock(origStr, "head")
	docTitleBlock := extractXMLBlock(origStr, "docTitle")

	// Extract namespace from original ncx tag
	ncxAttrs := "xmlns=\"http://www.daisy.org/z3986/2005/ncx/\" version=\"2005-1\""
	if idx := strings.Index(origStr, "<ncx"); idx >= 0 {
		end := strings.Index(origStr[idx:], ">")
		if end > 0 {
			tag := origStr[idx : idx+end]
			tag = strings.TrimPrefix(tag, "<ncx")
			ncxAttrs = strings.TrimSpace(tag)
		}
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	buf.WriteString("\n")
	buf.WriteString(fmt.Sprintf("<ncx %s>\n", ncxAttrs))

	if headBlock != "" {
		buf.WriteString("  ")
		buf.WriteString(headBlock)
		buf.WriteString("\n")
	}
	if docTitleBlock != "" {
		buf.WriteString("  ")
		buf.WriteString(docTitleBlock)
		buf.WriteString("\n")
	}

	buf.WriteString("  <navMap>\n")
	writeNavPoints(&buf, b.TOC, "    ", 1)
	buf.WriteString("  </navMap>\n")
	buf.WriteString("</ncx>\n")

	b.files[fullPath] = buf.Bytes()
}

func writeNavPoints(buf *bytes.Buffer, entries []TOCEntry, indent string, startOrder int) int {
	return writeNavPointsWithCounter(buf, entries, indent, startOrder, new(int))
}

func writeNavPointsWithCounter(buf *bytes.Buffer, entries []TOCEntry, indent string, startOrder int, counter *int) int {
	order := startOrder
	for _, e := range entries {
		buf.WriteString(fmt.Sprintf("%s<navPoint id=\"nav_%d\" playOrder=\"%d\">\n", indent, *counter, order))
		*counter++
		order++
		buf.WriteString(fmt.Sprintf("%s  <navLabel><text>%s</text></navLabel>\n", indent, xmlEscape(e.Title)))
		buf.WriteString(fmt.Sprintf("%s  <content src=%q/>\n", indent, e.Href))
		if len(e.Children) > 0 {
			order = writeNavPointsWithCounter(buf, e.Children, indent+"  ", order, counter)
		}
		buf.WriteString(fmt.Sprintf("%s</navPoint>\n", indent))
	}
	return order
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// ExtractText parses XHTML content and returns plain text with paragraph breaks.
func ExtractText(xhtmlData []byte) string {
	doc, err := html.Parse(bytes.NewReader(xhtmlData))
	if err != nil {
		return "(Unable to parse page content)"
	}

	// Find <body>
	var body *html.Node
	var findBody func(*html.Node)
	findBody = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBody(c)
			if body != nil {
				return
			}
		}
	}
	findBody(doc)
	if body == nil {
		body = doc
	}

	blockTags := map[string]bool{
		"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"div": true, "section": true, "article": true, "blockquote": true,
		"li": true, "dt": true, "dd": true, "pre": true,
		"header": true, "footer": true, "figcaption": true, "tr": true,
	}

	var parts []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			isBlock := blockTags[n.Data]
			isBr := n.Data == "br"

			if isBlock || isBr {
				parts = append(parts, "\n")
			}

			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}

			if isBlock {
				parts = append(parts, "\n")
			}
		} else if n.Type == html.TextNode {
			parts = append(parts, n.Data)
		} else {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	walk(body)

	text := strings.Join(parts, "")
	// Collapse 3+ newlines to 2
	text = collapseNewlines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// RebuildBody takes plain text and rebuilds XHTML body content.
func RebuildBody(originalXHTML []byte, newText string) []byte {
	doc, err := html.Parse(bytes.NewReader(originalXHTML))
	if err != nil {
		// Fallback: build a minimal XHTML document
		return buildMinimalXHTML(newText)
	}

	// Find <body>
	var body *html.Node
	var findBody func(*html.Node)
	findBody = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBody(c)
			if body != nil {
				return
			}
		}
	}
	findBody(doc)
	if body == nil {
		return buildMinimalXHTML(newText)
	}

	// Clear body children
	for body.FirstChild != nil {
		body.RemoveChild(body.FirstChild)
	}

	// Split text on double newlines, wrap each in <p>
	paragraphs := strings.Split(newText, "\n\n")
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		pNode := &html.Node{
			Type: html.ElementNode,
			Data: "p",
		}
		pNode.AppendChild(&html.Node{
			Type: html.TextNode,
			Data: p,
		})
		body.AppendChild(pNode)
		// Add a newline text node for formatting
		body.AppendChild(&html.Node{
			Type: html.TextNode,
			Data: "\n",
		})
	}

	var buf bytes.Buffer
	// Write as XML (XHTML)
	buf.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	buf.WriteString("\n")
	html.Render(&buf, doc)
	return buf.Bytes()
}

func buildMinimalXHTML(text string) []byte {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title></title></head>
<body>
`)
	paragraphs := strings.Split(text, "\n\n")
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		buf.WriteString("<p>")
		buf.WriteString(xmlEscape(p))
		buf.WriteString("</p>\n")
	}
	buf.WriteString("</body>\n</html>\n")
	return buf.Bytes()
}

// ── Metadata helpers ────────────────────────────────────────────────────────

// opfMetaParse is used only for reading dc: metadata; it is separate from the
// write-path structs so the two don't interfere with each other.
type opfMetaParse struct {
	XMLName  xml.Name      `xml:"package"`
	Metadata opfMetaFields `xml:"metadata"`
}

type opfMetaFields struct {
	Titles       []dcCharEl `xml:"http://purl.org/dc/elements/1.1/ title"`
	Creators     []dcCharEl `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Publishers   []dcCharEl `xml:"http://purl.org/dc/elements/1.1/ publisher"`
	Descriptions []dcCharEl `xml:"http://purl.org/dc/elements/1.1/ description"`
}

type dcCharEl struct {
	Value string `xml:",chardata"`
}

// GetMetadata parses and returns the editable metadata from the OPF.
func (b *EpubBook) GetMetadata() EpubMetadata {
	var pkg opfMetaParse
	if err := xml.Unmarshal(b.opfRaw, &pkg); err != nil {
		return EpubMetadata{}
	}
	m := pkg.Metadata
	meta := EpubMetadata{}
	if len(m.Titles) > 0 {
		meta.Title = strings.TrimSpace(m.Titles[0].Value)
	}
	if len(m.Creators) > 0 {
		meta.Creator = strings.TrimSpace(m.Creators[0].Value)
	}
	if len(m.Publishers) > 0 {
		meta.Publisher = strings.TrimSpace(m.Publishers[0].Value)
	}
	if len(m.Descriptions) > 0 {
		meta.Description = strings.TrimSpace(m.Descriptions[0].Value)
	}
	return meta
}

// SetMetadata updates the dc: elements in the raw OPF, replacing the first
// occurrence of each element or inserting it before </metadata> if absent.
func (b *EpubBook) SetMetadata(meta EpubMetadata) {
	opf := string(b.opfRaw)
	opf = setOPFDCElement(opf, "title", meta.Title)
	opf = setOPFDCElement(opf, "creator", meta.Creator)
	opf = setOPFDCElement(opf, "publisher", meta.Publisher)
	opf = setOPFDCElement(opf, "description", meta.Description)
	b.opfRaw = []byte(opf)
}

// setOPFDCElement replaces the first <dc:name>…</dc:name> in the OPF string, or
// inserts one before </metadata> if none is present.
func setOPFDCElement(opf, localName, value string) string {
	tag := "dc:" + localName
	re := regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(tag) + `[^>]*>.*?</` + regexp.QuoteMeta(tag) + `>`)
	replacement := "<" + tag + ">" + xmlEscape(value) + "</" + tag + ">"
	if loc := re.FindStringIndex(opf); loc != nil {
		return opf[:loc[0]] + replacement + opf[loc[1]:]
	}
	return strings.Replace(opf, "</metadata>", "    "+replacement+"\n  </metadata>", 1)
}

// ── Cover image helpers ──────────────────────────────────────────────────────

// GetCoverImage returns the raw bytes of the cover image, or nil if none.
func (b *EpubBook) GetCoverImage() ([]byte, error) {
	// EPUB3: manifest item with properties="cover-image"
	for _, item := range b.Manifest {
		if item.Properties == "cover-image" {
			data := b.GetContent(item.ID)
			return data, nil
		}
	}
	// EPUB2: <meta name="cover" content="item-id"/>
	if id := b.findCoverMetaID(); id != "" {
		data := b.GetContent(id)
		return data, nil
	}
	return nil, nil
}

// findCoverMetaID returns the manifest item ID referenced by an EPUB2
// <meta name="cover"> element, or "" if not present.
func (b *EpubBook) findCoverMetaID() string {
	reMeta := regexp.MustCompile(`<meta\b[^>]+>`)
	reContent := regexp.MustCompile(`content\s*=\s*["']([^"']+)["']`)
	for _, match := range reMeta.FindAllString(string(b.opfRaw), -1) {
		if strings.Contains(match, `name="cover"`) || strings.Contains(match, `name='cover'`) {
			if cm := reContent.FindStringSubmatch(match); len(cm) > 1 {
				return cm[1]
			}
		}
	}
	return ""
}

// SetCoverImage replaces the existing cover image or adds a new one.
func (b *EpubBook) SetCoverImage(data []byte, mediaType string) {
	ext := ".jpg"
	if mediaType == "image/png" {
		ext = ".png"
	}

	// Try to find and update an existing cover item.
	for i := range b.Manifest {
		if b.Manifest[i].Properties == "cover-image" {
			b.updateCoverFile(&b.Manifest[i], data, mediaType)
			return
		}
	}
	if id := b.findCoverMetaID(); id != "" {
		for i := range b.Manifest {
			if b.Manifest[i].ID == id {
				b.updateCoverFile(&b.Manifest[i], data, mediaType)
				return
			}
		}
	}

	// No existing cover: add one.
	coverHref := "cover" + ext
	var coverZipPath string
	if b.opfDir != "" {
		coverZipPath = b.opfDir + "/" + coverHref
	} else {
		coverZipPath = coverHref
	}
	b.Manifest = append(b.Manifest, ManifestItem{
		ID:         "cover-image",
		Href:       coverHref,
		MediaType:  mediaType,
		Properties: "cover-image",
	})
	b.files[coverZipPath] = data
	// EPUB2 compatibility: add <meta name="cover"> to the metadata block.
	metaTag := `<meta name="cover" content="cover-image"/>`
	b.opfRaw = []byte(strings.Replace(string(b.opfRaw), "</metadata>",
		"    "+metaTag+"\n  </metadata>", 1))
}

// updateCoverFile writes new image bytes for an existing manifest cover item
// and updates its media-type if it changed (e.g. JPG → PNG).
func (b *EpubBook) updateCoverFile(item *ManifestItem, data []byte, mediaType string) {
	oldHref := b.resolveHref(item.Href)

	ext := ".jpg"
	if mediaType == "image/png" {
		ext = ".png"
	}

	// Keep the same filename when possible; only rename if the extension changed.
	newHref := item.Href
	if !strings.HasSuffix(strings.ToLower(item.Href), ext) {
		newHref = strings.TrimSuffix(item.Href, path.Ext(item.Href)) + ext
	}
	newZipPath := b.resolveHref(newHref)
	if newZipPath == "" {
		newZipPath = oldHref // fallback
	}

	// Remove old file if we renamed it.
	if oldHref != newZipPath && oldHref != "" {
		delete(b.files, oldHref)
		b.removed[oldHref] = true
	}

	item.Href = newHref
	item.MediaType = mediaType
	b.files[newZipPath] = data
}

// SpineItemInfo holds display info for a spine item.
type SpineItemInfo struct {
	ID    string
	Title string
	Href  string
}

// GetSpineItems returns spine items with titles resolved from the TOC.
func (b *EpubBook) GetSpineItems() []SpineItemInfo {
	tocTitles := CollectTOCTitles(b.TOC)
	var items []SpineItemInfo

	for _, s := range b.Spine {
		mi := b.GetManifestItem(s.IDRef)
		if mi == nil {
			continue
		}
		// Skip NCX and nav documents
		if mi.MediaType == "application/x-dtbncx+xml" {
			continue
		}

		title := tocTitles[mi.Href]
		if title == "" {
			title = path.Base(mi.Href)
		}

		items = append(items, SpineItemInfo{
			ID:    mi.ID,
			Title: title,
			Href:  mi.Href,
		})
	}
	return items
}
