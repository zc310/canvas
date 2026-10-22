package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/tdewolff/test"
	"github.com/zc310/canvas"
	cimage "github.com/zc310/canvas/image"
)

func TestPDFClippedImageKeepsNativeClipAndOpaqueImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var output bytes.Buffer
	r := New(&output, 30, 20, &Options{Compress: false})
	r.RenderClippedImage(img, canvas.Identity.Translate(2, 3), canvas.Rectangle(8, 10).Translate(4, 0))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	if !bytes.Contains(data, []byte("W n")) {
		t.Fatal("PDF stream is missing the native path clipping operator")
	}
	if bytes.Contains(data, []byte("/SMask")) {
		t.Fatal("opaque clipped image should not require an SMask")
	}
}

func TestPDF(t *testing.T) {
	c := canvas.New(10, 10)
	c.RenderPath(canvas.MustParseSVGPath("L10 0"), canvas.DefaultStyle, canvas.Identity)

	//	pdfCompress = false
	//	buf := &bytes.Buffer{}
	//	c.WritePDF(buf)
	//	test.T(t, buf.String(), `%PDF-1.7
	//1 0 obj
	//<< /Length 14 >> stream
	//0 0 m 10 0 l f
	//endstream
	//endobj
	//2 0 obj
	//<< /Type /Page /Contents 1 0 R /Group << /Type /Group /CS /DeviceRGB /I true /S /Transparency >> /MediaBox [0 0 10 10] /Parent 2 0 R /Resources << >> >>
	//endobj
	//3 0 obj
	//<< /Type /Pages /Count 1 /Kids [2 0 R] >>
	//endobj
	//4 0 obj
	//<< /Type /Catalog /Pages 3 0 R >>
	//endobj
	//xref
	//0 5
	//0000000000 65535 f
	//0000000009 00000 n
	//0000000073 00000 n
	//0000000241 00000 n
	//0000000298 00000 n
	//trailer
	//<< /Root 4 0 R /Size 4 >>
	//starxref
	//347
	//%%EOF`)
}

func TestPDFPath(t *testing.T) {
	buf := &bytes.Buffer{}
	pdf := newPDFWriter(buf).NewPage(210.0, 297.0)
	pdf.SetAlpha(0.5)
	pdf.SetFill(canvas.Paint{Color: canvas.Red}, canvas.Identity)
	pdf.SetStroke(canvas.Paint{Color: canvas.Blue}, canvas.Identity)
	pdf.SetLineWidth(5.0)
	pdf.SetLineCap(canvas.RoundCap)
	pdf.SetLineJoin(canvas.RoundJoin)
	pdf.SetDashes(2.0, []float64{1.0, 2.0, 3.0})
	test.String(t, pdf.String(), " 2.8346457 0 0 2.8346457 0 0 cm /A0 gs 1 0 0 rg /A1 gs 0 0 1 RG 5 w 1 J 1 j [1 2 3 1 2 3] 2 d")
}

func TestPDFPatternDedup(t *testing.T) {
	buf := &bytes.Buffer{}
	pdf := newPDFWriter(buf).NewPage(210.0, 297.0)

	grad := canvas.NewLinearGradient(canvas.Point{0, 0}, canvas.Point{10, 0})
	grad.Grad = canvas.Grad{
		{Offset: 0.0, Color: color.RGBA{R: 255, A: 255}},
		{Offset: 1.0, Color: color.RGBA{B: 255, A: 255}},
	}
	m := canvas.Identity

	first := pdf.getPattern(grad, m)
	if second := pdf.getPattern(grad, m); second != first {
		t.Fatalf("same gradient should reuse pattern: %v and %v", first, second)
	}

	// 内容相同但对象不同的渐变也必须复用同一 pattern 资源。
	equal := canvas.NewLinearGradient(canvas.Point{0, 0}, canvas.Point{10, 0})
	equal.Grad = canvas.Grad{
		{Offset: 0.0, Color: color.RGBA{R: 255, A: 255}},
		{Offset: 1.0, Color: color.RGBA{B: 255, A: 255}},
	}
	if name := pdf.getPattern(equal, m); name != first {
		t.Fatalf("equal gradient should reuse pattern: %v and %v", first, name)
	}

	// 不同矩阵必须产生不同的 pattern。
	if name := pdf.getPattern(grad, canvas.Identity.Scale(2, 2)); name == first {
		t.Fatal("different matrix should not reuse pattern")
	}

	// 不同色标必须产生不同的 pattern。
	different := canvas.NewLinearGradient(canvas.Point{0, 0}, canvas.Point{10, 0})
	different.Grad = canvas.Grad{
		{Offset: 0.0, Color: color.RGBA{G: 255, A: 255}},
		{Offset: 1.0, Color: color.RGBA{B: 255, A: 255}},
	}
	if name := pdf.getPattern(different, m); name == first {
		t.Fatal("different gradient stops should not reuse pattern")
	}
}

