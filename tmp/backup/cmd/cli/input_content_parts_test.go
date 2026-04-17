package main

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuiapp"
	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func TestBuildInterleavedContentPartsPreservesAttachmentOffsets(t *testing.T) {
	parts := buildInterleavedContentParts("Hi豆包这两个是什么APP?", []tuiapp.Attachment{
		{Name: "later.png", Offset: len([]rune("Hi豆包"))},
		{Name: "first.png", Offset: 0},
	}, map[string]model.ContentPart{
		"first.png": {Type: model.ContentPartImage, FileName: "first.png", Data: "a"},
		"later.png": {Type: model.ContentPartImage, FileName: "later.png", Data: "b"},
	})

	if len(parts) != 4 {
		t.Fatalf("expected 4 interleaved parts, got %+v", parts)
	}
	if parts[0].Type != model.ContentPartImage || parts[0].FileName != "first.png" {
		t.Fatalf("expected first image part, got %+v", parts[0])
	}
	if parts[1].Type != model.ContentPartText || parts[1].Text != "Hi豆包" {
		t.Fatalf("expected first text segment, got %+v", parts[1])
	}
	if parts[2].Type != model.ContentPartImage || parts[2].FileName != "later.png" {
		t.Fatalf("expected second image part, got %+v", parts[2])
	}
	if parts[3].Type != model.ContentPartText || parts[3].Text != "这两个是什么APP?" {
		t.Fatalf("expected trailing text segment, got %+v", parts[3])
	}
}
