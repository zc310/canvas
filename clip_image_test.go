package canvas

import (
	"image"
	"image/color"
	"testing"
)

type clippedImageCapture struct{ image image.Image }

func (c *clippedImageCapture) Size() (float64, float64)              { return 20, 20 }
func (c *clippedImageCapture) RenderPath(*Path, Style, Matrix)       {}
func (c *clippedImageCapture) RenderText(*Text, Matrix)              {}
func (c *clippedImageCapture) RenderImage(img image.Image, _ Matrix) { c.image = img }

func TestCanvasClippedImageFallbackAppliesCoverage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i-3], img.Pix[i-2], img.Pix[i-1], img.Pix[i] = 200, 100, 50, 255
	}
	scene := New(20, 20)
	scene.RenderClippedImage(img, Identity, Rectangle(5, 10))
	renderer := &clippedImageCapture{}
	scene.RenderTo(renderer)
	if renderer.image == nil {
		t.Fatal("clipped image fallback was not rendered")
	}
	inside := color.NRGBAModel.Convert(renderer.image.At(2, 5)).(color.NRGBA)
	outside := color.NRGBAModel.Convert(renderer.image.At(8, 5)).(color.NRGBA)
	if inside.A != 255 || outside.A != 0 {
		t.Fatalf("inside=%v outside=%v, want opaque inside and transparent outside", inside, outside)
	}
}
