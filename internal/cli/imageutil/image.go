package imageutil

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// SupportedExtensions maps file extensions to their MIME types.
var SupportedExtensions = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".webp": "image/webp",
}

// MaxImageBytes is the maximum file size allowed for image loading (20MB).
const MaxImageBytes = 20 * 1024 * 1024

// IsImagePath returns true if the file path has a recognized image extension.
func IsImagePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := SupportedExtensions[ext]
	return ok
}

// MimeTypeForPath returns the MIME type for a recognized image file path.
func MimeTypeForPath(path string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	mime, ok := SupportedExtensions[ext]
	return mime, ok
}

// LoadAsContentPart reads an image file from disk and returns it as a ContentPart.
// The bytes are normalized the same way as other image entry points.
func LoadAsContentPart(absPath string) (model.ContentPart, error) {
	mime, ok := MimeTypeForPath(absPath)
	if !ok {
		return model.ContentPart{}, fmt.Errorf("unsupported image type: %s", filepath.Ext(absPath))
	}
	data, err := readImageFile(absPath)
	if err != nil {
		return model.ContentPart{}, err
	}
	return contentPartFromBytes(data, mime, filepath.Base(absPath), nil)
}

// LoadAsContentPartCached reads an image file, resizes if needed, and returns
// a cached ContentPart. If cache is nil, behaves like LoadAsContentPart with
// resize applied.
func LoadAsContentPartCached(absPath string, cache *Cache) (model.ContentPart, error) {
	mime, ok := MimeTypeForPath(absPath)
	if !ok {
		return model.ContentPart{}, fmt.Errorf("unsupported image type: %s", filepath.Ext(absPath))
	}
	data, err := readImageFile(absPath)
	if err != nil {
		return model.ContentPart{}, err
	}
	return contentPartFromBytes(data, mime, filepath.Base(absPath), cache)
}

// ContentPartFromBytes creates a ContentPart from raw image bytes, resizing if
// needed and caching the result.
func ContentPartFromBytes(raw []byte, mime string, fileName string, cache *Cache) (model.ContentPart, error) {
	if len(raw) == 0 {
		return model.ContentPart{}, fmt.Errorf("empty image data")
	}
	if len(raw) > MaxImageBytes {
		return model.ContentPart{}, fmt.Errorf("image too large: %d bytes (max %d)", len(raw), MaxImageBytes)
	}
	return contentPartFromBytes(raw, mime, fileName, cache)
}

func contentPartFromBytes(raw []byte, mime string, fileName string, cache *Cache) (model.ContentPart, error) {
	cacheKey := Key(raw)
	if cache != nil {
		if part, ok := cache.Get(cacheKey); ok {
			part.FileName = fileName
			return part, nil
		}
	}
	result, err := ResizeIfNeeded(raw, mime)
	if err != nil {
		return model.ContentPart{}, fmt.Errorf("image process: %w", err)
	}
	part := model.ContentPart{
		Type:     model.ContentPartImage,
		MimeType: result.MimeType,
		Data:     base64.StdEncoding.EncodeToString(result.Data),
		FileName: fileName,
	}
	if cache != nil {
		cache.Put(cacheKey, part)
	}
	return part, nil
}

func readImageFile(absPath string) ([]byte, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("image stat: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory, not an image file: %s", absPath)
	}
	if info.Size() > MaxImageBytes {
		return nil, fmt.Errorf("image too large: %d bytes (max %d)", info.Size(), MaxImageBytes)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("image read: %w", err)
	}
	return data, nil
}
