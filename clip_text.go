package canvas

import "image"

// Renderers without native text clipping use intersected outlines. The same
// transformation applies to both the text and its clipping path.
func renderClippedText(renderer Renderer, text *Text, matrix Matrix, clip *Path) {
	if text == nil || text.Empty() || clip == nil || clip.Empty() {
		return
	}

	clip = clip.Copy().Transform(matrix)
	text.RenderTo(clippedTextPaths{
		Renderer: renderer, clip: clip, bounds: clip.Bounds(), rectangle: clippedTextRectangle(clip),
	}, matrix, 0)
}

type clippedTextPaths struct {
	Renderer

	clip      *Path
	bounds    Rect
	rectangle bool
}

// Only an exact, single axis-aligned rectangle allows a bounds containment
// shortcut. A concave path or a path with holes can have the same bounding box.
func clippedTextRectangle(path *Path) bool {
	if len(path.d) != 20 || path.d[0] != MoveToCmd || path.d[16] != CloseCmd {
		return false
	}

	for i := 4; i < 16; i += 4 {
		if path.d[i] != LineToCmd {
			return false
		}
	}

	for i := 0; i < 16; i += 4 {
		j := (i + 4) % 16
		k := (i + 8) % 16
		vertical := path.d[i+1] == path.d[j+1]
		horizontal := path.d[i+2] == path.d[j+2]

		if vertical == horizontal || vertical == (path.d[j+1] == path.d[k+1]) {
			return false
		}
	}

	return true
}

func closedTextFill(path *Path, rule FillRule) *Path {
	// Filling implicitly closes every subpath, not just the last one. Copy
	// before closing because glyph and decoration paths may be shared.
	var closed *Path
	for _, subpath := range path.Split() {
		closed = closed.Append(subpath.Copy())
		closed.Close()
	}

	if rule == EvenOdd {
		closed = closed.Settle(EvenOdd)
	}

	return closed
}

func (r clippedTextPaths) RenderPath(path *Path, style Style, matrix Matrix) {
	if path == nil || path.Empty() || Equal(matrix.Det(), 0) {
		return
	}

	if !style.HasStroke() {
		bounds := path.Bounds().Transform(matrix)
		if !r.bounds.Touches(bounds) {
			return
		}
		if r.rectangle && r.bounds.Contains(bounds) {
			r.Renderer.RenderPath(path, style, matrix)

			return
		}
	}

	clip := r.clip.Copy().Transform(matrix.Inv())
	path = closedTextFill(path, style.FillRule).And(clip)
	style.FillRule = NonZero

	if !path.Empty() {
		r.Renderer.RenderPath(path, style, matrix)
	}
}

func (r clippedTextPaths) RenderText(text *Text, matrix Matrix) {
	text.RenderTo(r, matrix, 0)
}

func (r clippedTextPaths) RenderImage(img image.Image, matrix Matrix) {
	if Equal(matrix.Det(), 0) {
		return
	}

	clip := r.clip.Copy().Transform(matrix.Inv())
	if renderer, ok := r.Renderer.(ClippedImageRenderer); ok {
		renderer.RenderClippedImage(img, matrix, clip)
	} else {
		r.Renderer.RenderImage(clipImage(img, clip), matrix)
	}
}