const fontDir = "../../resources/"

func TestPDFText(t *testing.T) {
	t.Run("without_subset", func(t *testing.T) {
		testPDFText(t, false)
	})
	t.Run("with_subset", func(t *testing.T) {
		testPDFText(t, true)
	})
}

func testPDFText(t *testing.T, subsetFonts bool) {
	t.Helper()
	dejaVuSerif := canvas.NewFontFamily("dejavu-serif")
	if err := dejaVuSerif.LoadFontFile(fontDir+"DejaVuSerif.ttf", canvas.FontRegular); err != nil {
		t.Fatal(err)
	}

	ebGaramond := canvas.NewFontFamily("eb-garamond")
	if err := ebGaramond.LoadFontFile(fontDir+"EBGaramond12-Regular.otf", canvas.FontRegular); err != nil {
		t.Fatal(err)
	}

	dejaVu8 := dejaVuSerif.Face(8, canvas.Black, canvas.FontRegular, canvas.FontNormal)
	dejaVu12 := dejaVuSerif.Face(12, canvas.Red, canvas.FontRegular, canvas.FontNormal, canvas.FontUnderline)
	dejaVu12sub := dejaVuSerif.Face(12, canvas.Black, canvas.FontRegular, canvas.FontSubscript)
	garamond10 := ebGaramond.Face(10, canvas.Black, canvas.FontBold, canvas.FontNormal)

	rt := canvas.NewRichText(dejaVu12)
	rt.WriteFace(dejaVu8, "dejaVu8")
	rt.WriteFace(dejaVu12, " glyphspacing")
	rt.WriteFace(dejaVu12sub, " dejaVu12sub")
	rt.WriteFace(garamond10, " garamond10")
	text := rt.ToText(180, 20.0, canvas.Justify, canvas.Top, nil)

	buf := &bytes.Buffer{}
	pdf := New(buf, 210, 297, &Options{Compress: true, SubsetFonts: subsetFonts})
	pdf.RenderText(text, canvas.Identity.Translate(15, 250))
	if err := pdf.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) || !bytes.HasSuffix(buf.Bytes(), []byte("%%EOF\n")) {
		t.Fatal("PDF header or trailer missing")
	}
	if len(pdf.w.pdf.fontsH) != 2 {
		t.Fatalf("embedded fonts = %d, want TrueType and CFF", len(pdf.w.pdf.fontsH))
	}
	// Inspect the embedded programs instead of depending on compressor-specific
	// file sizes. PDF subsets may omit tables required by standalone font parsers.
	for _, source := range []*canvas.Font{dejaVu8.Font, garamond10.Font} {
		ref, ok := pdf.w.pdf.fontsH[source]
		if !ok {
			t.Fatalf("font %q was not embedded", source.Name())
		}
		program := pdfTextFontProgram(t, buf.Bytes(), pdf.w.pdf, ref)
		outline := "glyf"
		if source.IsCFF {
			outline = "CFF "
		}
		count := pdfTextFontGlyphCount(t, program, outline)
		if subsetFonts {
			if count <= 1 || count >= source.SFNT.NumGlyphs() {
				t.Errorf("font %q subset has %d glyphs, original has %d", source.Name(), count, source.SFNT.NumGlyphs())
			}
		} else if !bytes.Equal(program, source.SFNT.Write()) {
			t.Errorf("font %q was changed despite disabling subsetting", source.Name())
		}
	}
	t.Run("text extraction", func(t *testing.T) {
		tool, err := exec.LookPath("pdftotext")
		if err != nil {
			t.Skip("pdftotext unavailable; skipping text extraction check")
		}
		cmd := exec.CommandContext(t.Context(), tool, "-", "-")
		cmd.Stdin = bytes.NewReader(buf.Bytes())
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("pdftotext: %v: %s", err, output)
		}
		want := "dejaVu8 glyphspacing dejaVu12sub garamond10"
		if got := strings.Join(strings.Fields(string(output)), " "); got != want {
			t.Fatalf("extracted text = %q, want %q", got, want)
		}
	})
	if testing.Verbose() {
		path := filepath.Join(t.TempDir(), "text.pdf")
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("PDF saved to temporary test directory: %s", path)
	}
}

