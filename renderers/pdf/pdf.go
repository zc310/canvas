package pdf

import (
	"fmt"
	"image"
	"io"
	"math"

	"github.com/tdewolff/canvas"
	cimage "github.com/tdewolff/canvas/image"
)

type Options struct {
	Compress    bool
	SubsetFonts bool
	cimage.ImageEncoding
}

var DefaultOptions = Options{
	Compress:      true,
	SubsetFonts:   true,
	ImageEncoding: cimage.Lossless,
}

// PDF is a portable document format renderer.
type PDF struct {
	w             *pdfPageWriter
	width, height float64
	opts          *Options
}

// New returns a portable document format (PDF) renderer.
func New(w io.Writer, width, height float64, opts *Options) *PDF {
	if opts == nil {
		defaultOptions := DefaultOptions
		opts = &defaultOptions
	}

	page := newPDFWriter(w).NewPage(width, height)
	page.pdf.SetCompression(opts.Compress)
	page.pdf.SetFontSubsetting(opts.SubsetFonts)
	return &PDF{
		w:      page,
		width:  width,
		height: height,
		opts:   opts,
	}
}

// SetImageEncoding sets the image encoding to Loss or Lossless.
func (r *PDF) SetImageEncoding(enc cimage.ImageEncoding) {
	r.opts.ImageEncoding = enc
}

// SetInfo sets the document's title, subject, keywords, author and creator.
func (r *PDF) SetInfo(title, subject, keywords, author, creator string) {
	r.w.pdf.SetTitle(title)
	r.w.pdf.SetSubject(subject)
	r.w.pdf.SetKeywords(keywords)
	r.w.pdf.SetAuthor(author)
	r.w.pdf.SetCreator(creator)
}

// SetLang sets the document's language. It must adhere the RFC 3066 specification on Language-Tag, eg. es-CL.
func (r *PDF) SetLang(lang string) {
	r.w.pdf.SetLang(lang)
}

// NewPage starts adds a new page where further rendering will be written to.
func (r *PDF) NewPage(width, height float64) {
	r.w = r.w.pdf.NewPage(width, height)
}

// AddAnchor adds an anchor that can be referenced by a link (see AddLink). The rectangle is the area to be referenced. If the width and
// height are zero and the X and Y positions are zero, it will fit the entire page. If either width/height and X/Y are zero then it will
// fit the page's height/width and scroll to the X/Y position. Otherwise, if the width and height are zero and X and Y are not zero, it
// will scroll to the position but not change it's zoom.
func (r *PDF) AddAnchor(name string, rect canvas.Rect) {
	r.w.AddAnchor(name, rect)
}

// AddAnchorToPage adds an anchor bound to the given page index instead of the current
// page. The destination name tree is only built in Close, and the page references it
// indexes are known by then, so an anchor may be registered before its target page is
// created. Links that point backwards — a link on a later page targeting an earlier one —
// cannot use AddAnchor, which would bind them to whichever page happens to be current.
func (r *PDF) AddAnchorToPage(page int, name string, rect canvas.Rect) {
	r.w.pdf.anchors = append(r.w.pdf.anchors, pdfAnchor{page: page, name: name, rect: rect})
}

// AddDestToPage registers a named destination of an explicit kind on the given page,
// which a link can then point at with AddLink("#" + name).
//
// It is the unambiguous counterpart to AddAnchor, which infers the destination type
// from the shape of a rectangle and therefore cannot express, for example, an XYZ
// destination at x = 0 — indistinguishable from a FitH — or a non-zero zoom, which
// the inferred form always writes as zero. Prefer this whenever the destination type
// is known.
//
// pageHeight is the height of the target page in millimetres: Dest coordinates use
// the top left as their origin while PDF uses the bottom left, so the conversion
// needs the page height. It may differ from the current page's height, and passing
// the wrong value flips the destination vertically.
func (r *PDF) AddDestToPage(page int, pageHeight float64, name string, dest Dest) {
	r.w.pdf.anchors = append(r.w.pdf.anchors, pdfAnchor{
		page:     page,
		name:     name,
		dest:     &dest,
		pageSize: pageHeight,
	})
}

