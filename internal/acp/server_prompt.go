package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	imageutil "github.com/OnslaughtSnail/caelis/internal/cli/imageutil"
	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type promptInputResult struct {
	text         string
	contentParts []model.ContentPart
	hasImages    bool
}

func (s *Server) promptInput(sessionID string, blocks []json.RawMessage) (promptInputResult, error) {
	result := promptInputResult{}
	orderedParts := make([]model.ContentPart, 0, len(blocks))
	textParts := make([]string, 0, len(blocks))
	for _, raw := range blocks {
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			return promptInputResult{}, err
		}
		switch strings.TrimSpace(base.Type) {
		case "text":
			var block TextContent
			if err := json.Unmarshal(raw, &block); err != nil {
				return promptInputResult{}, err
			}
			text := strings.TrimSpace(block.Text)
			if text != "" {
				textParts = append(textParts, text)
				orderedParts = append(orderedParts, model.ContentPart{
					Type: model.ContentPartText,
					Text: text,
				})
			}
		case "image":
			var block ImageContent
			if err := json.Unmarshal(raw, &block); err != nil {
				return promptInputResult{}, err
			}
			part, err := s.resolveImageBlock(sessionID, block)
			if err != nil {
				return promptInputResult{}, err
			}
			if part.Type == model.ContentPartImage {
				orderedParts = append(orderedParts, part)
				result.hasImages = true
			}
		case "resource_link":
			var block ResourceLink
			if err := json.Unmarshal(raw, &block); err != nil {
				return promptInputResult{}, err
			}
			part, text, err := s.resolveResourceLink(sessionID, block)
			if err != nil {
				return promptInputResult{}, err
			}
			if part.Type == model.ContentPartImage {
				orderedParts = append(orderedParts, part)
				result.hasImages = true
			}
			if strings.TrimSpace(text) != "" {
				textParts = append(textParts, text)
				orderedParts = append(orderedParts, model.ContentPart{
					Type: model.ContentPartText,
					Text: text,
				})
			}
		case "resource":
			var block EmbeddedResource
			if err := json.Unmarshal(raw, &block); err != nil {
				return promptInputResult{}, err
			}
			part, text, err := s.resolveEmbeddedResource(block)
			if err != nil {
				return promptInputResult{}, err
			}
			if part.Type == model.ContentPartImage {
				orderedParts = append(orderedParts, part)
				result.hasImages = true
			}
			if text != "" {
				textParts = append(textParts, text)
				orderedParts = append(orderedParts, model.ContentPart{
					Type: model.ContentPartText,
					Text: text,
				})
			}
		default:
			return promptInputResult{}, fmt.Errorf("unsupported prompt block type %q", base.Type)
		}
	}
	result.text = strings.TrimSpace(strings.Join(textParts, "\n\n"))
	if result.hasImages {
		result.contentParts = compactContentParts(orderedParts)
	}
	return result, nil
}

func (s *Server) resolveResourceLink(sessionID string, link ResourceLink) (model.ContentPart, string, error) {
	uri := strings.TrimSpace(link.URI)
	if uri == "" {
		return model.ContentPart{}, "", nil
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return model.ContentPart{}, "", err
	}
	if parsed.Scheme != "file" {
		return model.ContentPart{}, "", fmt.Errorf("unsupported resource link scheme %q", parsed.Scheme)
	}
	path := filepath.Clean(parsed.Path)
	if s.linkIsImage(path, link.MimeType) {
		part, err := s.loadImageContentPart(sessionID, path, link.MimeType, link.Name)
		return part, "", err
	}
	var resp ReadTextFileResponse
	err = s.cfg.Conn.Call(context.Background(), MethodReadTextFile, ReadTextFileRequest{
		SessionID: sessionID,
		Path:      path,
	}, &resp)
	if err != nil {
		data, readErr := sessReadFile(s.sessionFS(sessionID), path)
		if readErr != nil {
			return model.ContentPart{}, "", err
		}
		resp.Content = data
	}
	label := strings.TrimSpace(link.Name)
	if label == "" {
		label = path
	}
	return model.ContentPart{}, fmt.Sprintf("[resource: %s]\n%s", label, strings.TrimSpace(resp.Content)), nil
}