// Resolve the font descriptor's program reference using the writer's object
// offsets, and use the declared stream length to locate the binary data.
func pdfTextFontProgram(t *testing.T, data []byte, writer *pdfWriter, ref pdfRef) []byte {
	t.Helper()
	object := data[writer.objOffsets[ref-1]:]
	end := bytes.Index(object, []byte("\nendobj"))
	if end < 0 {
		t.Fatal("font object end missing")
	}
	match := regexp.MustCompile(`/FontFile[23] (\d+) 0 R`).FindSubmatch(object[:end])
	if len(match) != 2 {
		t.Fatal("font program reference missing")
	}
	programRef, err := strconv.Atoi(string(match[1]))
	if err != nil || programRef < 1 || programRef > len(writer.objOffsets) {
		t.Fatalf("invalid font program reference: %q", match[1])
	}
	object = data[writer.objOffsets[programRef-1]:]
	start := bytes.Index(object, []byte("stream\n"))
	if start < 0 {
		t.Fatal("font program stream missing")
	}
	match = regexp.MustCompile(`/Length (\d+)`).FindSubmatch(object[:start])
	if len(match) != 2 || !bytes.Contains(object[:start], []byte("/Filter/FlateDecode")) {
		t.Fatal("compressed font stream length or filter missing")
	}
	length, err := strconv.Atoi(string(match[1]))
	start += len("stream\n")
	if err != nil || length <= 0 || length > len(object)-start {
		t.Fatalf("invalid font stream length: %q", match[1])
	}
	reader, err := zlib.NewReader(bytes.NewReader(object[start : start+length]))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	program, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// Read maxp directly: PDF subsets do not need standalone-font metadata such as
// name or post tables. Also check table bounds and the expected outline format.
func pdfTextFontGlyphCount(t *testing.T, program []byte, outline string) uint16 {
	t.Helper()
	if len(program) < 12 {
		t.Fatal("font program header missing")
	}
	tables := int(binary.BigEndian.Uint16(program[4:6]))
	if tables == 0 || tables > (len(program)-12)/16 {
		t.Fatal("invalid font table directory")
	}
	var count uint16
	hasOutline := false
	for i := 0; i < tables; i++ {
		record := program[12+i*16 : 12+(i+1)*16]
		offset := uint64(binary.BigEndian.Uint32(record[8:12]))
		length := uint64(binary.BigEndian.Uint32(record[12:16]))
		if offset+length > uint64(len(program)) {
			t.Fatalf("font table %q exceeds program bounds", record[:4])
		}
		if string(record[:4]) == outline && length > 0 {
			hasOutline = true
		}
		if string(record[:4]) == "maxp" && length >= 6 {
			count = binary.BigEndian.Uint16(program[offset+4 : offset+6])
		}
	}
	if count == 0 || !hasOutline {
		t.Fatalf("font has %d glyphs, %s outlines present=%v", count, outline, hasOutline)
	}
	return count
}

func TestPDFImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	buf := &bytes.Buffer{}
	pdf := newPDFWriter(buf).NewPage(210.0, 297.0)
	pdf.DrawImage(img, cimage.Lossless, canvas.Identity)
	test.String(t, pdf.String(), " 2.8346457 0 0 2.8346457 0 0 cm q 0 0 2 2 re W n 0 0 m 0 2 l 2 2 l 2 0 l h W n 2 0 0 2 0 0 cm /Im0 Do Q")
}

