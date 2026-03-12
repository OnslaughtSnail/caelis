package tuiapp

import "strings"

// Attachment describes one inline attachment token in the composer.
// Offset is measured in rune positions within Text.
type Attachment struct {
	Name   string
	Offset int
}

// Submission is the structured payload emitted by the composer.
type Submission struct {
	Text        string
	Attachments []Attachment
}

func cloneAttachments(items []Attachment) []Attachment {
	if len(items) == 0 {
		return nil
	}
	out := make([]Attachment, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		offset := item.Offset
		if offset < 0 {
			offset = 0
		}
		out = append(out, Attachment{Name: name, Offset: offset})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func submissionAttachments(items []inputAttachment) []Attachment {
	if len(items) == 0 {
		return nil
	}
	out := make([]Attachment, 0, len(items))
	for _, item := range cloneInputAttachments(items) {
		out = append(out, Attachment{
			Name:   item.Name,
			Offset: item.Offset,
		})
	}
	return out
}
