package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type parentResolveChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (r *parentResolveChunkRepo) ListChunksByID(
	_ context.Context,
	_ uint64,
	ids []string,
) ([]*types.Chunk, error) {
	out := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if chunk := r.chunks[id]; chunk != nil {
			out = append(out, chunk)
		}
	}
	return out, nil
}

func (r *parentResolveChunkRepo) ListChunksByParentIDs(
	_ context.Context,
	_ uint64,
	_ []string,
) ([]*types.Chunk, error) {
	return nil, nil
}

func TestResolveParentChunks_KeepsImagesOutsideMatchedChildRange(t *testing.T) {
	childContent := "A fetal nuclear cataract may lie at the sutures (Fig. 1.5)."
	parentContent := childContent + "\n\n" +
		"![Fig. 1.5 Fetal nuclear cataract.](resource://figure-1-5)"
	repo := &parentResolveChunkRepo{chunks: map[string]*types.Chunk{
		"parent-1": {
			ID:        "parent-1",
			Content:   parentContent,
			ChunkType: types.ChunkTypeParentText,
			StartAt:   100,
			EndAt:     100 + len([]rune(parentContent)),
		},
	}}
	merge := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	result := &types.SearchResult{
		ID:            "child-1",
		Content:       childContent,
		ChunkType:     string(types.ChunkTypeText),
		ParentChunkID: "parent-1",
		StartAt:       100,
		EndAt:         100 + len([]rune(childContent)),
	}

	out := merge.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(out) != 1 {
		t.Fatalf("unexpected result count: %d", len(out))
	}
	if out[0].Content != parentContent {
		t.Fatalf("expected complete parent content, got %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "resource://figure-1-5") {
		t.Fatalf("expected figure outside the child range to remain in context: %q", out[0].Content)
	}
}

func TestCollectResolvedContentParentIDs(t *testing.T) {
	parentMap := map[string]*types.Chunk{
		"parent-1": {ID: "parent-1", ChunkType: types.ChunkTypeParentText},
		"parent-2": {ID: "parent-2", ChunkType: types.ChunkTypeParentText},
		"text-2":   {ID: "text-2", ChunkType: types.ChunkTypeText, ParentChunkID: "parent-2"},
		"text-x":   {ID: "text-x", ChunkType: types.ChunkTypeText},
	}
	results := []*types.SearchResult{
		{ID: "text-1", ChunkType: string(types.ChunkTypeText), ParentChunkID: "parent-1"},
		{ID: "img-1", ChunkType: string(types.ChunkTypeImageOCR), ParentChunkID: "text-2"},
		{ID: "text-3", ChunkType: string(types.ChunkTypeText), ParentChunkID: "text-x"}, // not parent_text
	}
	ids := collectResolvedContentParentIDs(results, parentMap)
	if len(ids) != 2 {
		t.Fatalf("ids: %v", ids)
	}
	got := map[string]bool{ids[0]: true, ids[1]: true}
	if !got["parent-1"] || !got["parent-2"] {
		t.Fatalf("ids: %v", ids)
	}
}

func TestAssignParentImageInfo_UsesAllParentMetadata(t *testing.T) {
	r := &types.SearchResult{ImageInfo: "hit-only"}
	assignParentImageInfo(r, map[string]string{"parent-1": "all-parent-images"}, "parent-1")
	if r.ImageInfo != "all-parent-images" {
		t.Fatalf("image info: %q", r.ImageInfo)
	}

	assignParentImageInfo(r, nil, "parent-1")
	if r.ImageInfo != "all-parent-images" {
		t.Fatalf("existing image info should be preserved: %q", r.ImageInfo)
	}
}