// AddLink adds a link at the given rectangle. If the URI starts with # this will link to an anchor (set with AddAnchor).
func (r *PDF) AddLink(uri string, rect canvas.Rect) {
	r.w.AddLink(uri, rect)
}

// AddOutline adds an outline element at the given y position. The top-level element must have level zero. If any level is missing, then
// higher level elements are ignored.
func (r *PDF) AddOutline(name string, level int, y float64) {
	r.w.AddOutline(name, level, y)
}

// AddOutlineToPage adds an outline entry of an explicit destination kind, bound to
// the given page rather than to the current one.
//
// It is the counterpart to AddAnchorToPage and AddDestToPage. AddOutline infers the
// destination from y alone, which can only express Fit and FitH; pass dest to state
// the kind directly. The outline tree is written in Close, so an entry may be
// registered before its target page exists; entries whose target page never gets
// written are dropped along with their descendants.
//
// level is the nesting depth, zero for a top-level entry, and entries are expected
// in document order — the tree is rebuilt from the levels, the way AddOutline does.
//
// pageHeight is the height of the target page in millimetres; see Dest.
func (r *PDF) AddOutlineToPage(page int, pageHeight float64, name string, level int, dest Dest) {
	r.w.pdf.outlines = append(r.w.pdf.outlines, pdfOutline{
		page:     page,
		name:     name,
		level:    level,
		dest:     &dest,
		pageSize: pageHeight,
		// The tree builder treats -1 as "unset"; the zero value would look like
		// a real reference to the first entry and make entries their own parent.
		parent: -1,
		prev:   -1,
		next:   -1,
		first:  -1,
		last:   -1,
	})
}

// Close finished and closes the PDF.
func (r *PDF) Close() error {
	return r.w.pdf.Close()
}

// Size returns the size of the canvas in millimeters.
func (r *PDF) Size() (float64, float64) {
	return r.width, r.height
}

