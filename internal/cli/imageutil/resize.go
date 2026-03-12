package imageutil

import (
	"bytes"
	"fmt"
	goimage "image"
	"image/jpeg"
	"image/png"
	"strings"

	// Register standard decoders.
	_ "image/gif"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

const (
	// MaxResizeWidth is the maximum width before downscaling.
	MaxResizeWidth = 2048
	// MaxResizeHeight is the maximum height before downscaling.
	MaxResizeHeight = 768
	// jpegQuality is the quality used when re-encoding as JPEG.
	jpegQuality = 85
)

// ResizeResult holds the output of ResizeIfNeeded.
type ResizeResult struct {
	Data     []byte
	MimeType string
	Width    int
	Height   int
}

// ResizeIfNeeded decodes the image, downscales it if it exceeds the max
// dimensions, and re-encodes it. If the image is within bounds the original
// data is returned unchanged.
func ResizeIfNeeded(data []byte, srcMime string) (ResizeResult, error) {
	if len(data) == 0 {
		return ResizeResult{}, fmt.Errorf("empty image data")
	}
	img, format, err := goimage.Decode(bytes.NewReader(data))
	if err != nil {
		return ResizeResult{}, fmt.Errorf("image decode: %w", err)
	}
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	outMime := normalizeMime(srcMime, format)
	needsPNGReencode := outMime == "image/tiff"

	if w <= MaxResizeWidth && h <= MaxResizeHeight && !needsPNGReencode {
		return ResizeResult{Data: data, MimeType: outMime, Width: w, Height: h}, nil
	}

	// Compute new dimensions preserving aspect ratio.
	newW, newH := fitDimensions(w, h, MaxResizeWidth, MaxResizeHeight)

	dst := goimage.NewRGBA(goimage.Rect(0, 0, newW, newH))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	var buf bytes.Buffer
	switch outMime {
	case "image/jpeg":
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality})
	default:
		outMime = "image/png"
		err = png.Encode(&buf, dst)
	}
	if err != nil {
		return ResizeResult{}, fmt.Errorf("image encode: %w", err)
	}
	return ResizeResult{Data: buf.Bytes(), MimeType: outMime, Width: newW, Height: newH}, nil
}

// fitDimensions scales (w, h) to fit within (maxW, maxH) preserving aspect ratio.
func fitDimensions(w, h, maxW, maxH int) (int, int) {
	if w <= maxW && h <= maxH {
		return w, h
	}
	scaleW := float64(maxW) / float64(w)
	scaleH := float64(maxH) / float64(h)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	newW := int(float64(w) * scale)
	newH := int(float64(h) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	return newW, newH
}

// normalizeMime returns a canonical MIME type from the source MIME and decoded format.
func normalizeMime(srcMime string, decodedFormat string) string {
	src := strings.ToLower(strings.TrimSpace(srcMime))
	switch src {
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	case "image/gif":
		return "image/gif"
	case "image/tiff":
		return "image/tiff"
	case "image/webp":
		return "image/webp"
	}
	// Fallback to decoded format name.
	switch strings.ToLower(decodedFormat) {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	default:
		return "image/png"
	}
}
