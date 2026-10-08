package pdf

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/tdewolff/canvas"
)

var pageObjPattern = regexp.MustCompile(`(\d+) 0 obj\s*<</Type/Page[^s]`)

// anchorRefPattern matches one name tree entry, such as "(to-second) 11 0 R".
var anchorRefPattern = regexp.MustCompile(`\(([A-Za-z0-9_.-]+)\)\s*(\d+) 0 R`)

// pageObjects returns the indirect references of the document's page objects, in
// document order, each formatted like "5 0 R".
func pageObjects(out string) []string {
	matches := pageObjPattern.FindAllStringSubmatch(out, -1)
	refs := make([]string, 0, len(matches))
	for _, m := range matches {
		refs = append(refs, m[1]+" 0 R")
	}
	return refs
}

// anchorDest returns the /D destination array that the name tree maps key onto.
//
// The name tree holds an indirect reference to a <</D [...]>> object rather than
// the array itself, so the object number has to be resolved first.
func anchorDest(t *testing.T, out, key string) string {
	t.Helper()
	for _, m := range anchorRefPattern.FindAllStringSubmatch(out, -1) {
		if m[1] != key {
			continue
		}
		marker := "\n" + m[2] + " 0 obj"
		idx := strings.Index(out, marker)
		if idx < 0 {
			t.Fatalf("anchor object %s not found", marker)
		}
		rest := out[idx:]
		at := strings.Index(rest, "/D")
		if at < 0 {
			t.Fatalf("anchor object has no /D:\n%s", rest)
		}
		rest = rest[at:]
		end := strings.Index(rest, "]")
		if end < 0 {
			t.Fatalf("destination array is not closed:\n%s", rest)
		}
		return rest[:end+1]
	}
	t.Fatalf("name %q not found in the name tree:\n%s", key, out)
	return ""
}

// buildAnnotatedPDF writes a PDF with the given number of pages, calling register
// after each page is rendered, and returns the raw bytes.
func buildAnnotatedPDF(t *testing.T, pages int, register func(doc *PDF, index int)) string {
	t.Helper()
	buf := &bytes.Buffer{}
	opts := DefaultOptions
	opts.Compress = false
	doc := New(buf, 210, 297, &opts)
	for index := range pages {
		surface := canvas.New(210, 297)
		surface.RenderTo(doc)
		if register != nil {
			register(doc, index)
		}
		if index < pages-1 {
			doc.NewPage(210, 297)
		}
	}
	if err := doc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestAddAnchorToPageBindsNotYetCreatedPage checks that an anchor can target a page
// that has not been written out yet.
//
// AddAnchor binds an anchor to whichever page is current, so it can only express a
// link whose target follows its source. Here a second-page anchor is registered
// while only the first page exists, and the name tree must still resolve it to the
// second page.
func TestAddAnchorToPageBindsNotYetCreatedPage(t *testing.T) {
	out := buildAnnotatedPDF(t, 2, func(doc *PDF, index int) {
		if index != 0 {
			return
		}
		// The first page has not been written yet; register an anchor for the
		// second one anyway.
		doc.AddAnchorToPage(1, "to-second", canvas.Rect{X0: 0, Y0: 100, X1: 50, Y1: 120})
		doc.AddLink("#to-second", canvas.Rect{X0: 10, Y0: 150, X1: 80, Y1: 165})
	})

	if !strings.Contains(out, "/Dest/to-second") {
		t.Fatalf("Dest is not written as a name object:\n%s", out)
	}
	pages := pageObjects(out)
	if len(pages) != 2 {
		t.Fatalf("page object count = %d, want 2:\n%s", len(pages), out)
	}
	if dest := anchorDest(t, out, "to-second"); !strings.Contains(dest, pages[1]) {
		t.Fatalf("to-second resolves to %q, want the second page %q", dest, pages[1])
	}
}

// TestAddAnchorBindsCurrentPage pins the existing AddAnchor behaviour, which binds
// to the current page, and covers how the destination is spelled.
func TestAddAnchorBindsCurrentPage(t *testing.T) {
	out := buildAnnotatedPDF(t, 1, func(doc *PDF, index int) {
		doc.AddAnchor("here", canvas.Rect{X0: 0, Y0: 100, X1: 50, Y1: 120})
		doc.AddLink("#here", canvas.Rect{X0: 10, Y0: 150, X1: 80, Y1: 165})
	})

	if !strings.Contains(out, "/Dest/here") {
		t.Fatalf("Dest is not written as a name object:\n%s", out)
	}
	pages := pageObjects(out)
	if len(pages) != 1 {
		t.Fatalf("page object count = %d, want 1:\n%s", len(pages), out)
	}
	if dest := anchorDest(t, out, "here"); !strings.Contains(dest, pages[0]) {
		t.Fatalf("here resolves to %q, want the first page %q", dest, pages[0])
	}
}

// TestAddAnchorToPageOutOfRangeIsDropped checks that an anchor naming a page that
// never gets written does not fail Close.
//
// AddAnchorToPage deliberately accepts a page index that does not exist yet, since
// the whole point is to register an anchor before its target page is created. The
// index only becomes checkable in Close, by which point a caller that stopped early
// may have left a dangling anchor behind. That must not panic.
func TestAddAnchorToPageOutOfRangeIsDropped(t *testing.T) {
	buf := &bytes.Buffer{}
	opts := DefaultOptions
	opts.Compress = false
	doc := New(buf, 210, 297, &opts)

	surface := canvas.New(210, 297)
	surface.RenderTo(doc)
	// Only one page is ever written, but the anchors name a second and a third.
	doc.AddAnchorToPage(1, "missing", canvas.Rect{X0: 0, Y0: 100, X1: 50, Y1: 120})
	doc.AddAnchorToPage(2, "also-missing", canvas.Rect{X0: 0, Y0: 100, X1: 50, Y1: 120})
	doc.AddLink("#missing", canvas.Rect{X0: 10, Y0: 150, X1: 80, Y1: 165})

	if err := doc.Close(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "%%EOF") {
		t.Fatalf("Close did not finish the document:\n%s", out)
	}
	if strings.Contains(out, "also-missing") {
		t.Fatalf("dangling anchor was written to the name tree:\n%s", out)
	}
}