// RenderPath renders a path to the canvas using a style and a transformation matrix.
func (r *PDF) RenderPath(path *canvas.Path, style canvas.Style, m canvas.Matrix) {
	// PDFs don't support the arcs joiner, miter joiner (not clipped), or miter joiner (clipped) with non-bevel fallback
	strokeUnsupported := false
	if _, ok := style.StrokeJoiner.(canvas.ArcsJoiner); ok {
		strokeUnsupported = true
	} else if miter, ok := style.StrokeJoiner.(canvas.MiterJoiner); ok {
		if math.IsNaN(miter.Limit) {
			strokeUnsupported = true
		} else if _, ok := miter.GapJoiner.(canvas.BevelJoiner); !ok {
			strokeUnsupported = true
		}
	}
	if !strokeUnsupported {
		if m.IsSimilarity() {
			scale := math.Sqrt(math.Abs(m.Det()))
			style.StrokeWidth *= scale
			style.DashOffset, style.Dashes = canvas.ScaleDash(style.StrokeWidth, style.DashOffset, style.Dashes)
		} else {
			strokeUnsupported = true
		}
	}

	// PDFs don't support connecting first and last dashes if path is closed, so we move the start of the path if this is the case
	// TODO: closing dashes
	//if style.DashesClose {
	//	strokeUnsupported = true
	//}

	closed := false
	data := path.Copy().Transform(m).ToPDF()
	if 1 < len(data) && data[len(data)-1] == 'h' {
		data = data[:len(data)-2]
		closed = true
	}

	if !style.HasStroke() || !strokeUnsupported {
		if style.HasFill() && !style.HasStroke() {
			r.w.SetFill(style.Fill, m)
			r.w.Write([]byte(" "))
			r.w.Write([]byte(data))
			r.w.Write([]byte(" f"))
			if style.FillRule == canvas.EvenOdd {
				r.w.Write([]byte("*"))
			}
		} else if !style.HasFill() && style.HasStroke() {
			r.w.SetStroke(style.Stroke, m)
			r.w.SetLineWidth(style.StrokeWidth)
			r.w.SetLineCap(style.StrokeCapper)
			r.w.SetLineJoin(style.StrokeJoiner)
			r.w.SetDashes(style.DashOffset, style.Dashes)
			r.w.Write([]byte(" "))
			r.w.Write([]byte(data))
			if closed {
				r.w.Write([]byte(" s"))
			} else {
				r.w.Write([]byte(" S"))
			}
		} else if style.HasFill() && style.HasStroke() {
			sameAlpha := style.Fill.IsColor() && style.Stroke.IsColor() && style.Fill.Color.A == style.Stroke.Color.A
			if sameAlpha {
				r.w.SetFill(style.Fill, m)
				r.w.SetStroke(style.Stroke, m)
				r.w.SetLineWidth(style.StrokeWidth)
				r.w.SetLineCap(style.StrokeCapper)
				r.w.SetLineJoin(style.StrokeJoiner)
				r.w.SetDashes(style.DashOffset, style.Dashes)
				r.w.Write([]byte(" "))
				r.w.Write([]byte(data))
				if closed {
					r.w.Write([]byte(" b"))
				} else {
					r.w.Write([]byte(" B"))
				}
				if style.FillRule == canvas.EvenOdd {
					r.w.Write([]byte("*"))
				}
			} else {
				r.w.SetFill(style.Fill, m)
				r.w.Write([]byte(" "))
				r.w.Write([]byte(data))
				r.w.Write([]byte(" f"))
				if style.FillRule == canvas.EvenOdd {
					r.w.Write([]byte("*"))
				}

				r.w.SetStroke(style.Stroke, m)
				r.w.SetLineWidth(style.StrokeWidth)
				r.w.SetLineCap(style.StrokeCapper)
				r.w.SetLineJoin(style.StrokeJoiner)
				r.w.SetDashes(style.DashOffset, style.Dashes)
				r.w.Write([]byte(" "))
				r.w.Write([]byte(data))
				if closed {
					r.w.Write([]byte(" s"))
				} else {
					r.w.Write([]byte(" S"))
				}
			}
		}
	} else {
		// style.HasStroke() && strokeUnsupported
		if style.HasFill() {
			r.w.SetFill(style.Fill, m)
			r.w.Write([]byte(" "))
			r.w.Write([]byte(data))
			r.w.Write([]byte(" f"))
			if style.FillRule == canvas.EvenOdd {
				r.w.Write([]byte("*"))
			}
		}

		// stroke settings unsupported by PDF, draw stroke explicitly
		if style.IsDashed() {
			path = path.Dash(style.DashOffset, style.Dashes...)
		}
		path = path.Stroke(style.StrokeWidth, style.StrokeCapper, style.StrokeJoiner, canvas.Tolerance)

		r.w.SetFill(style.Stroke, m)
		r.w.Write([]byte(" "))
		r.w.Write([]byte(path.Transform(m).ToPDF()))
		r.w.Write([]byte(" f"))
	}
}

// RenderText renders a text object to the canvas using a transformation matrix.
func (r *PDF) RenderText(text *canvas.Text, m canvas.Matrix) {
	text.RenderDecorationsTo(r, m, 0.0)

	text.WalkSpans(func(x, y float64, span canvas.TextSpan) {
		if span.IsText() {
			style := canvas.DefaultStyle
			style.Fill = span.Face.Fill

			r.w.StartTextObject()
			r.w.SetFill(span.Face.Fill, m)
			r.w.SetFont(span.Face.Font, span.Face.Size, span.Direction)
			r.w.SetTextPosition(m.Translate(x, y).Shear(span.Face.FauxItalic, 0.0))

			if 0.0 < span.Face.FauxBold {
				r.w.SetTextRenderMode(2)
				r.w.SetStroke(span.Face.Fill, m)
				fmt.Fprintf(r.w, " %v w", dec(span.Face.FauxBold*2.0))
			} else {
				r.w.SetTextRenderMode(0)
			}
			r.w.WriteText(text.WritingMode, span.Glyphs)
			r.w.EndTextObject()
		} else {
			for _, obj := range span.Objects {
				obj.Canvas.RenderViewTo(r, m.Mul(obj.View(x, y, span.Face)))
			}
		}
	})
}

// RenderImage renders an image to the canvas using a transformation matrix.
func (r *PDF) RenderImage(img image.Image, m canvas.Matrix) {
	r.w.DrawImage(img, r.opts.ImageEncoding, m)
}
