//go:build js && wasm

package rasterizer

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/vector"

	"github.com/tdewolff/canvas"
)

func Draw(c *canvas.Canvas, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, int(c.W*resolution.DPMM()+0.5), int(c.H*resolution.DPMM()+0.5)))
	ras := FromImage(img, resolution, colorSpace)
	c.RenderTo(ras)
	ras.Close()
	return img
}

type Rasterizer struct {
	draw.Image
	resolution canvas.Resolution
	colorSpace canvas.ColorSpace
	raster     *vector.Rasterizer
}

func New(width, height float64, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *Rasterizer {
	img := image.NewRGBA(image.Rect(0, 0, int(width*resolution.DPMM()+0.5), int(height*resolution.DPMM()+0.5)))
	return FromImage(img, resolution, colorSpace)
}

func FromImage(img draw.Image, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *Rasterizer {
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		panic("raster size is zero, increase resolution")
	}
	if math.MaxInt32/bounds.Dx() < bounds.Dy() {
		panic("raster size overflow, decrease resolution")
	}
	if colorSpace == nil {
		colorSpace = canvas.DefaultColorSpace
	}
	return &Rasterizer{
		Image: img, resolution: resolution, colorSpace: colorSpace,
		raster: vector.NewRasterizer(bounds.Dx(), bounds.Dy()),
	}
}

func (r *Rasterizer) Close() {
	if _, ok := r.colorSpace.(canvas.LinearColorSpace); !ok {
		changeColorSpace(r.Image, r.Image, r.colorSpace.FromLinear)
	}
}

func (r *Rasterizer) Reset() {
	if img, ok := r.Image.(*image.RGBA); ok {
		clear(img.Pix)
	} else {
		draw.Draw(r.Image, r.Image.Bounds(), image.Transparent, image.Point{}, draw.Src)
	}
	r.raster.Reset(r.Bounds().Dx(), r.Bounds().Dy())
}

func (r *Rasterizer) SetOp(op draw.Op) { r.raster.DrawOp = op }

func (r *Rasterizer) Size() (float64, float64) {
	size := r.Bounds().Size()
	return float64(size.X) / r.resolution.DPMM(), float64(size.Y) / r.resolution.DPMM()
}

func (r *Rasterizer) RenderPath(path *canvas.Path, style canvas.Style, m canvas.Matrix) {
	var fill, stroke *canvas.Path
	if style.HasFill() {
		fill = path.Copy().Transform(m)
	}
	if style.HasStroke() {
		tolerance := canvas.PixelTolerance / r.resolution.DPMM()
		stroke = path
		if len(style.Dashes) > 0 {
			dashOffset, dashes := canvas.ScaleDash(style.StrokeWidth, style.DashOffset, style.Dashes)
			stroke = stroke.Dash(dashOffset, dashes...)
		}
		stroke = stroke.Stroke(style.StrokeWidth, style.StrokeCapper, style.StrokeJoiner, tolerance).Transform(m)
	}
	if style.HasFill() {
		if style.Fill.IsPattern() {
			if hatch, ok := style.Fill.Pattern.(*canvas.HatchPattern); ok {
				style.Fill = hatch.Fill
			} else {
				style.Fill.Pattern.Transform(m).SetColorSpace(r.colorSpace).RenderTo(r, fill)
				style.Fill = canvas.Paint{}
			}
		}
		r.drawPath(fill, style.Fill, style.FillRule, m)
	}
	if style.HasStroke() {
		if style.Stroke.IsPattern() {
			if hatch, ok := style.Stroke.Pattern.(*canvas.HatchPattern); ok {
				style.Stroke = hatch.Fill
			} else {
				style.Stroke.Pattern.Transform(m).SetColorSpace(r.colorSpace).RenderTo(r, stroke)
				style.Stroke = canvas.Paint{}
			}
		}
		r.drawPath(stroke, style.Stroke, canvas.NonZero, m)
	}
}

func (r *Rasterizer) drawPath(path *canvas.Path, paint canvas.Paint, rule canvas.FillRule, m canvas.Matrix) {
	if !paint.IsColor() && !paint.IsGradient() {
		return
	}
	r.raster.Reset(r.Bounds().Dx(), r.Bounds().Dy())
	path.ToVectorRasterizer(r.raster, r.resolution)
	var src image.Image
	if paint.IsGradient() {
		src = gradientImage{gradient: paint.Gradient.SetColorSpace(r.colorSpace), inverse: m.Inv(), resolution: r.resolution, height: r.Bounds().Dy()}
	} else {
		src = image.NewUniform(r.colorSpace.ToLinear(paint.Color))
	}
	r.raster.Draw(r.Image, r.Bounds(), src, image.Point{})
}

func (r *Rasterizer) RenderText(text *canvas.Text, m canvas.Matrix) {
	text.RenderTo(r, m, r.resolution)
}

func (r *Rasterizer) RenderImage(img image.Image, m canvas.Matrix) {
	margin := 0
	if (m[0][1] != 0 || m[1][0] != 0) && (m[0][0] != 0 || m[1][1] == 0) {
		margin = 4
		size, sp := img.Bounds().Size(), img.Bounds().Min
		img2 := image.NewRGBA(image.Rect(0, 0, size.X+margin*2, size.Y+margin*2))
		draw.Draw(img2, image.Rect(margin, margin, size.X+margin, size.Y+margin), img, sp, draw.Over)
		img = img2
	}
	if _, ok := r.colorSpace.(canvas.LinearColorSpace); !ok {
		changeColorSpace(img.(draw.Image), img, r.colorSpace.ToLinear)
	}
	dpmm := r.resolution.DPMM()
	origin := m.Dot(canvas.Point{-float64(margin), float64(img.Bounds().Size().Y - margin)}).Mul(dpmm)
	m = m.Scale(dpmm, dpmm)
	h := float64(r.Bounds().Size().Y)
	aff3 := f64.Aff3{m[0][0], -m[0][1], origin.X, -m[1][0], m[1][1], h - origin.Y}
	draw.CatmullRom.Transform(r, aff3, img, img.Bounds(), draw.Over, nil)
}

type gradientImage struct {
	gradient   canvas.Gradient
	inverse    canvas.Matrix
	resolution canvas.Resolution
	height     int
}

func (g gradientImage) ColorModel() color.Model { return color.RGBAModel }
func (g gradientImage) Bounds() image.Rectangle { return image.Rect(0, 0, 1<<30, g.height) }
func (g gradientImage) At(x, y int) color.Color {
	p := g.inverse.Dot(canvas.Point{
		X: float64(x) / g.resolution.DPMM(),
		Y: float64(g.height-y) / g.resolution.DPMM(),
	})
	return g.gradient.At(p.X, p.Y)
}