func (s *Server) resolveEmbeddedResource(block EmbeddedResource) (model.ContentPart, string, error) {
	resource := block.Resource
	if imageMIME(strings.TrimSpace(resource.MimeType)) {
		raw := strings.TrimSpace(resource.Blob)
		if raw == "" {
			raw = strings.TrimSpace(resource.Data)
		}
		if raw == "" {
			raw = strings.TrimSpace(resource.Text)
		}
		if raw == "" {
			return model.ContentPart{}, "", nil
		}
		data, mime, err := decodeEmbeddedImageData(raw, resource.MimeType)
		if err != nil {
			return model.ContentPart{}, "", err
		}
		name := strings.TrimSpace(resource.Name)
		if name == "" {
			name = filepath.Base(strings.TrimSpace(resource.URI))
		}
		part, err := imageutil.ContentPartFromBytes(data, mime, name, nil)
		return part, "", err
	}
	text := strings.TrimSpace(resource.Text)
	if text == "" {
		return model.ContentPart{}, "", nil
	}
	name := strings.TrimSpace(resource.Name)
	if name == "" {
		name = strings.TrimSpace(resource.URI)
	}
	return model.ContentPart{}, fmt.Sprintf("[embedded resource: %s]\n%s", name, text), nil
}

func (s *Server) resolveImageBlock(sessionID string, block ImageContent) (model.ContentPart, error) {
	if uri := strings.TrimSpace(block.URI); uri != "" {
		link := ResourceLink{
			Type:     "resource_link",
			Name:     block.Name,
			URI:      uri,
			MimeType: block.MimeType,
		}
		part, _, err := s.resolveResourceLink(sessionID, link)
		return part, err
	}
	raw := strings.TrimSpace(block.Data)
	if raw == "" {
		return model.ContentPart{}, nil
	}
	data, mime, err := decodeEmbeddedImageData(raw, block.MimeType)
	if err != nil {
		return model.ContentPart{}, err
	}
	name := strings.TrimSpace(block.Name)
	if name == "" {
		name = "image"
	}
	return imageutil.ContentPartFromBytes(data, mime, name, nil)
}

func (s *Server) loadImageContentPart(sessionID string, path string, mime string, name string) (model.ContentPart, error) {
	fsys := s.sessionFS(sessionID)
	if fsys == nil {
		return model.ContentPart{}, fmt.Errorf("session file system is not available")
	}
	data, err := fsys.ReadFile(path)
	if err != nil {
		return model.ContentPart{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(path)
	}
	if strings.TrimSpace(mime) == "" {
		if detected, ok := imageutil.MimeTypeForPath(path); ok {
			mime = detected
		}
	}
	return imageutil.ContentPartFromBytes(data, mime, name, nil)
}

func (s *Server) linkIsImage(path string, mime string) bool {
	if imageMIME(mime) {
		return true
	}
	return imageutil.IsImagePath(path)
}

func imageMIME(mime string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mime)), "image/")
}

func decodeEmbeddedImageData(raw string, fallbackMime string) ([]byte, string, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		parts := strings.SplitN(value, ",", 2)
		if len(parts) != 2 {
			return nil, "", fmt.Errorf("invalid image data URL")
		}
		header := strings.ToLower(parts[0])
		mime := fallbackMime
		if strings.HasPrefix(header, "data:") {
			mime = strings.TrimPrefix(parts[0], "data:")
			mime = strings.TrimSuffix(mime, ";base64")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, "", fmt.Errorf("invalid image data: %w", err)
		}
		return data, mime, nil
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, "", fmt.Errorf("invalid image data: %w", err)
	}
	return data, fallbackMime, nil
}

func filterImageContentParts(parts []model.ContentPart, keepImages bool) []model.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	filtered := make([]model.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == model.ContentPartImage && !keepImages {
			continue
		}
		filtered = append(filtered, part)
	}
	return filtered
}

func compactContentParts(parts []model.ContentPart) []model.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]model.ContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case model.ContentPartText:
			if part.Text == "" {
				continue
			}
			if n := len(out); n > 0 && out[n-1].Type == model.ContentPartText {
				out[n-1].Text += "\n\n" + part.Text
				continue
			}
		case model.ContentPartImage:
			if strings.TrimSpace(part.Data) == "" && strings.TrimSpace(part.FileName) == "" {
				continue
			}
		}
		out = append(out, part)
	}
	return out
}
