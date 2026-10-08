package pdf

import (
	"bytes"
	"regexp"
	"strconv"
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

// mm 把毫米换算成 PDF 点，保留两位小数，与写出格式一致。
func mm(v float64) string { return strconv.FormatFloat(v*ptPerMm, 'f', 2, 64) }

// destOf 返回名称树里 key 对应的目的地数组，去掉空白便于断言。
func destOf(t *testing.T, out, key string) string {
	t.Helper()
	return strings.Join(strings.Fields(anchorDest(t, out, key)), " ")
}

// destFields 返回名称树里 key 对应的目的地数组，已去掉 /D 前缀并按空白拆开。
func destFields(t *testing.T, out, key string) []string {
	t.Helper()
	raw := strings.TrimSpace(anchorDest(t, out, key))
	raw = strings.TrimPrefix(raw, "/D")
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "["))
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "]"))
	return strings.Fields(raw)
}

// checkDest 逐项比较目的地：名称精确匹配，数值按容差比较。写出时的浮点位数由
// canvas.Precision 与 minify.Decimal 决定，不便按字符串断言。
func checkDest(t *testing.T, got []string, wantKind string, wantNums ...float64) {
	t.Helper()
	// 目的地数组形如 [N 0 R /Kind num...]，页引用占三项。
	if len(got) != 4+len(wantNums) {
		t.Fatalf("目的地 = %v，期望 %d 项", got, 4+len(wantNums))
	}
	if got[1] != "0" || got[2] != "R" {
		t.Fatalf("前三项应为页引用，实际 %v", got[:3])
	}
	if got[3] != "/"+wantKind {
		t.Fatalf("目的地类型 = %q，期望 /%s", got[3], wantKind)
	}
	for i, want := range wantNums {
		token := got[4+i]
		num, err := strconv.ParseFloat(token, 64)
		if err != nil {
			t.Fatalf("第 %d 项 %q 不是数值", 4+i, token)
		}
		if diff := num - want; diff > 0.01 || diff < -0.01 {
			t.Fatalf("第 %d 项 = %v，期望 %v", 4+i, num, want)
		}
	}
}

// TestAddDestToPageWritesExplicitKinds 验证 AddDestToPage 按显式类型写出目的地，
// 不像 AddAnchor 那样从矩形形状反推。
func TestAddDestToPageWritesExplicitKinds(t *testing.T) {
	const height = 297.0
	mm := func(v float64) float64 { return v * ptPerMm }
	cases := []struct {
		name string
		dest Dest
		kind string
		nums []float64
	}{
		{"fit", Dest{Kind: DestFit}, "Fit", nil},
		// 距页顶 100mm → 距页底 197mm。
		{"fith", Dest{Kind: DestFitH, Y: 100}, "FitH", []float64{mm(197)}},
		{"fitv", Dest{Kind: DestFitV, X: 50}, "FitV", []float64{mm(50)}},
		{
			"xyz", Dest{Kind: DestXYZ, X: 50, Y: 100}, "XYZ",
			[]float64{mm(50), mm(197), 0},
		},
		{"fith-top", Dest{Kind: DestFitH, Y: 0}, "FitH", []float64{mm(297)}},
		// 这两种正是 AddAnchor 无法表达的：x 为 0 的 XYZ 曾被反推成 FitH，
		// 而 FitV 的 x 为 0 曾被反推成整页 Fit。
		{"xyz-origin-x", Dest{Kind: DestXYZ, X: 0, Y: 100}, "XYZ", []float64{0, mm(197), 0}},
		{"fitv-origin-x", Dest{Kind: DestFitV, X: 0}, "FitV", []float64{0}},
		{
			"fitr", Dest{Kind: DestFitR, X0: 10, Y0: 60, X1: 110, Y1: 20}, "FitR",
			[]float64{mm(10), mm(277), mm(110), mm(237)},
		},
	}
	for _, c := range cases {
		out := buildAnnotatedPDF(t, 1, func(doc *PDF, index int) {
			doc.AddDestToPage(0, height, c.name, c.dest)
			doc.AddLink("#"+c.name, canvas.Rect{X0: 10, Y0: 150, X1: 80, Y1: 165})
		})
		t.Run(c.name, func(t *testing.T) {
			checkDest(t, destFields(t, out, c.name), c.kind, c.nums...)
		})
	}
}

// TestAddDestToPageKeepsZoom 验证 Zoom 不会被写成 0。
//
// AddAnchor 从矩形形状反推目的地类型，XYZ 的缩放被硬编码为 0；显式 API 必须
// 原样写出。
func TestAddDestToPageKeepsZoom(t *testing.T) {
	for _, zoom := range []float64{2.5, 1, 0.5} {
		out := buildAnnotatedPDF(t, 1, func(doc *PDF, index int) {
			doc.AddDestToPage(0, 297, "zoomed", Dest{Kind: DestXYZ, X: 10, Y: 20, Zoom: zoom})
			doc.AddLink("#zoomed", canvas.Rect{X0: 10, Y0: 150, X1: 80, Y1: 165})
		})
		checkDest(t, destFields(t, out, "zoomed"), "XYZ", 10*ptPerMm, (297-20)*ptPerMm, zoom*ptPerMm)
	}
}

// TestAddDestToPageOutOfRangeIsDropped 验证目标页未写出时该目的地被丢弃，且不影响
// 同一文档里其它内容。
func TestAddDestToPageOutOfRangeIsDropped(t *testing.T) {
	out := buildAnnotatedPDF(t, 1, func(doc *PDF, index int) {
		doc.AddDestToPage(3, 297, "dangling", Dest{Kind: DestFit})
		doc.AddDestToPage(0, 297, "valid", Dest{Kind: DestFit})
		doc.AddLink("#dangling", canvas.Rect{X0: 10, Y0: 150, X1: 80, Y1: 165})
		doc.AddLink("#valid", canvas.Rect{X0: 10, Y0: 200, X1: 80, Y1: 215})
	})
	if !strings.Contains(out, "%%EOF") {
		t.Fatalf("Close 未完成文档:\n%s", out)
	}
	// 名称树以 "(name) N 0 R" 的形式出现；链接注解里的 /Dest 也会提到这个名字，
	// 所以只检查名称树本身。
	if strings.Contains(out, "(dangling) ") {
		t.Fatalf("悬空目的地被写入名称树:\n%s", out)
	}
	checkDest(t, destFields(t, out, "valid"), "Fit")
}
