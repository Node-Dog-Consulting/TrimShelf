package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestXmlEscape(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "hello"},
		{"a & b", "a &amp; b"},
		{"<tag>", "&lt;tag&gt;"},
		{`he said "hi"`, `he said &quot;hi&quot;`},
		{"a & <b>", "a &amp; &lt;b&gt;"},
	}
	for _, tt := range tests {
		got := xmlEscape(tt.input)
		if got != tt.want {
			t.Errorf("xmlEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractText(t *testing.T) {
	xhtml := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Test</title></head>
<body>
<h1>Title</h1>
<p>First paragraph.</p>
<p>Second paragraph.</p>
</body>
</html>`)

	text := ExtractText(xhtml)
	if !strings.Contains(text, "Title") {
		t.Error("expected text to contain 'Title'")
	}
	if !strings.Contains(text, "First paragraph.") {
		t.Error("expected text to contain 'First paragraph.'")
	}
	if !strings.Contains(text, "Second paragraph.") {
		t.Error("expected text to contain 'Second paragraph.'")
	}
}

func TestExtractTextInvalidXHTML(t *testing.T) {
	text := ExtractText([]byte("not valid xhtml at all"))
	if text == "" {
		t.Error("expected non-empty fallback text for invalid input")
	}
}

func TestRebuildBody(t *testing.T) {
	original := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Test</title></head>
<body>
<p>Old content.</p>
</body>
</html>`)

	result := RebuildBody(original, "New paragraph one.\n\nNew paragraph two.")
	s := string(result)
	if !strings.Contains(s, "New paragraph one.") {
		t.Error("expected rebuilt body to contain 'New paragraph one.'")
	}
	if !strings.Contains(s, "New paragraph two.") {
		t.Error("expected rebuilt body to contain 'New paragraph two.'")
	}
	if strings.Contains(s, "Old content.") {
		t.Error("rebuilt body should not contain old content")
	}
}

func TestCollectTOCTitles(t *testing.T) {
	entries := []TOCEntry{
		{Title: "Chapter 1", Href: "ch1.xhtml"},
		{Title: "Chapter 2", Href: "ch2.xhtml#section1"},
		{Title: "Parent", Href: "parent.xhtml", Children: []TOCEntry{
			{Title: "Child", Href: "child.xhtml"},
		}},
	}

	titles := CollectTOCTitles(entries)
	if titles["ch1.xhtml"] != "Chapter 1" {
		t.Errorf("ch1.xhtml = %q, want 'Chapter 1'", titles["ch1.xhtml"])
	}
	if titles["ch2.xhtml"] != "Chapter 2" {
		t.Errorf("ch2.xhtml = %q, want 'Chapter 2'", titles["ch2.xhtml"])
	}
	if titles["child.xhtml"] != "Child" {
		t.Errorf("child.xhtml = %q, want 'Child'", titles["child.xhtml"])
	}
	if titles["parent.xhtml"] != "Parent" {
		t.Errorf("parent.xhtml = %q, want 'Parent'", titles["parent.xhtml"])
	}
}

func TestFilterTOC(t *testing.T) {
	entries := []TOCEntry{
		{Title: "Keep", Href: "keep.xhtml"},
		{Title: "Remove", Href: "remove.xhtml", Children: []TOCEntry{
			{Title: "Child", Href: "child.xhtml"},
		}},
		{Title: "Also Keep", Href: "also.xhtml"},
	}

	removeHrefs := map[string]bool{"remove.xhtml": true}
	result := filterTOC(entries, removeHrefs)

	if len(result) != 3 {
		t.Fatalf("expected 3 entries (child promoted), got %d", len(result))
	}
	if result[0].Title != "Keep" {
		t.Errorf("result[0] = %q, want 'Keep'", result[0].Title)
	}
	if result[1].Title != "Child" {
		t.Errorf("result[1] = %q, want 'Child' (promoted)", result[1].Title)
	}
	if result[2].Title != "Also Keep" {
		t.Errorf("result[2] = %q, want 'Also Keep'", result[2].Title)
	}
}

func TestWriteNavPoints(t *testing.T) {
	entries := []TOCEntry{
		{Title: "Chapter 1", Href: "ch1.xhtml"},
		{Title: "Chapter 2", Href: "ch2.xhtml", Children: []TOCEntry{
			{Title: "Section 2.1", Href: "ch2.xhtml#s1"},
		}},
	}

	var buf bytes.Buffer
	order := writeNavPoints(&buf, entries, "  ", 1)

	s := buf.String()
	if !strings.Contains(s, `id="nav_0"`) {
		t.Error("expected nav_0 in output")
	}
	if !strings.Contains(s, `playOrder="1"`) {
		t.Error("expected playOrder=1")
	}
	if !strings.Contains(s, "Chapter 1") {
		t.Error("expected 'Chapter 1' in output")
	}
	if !strings.Contains(s, "Section 2.1") {
		t.Error("expected 'Section 2.1' in output")
	}
	if order != 4 {
		t.Errorf("expected final order=4, got %d", order)
	}
}

func TestWriteNavPointsFreshCounter(t *testing.T) {
	entries := []TOCEntry{{Title: "A", Href: "a.xhtml"}}

	// Call twice — counter should reset each time (no global state)
	var buf1 bytes.Buffer
	writeNavPoints(&buf1, entries, "", 1)
	var buf2 bytes.Buffer
	writeNavPoints(&buf2, entries, "", 1)

	if buf1.String() != buf2.String() {
		t.Error("expected identical output from two calls (no shared counter state)")
	}
}

func TestValidZipPath(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		valid bool
	}{
		{"normal path", "OEBPS/chapter1.xhtml", true},
		{"root file", "mimetype", true},
		{"nested path", "META-INF/container.xml", true},
		{"parent traversal", "../etc/passwd", false},
		{"mid traversal", "OEBPS/../../etc/passwd", false},
		{"absolute path", "/etc/passwd", false},
		{"backslash absolute", "\\etc\\passwd", false},
		{"dot path", "./file.txt", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validZipPath(tt.path)
			if got != tt.valid {
				t.Errorf("validZipPath(%q) = %v, want %v", tt.path, got, tt.valid)
			}
		})
	}
}

func TestExtractPackageAttrs(t *testing.T) {
	tests := []struct {
		name string
		opf  string
		want string
	}{
		{
			name: "standard EPUB3",
			opf:  `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">`,
			want: `xmlns="http://www.idpf.org/2007/opf" version="3.0"`,
		},
		{
			name: "self-closing package",
			opf:  `<package xmlns="http://www.idpf.org/2007/opf" version="2.0"/>`,
			want: `xmlns="http://www.idpf.org/2007/opf" version="2.0"`,
		},
		{
			name: "no package tag",
			opf:  `<?xml version="1.0"?>`,
			want: `xmlns="http://www.idpf.org/2007/opf" version="3.0"`,
		},
		{
			name: "extra namespaces",
			opf:  `<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">`,
			want: `xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPackageAttrs([]byte(tt.opf))
			if got != tt.want {
				t.Errorf("extractPackageAttrs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractXMLBlock(t *testing.T) {
	opf := `<package><metadata><dc:title>Test</dc:title></metadata><manifest></manifest></package>`
	meta := extractXMLBlock(opf, "metadata")
	if !strings.Contains(meta, "dc:title") {
		t.Errorf("expected metadata block to contain dc:title, got %q", meta)
	}

	missing := extractXMLBlock(opf, "guide")
	if missing != "" {
		t.Errorf("expected empty string for missing block, got %q", missing)
	}
}