// A PNG image stream is already zlib data: the PDF should declare FlateDecode and write
// the bytes verbatim. Compressing it again leaves the decoder with the inner zlib
// stream and the image renders blank (the QR code in zsbk.ofd hit this).
func TestPDFPNGImageStreamIsSingleCompressed(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 0, A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	cimg := &cimage.Image{
		Bytes:    pngBuf.Bytes(),
		Mimetype: "image/png",
		Config:   image.Config{ColorModel: color.RGBAModel, Width: 4, Height: 4},
	}

	buf := &bytes.Buffer{}
	pdf := newPDFWriter(buf)
	pdf.NewPage(50, 50).DrawImage(cimg, cimage.Lossless, canvas.Identity)
	if err := pdf.Close(); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()

	marker := []byte("/Predictor 15")
	at := bytes.Index(out, marker)
	if at < 0 {
		t.Fatalf("PNG passthrough image dict (Predictor 15) not found")
	}
	// When Filter is an array, DecodeParms must be an array too; otherwise readers
	// ignore Predictor and the image is skewed.
	if !bytes.Contains(out, []byte("/DecodeParms[<<")) {
		t.Fatalf("DecodeParms must be an array when Filter is an array")
	}
	start := bytes.Index(out[at:], []byte("stream\n"))
	if start < 0 {
		t.Fatal("image stream start not found")
	}
	start += at + len("stream\n")
	end := bytes.Index(out[start:], []byte("\nendstream"))
	if end < 0 {
		t.Fatal("image stream end not found")
	}
	reader, err := zlib.NewReader(bytes.NewReader(out[start : start+end]))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	// A single inflate must yield exactly the PNG filtered scanlines: height*(1+width*3).
	want := 4 * (1 + 4*3)
	if len(decoded) != want {
		t.Fatalf("inflated image stream length = %d, want %d (looks double-compressed)", len(decoded), want)
	}
}

func TestPDFMultipage(t *testing.T) {
	buf := &bytes.Buffer{}
	pdf := New(buf, 210, 297, nil)
	pdf.NewPage(210, 297)
	err := pdf.Close()
	test.Error(t, err)
	out := buf.String()

	test.That(t, strings.Contains(out, "/Type/Pages/Count 2"), `could not find "/Type /Pages /Count 2" in output`)

	nbPages := strings.Count(out, "/Type/Page/")
	test.That(t, nbPages == 2, "expected 2 pages, got", nbPages)
}

func TestPDFMetadata(t *testing.T) {
	buf := &bytes.Buffer{}
	pdf := New(buf, 210, 297, nil)
	pdf.NewPage(210, 297)
	pdf.SetInfo("a1", "b2", "c3", "d4", "e5")
	err := pdf.Close()
	test.Error(t, err)
	out := buf.String()

	test.That(t, strings.Contains(out, "/Title(a1)"), `could not find "/Title (a1)" in output`)
	test.That(t, strings.Contains(out, "/Subject(b2)"), `could not find "/Subject (b2)" in output`)
	test.That(t, strings.Contains(out, "/Keywords(c3)"), `could not find "/Keywords (c3)" in output`)
	test.That(t, strings.Contains(out, "/Author(d4)"), `could not find "/Author (d4)" in output`)
	test.That(t, strings.Contains(out, "/Creator(e5)"), `could not find "/Creator (e5)" in output`)
}
