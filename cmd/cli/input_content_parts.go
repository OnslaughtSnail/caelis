package main

import (
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuiapp"
	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func buildInterleavedContentParts(text string, attachments []tuiapp.Attachment, library map[string]model.ContentPart) []model.ContentPart {
	textRunes := []rune(text)
	items := cloneAndSortAttachments(attachments)
	if len(items) == 0 {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []model.ContentPart{{
			Type: model.ContentPartText,
			Text: text,
		}}
	}

	parts := make([]model.ContentPart, 0, len(items)*2+1)
	textPos := 0
	for _, item := range items {
		offset := item.Offset
		if offset < 0 {
			offset = 0
		}
		if offset > len(textRunes) {
			offset = len(textRunes)
		}
		if offset > textPos {
			parts = append(parts, model.ContentPart{
				Type: model.ContentPartText,
				Text: string(textRunes[textPos:offset]),
			})
			textPos = offset
		}
		part, ok := library[strings.TrimSpace(item.Name)]
		if !ok {
			continue
		}
		parts = append(parts, part)
	}
	if textPos < len(textRunes) {
		parts = append(parts, model.ContentPart{
			Type: model.ContentPartText,
			Text: string(textRunes[textPos:]),
		})
	}
	return compactContentParts(parts)
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
				out[n-1].Text += part.Text
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

func cloneAndSortAttachments(items []tuiapp.Attachment) []tuiapp.Attachment {
	if len(items) == 0 {
		return nil
	}
	out := make([]tuiapp.Attachment, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		offset := item.Offset
		if offset < 0 {
			offset = 0
		}
		out = append(out, tuiapp.Attachment{
			Name:   name,
			Offset: offset,
		})
	}
	if len(out) == 0 {
		return nil
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Offset < out[j].Offset
	})
	return out
}

func attachmentNames(items []tuiapp.Attachment) []string {
	if len(items) == 0 {
		return nil
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func (c *cliConsole) pendingAttachmentLibrary() map[string]model.ContentPart {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	library := make(map[string]model.ContentPart, len(c.pendingAttachments))
	for _, part := range c.pendingAttachments {
		name := strings.TrimSpace(part.FileName)
		if name == "" {
			continue
		}
		library[name] = part
	}
	return library
}

func (c *cliConsole) consumePendingAttachmentsByName(names []string) []model.ContentPart {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	if len(c.pendingAttachments) == 0 {
		return nil
	}
	if len(names) == 0 {
		parts := c.pendingAttachments
		c.pendingAttachments = nil
		return parts
	}
	library := make(map[string]model.ContentPart, len(c.pendingAttachments))
	for _, part := range c.pendingAttachments {
		name := strings.TrimSpace(part.FileName)
		if name == "" {
			continue
		}
		library[name] = part
	}
	parts := make([]model.ContentPart, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		part, ok := library[name]
		if !ok {
			continue
		}
		parts = append(parts, part)
	}
	c.pendingAttachments = nil
	return parts
}
