package imageutil

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/tiff"
)

func makePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestResizeIfNeeded_SmallImage(t *testing.T) {
	data := makePNG(100, 50)
	result, err := ResizeIfNeeded(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 100 || result.Height != 50 {
		t.Fatalf("expected 100x50, got %dx%d", result.Width, result.Height)
	}
	// Small image should not be re-encoded — original data returned.
	if !bytes.Equal(result.Data, data) {
		t.Fatal("expected original data for small image")
	}
	if result.MimeType != "image/png" {
		t.Fatalf("unexpected mime: %s", result.MimeType)
	}
}

func TestResizeIfNeeded_OversizedWidth(t *testing.T) {
	data := makePNG(4096, 400)
	result, err := ResizeIfNeeded(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if result.Width > MaxResizeWidth {
		t.Fatalf("expected width <= %d, got %d", MaxResizeWidth, result.Width)
	}
	if result.Height > MaxResizeHeight {
		t.Fatalf("expected height <= %d, got %d", MaxResizeHeight, result.Height)
	}
	if result.Width == 0 || result.Height == 0 {
		t.Fatal("dimensions must be > 0")
	}
}

func TestResizeIfNeeded_OversizedHeight(t *testing.T) {
	data := makePNG(200, 2000)
	result, err := ResizeIfNeeded(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if result.Height > MaxResizeHeight {
		t.Fatalf("expected height <= %d, got %d", MaxResizeHeight, result.Height)
	}
}

func TestResizeIfNeeded_InvalidData(t *testing.T) {
	_, err := ResizeIfNeeded([]byte("not an image"), "image/png")
	if err == nil {
		t.Fatal("expected error for invalid image data")
	}
}

func TestResizeIfNeeded_EmptyData(t *testing.T) {
	_, err := ResizeIfNeeded(nil, "image/png")
	if err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestResizeIfNeeded_WebP(t *testing.T) {
	// 2x2 WEBP generated from a local PNG fixture.
	const webpBase64 = "UklGRkAAAABXRUJQVlA4IDQAAADQAQCdASoCAAIAAgA0JZgCdAEO/gPIAAD+4p9hDzz1v8gubqShGz0MATI/8B/Ev7AisrAA"
	data, err := base64.StdEncoding.DecodeString(webpBase64)
	if err != nil {
		t.Fatalf("decode base64 webp: %v", err)
	}
	result, err := ResizeIfNeeded(data, "image/webp")
	if err != nil {
		t.Fatalf("webp decode should be supported: %v", err)
	}
	if result.Width != 2 || result.Height != 2 {
		t.Fatalf("expected 2x2, got %dx%d", result.Width, result.Height)
	}
	if result.MimeType != "image/webp" {
		t.Fatalf("unexpected mime: %s", result.MimeType)
	}
}

func TestResizeIfNeeded_TIFFReencodesToPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := tiff.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode tiff: %v", err)
	}
	result, err := ResizeIfNeeded(buf.Bytes(), "image/tiff")
	if err != nil {
		t.Fatalf("tiff decode should be supported: %v", err)
	}
	if result.MimeType != "image/png" {
		t.Fatalf("expected TIFF to normalize to image/png, got %s", result.MimeType)
	}
	if result.Width != 8 || result.Height != 4 {
		t.Fatalf("expected 8x4, got %dx%d", result.Width, result.Height)
	}
	if bytes.Equal(result.Data, buf.Bytes()) {
		t.Fatal("expected TIFF input to be re-encoded")
	}
}

func TestLoadAsContentPart_TIFFNormalizesToPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := tiff.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode tiff: %v", err)
	}

	path := filepath.Join(t.TempDir(), "frame.tiff")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write tiff: %v", err)
	}

	part, err := LoadAsContentPart(path)
	if err != nil {
		t.Fatalf("load content part: %v", err)
	}
	if part.MimeType != "image/png" {
		t.Fatalf("expected TIFF to normalize to image/png, got %s", part.MimeType)
	}
}

func TestFitDimensions(t *testing.T) {
	tests := []struct {
		w, h, maxW, maxH int
		wantW, wantH     int
	}{
		{100, 50, 2048, 768, 100, 50},      // within bounds
		{4096, 768, 2048, 768, 2048, 384},  // width exceeds
		{2048, 1536, 2048, 768, 1024, 768}, // height exceeds
		{4096, 1536, 2048, 768, 2048, 768}, // both exceed — height is limiting
	}
	for _, tc := range tests {
		gotW, gotH := fitDimensions(tc.w, tc.h, tc.maxW, tc.maxH)
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Errorf("fitDimensions(%d,%d,%d,%d) = (%d,%d), want (%d,%d)",
				tc.w, tc.h, tc.maxW, tc.maxH, gotW, gotH, tc.wantW, tc.wantH)
		}
	}
}
