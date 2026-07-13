package im

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareIMDisplayContent_ExtractsLocalImagesForUpload(t *testing.T) {
	baseDir := t.TempDir()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", baseDir)
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "10000", "exports"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "10000", "exports", "a.png"), []byte("image-bytes"), 0o644))

	svc := &Service{}

	content, images := svc.prepareIMDisplayContent(context.Background(),
		"前文\n\n![图 1](local://10000/exports/a.png)\n\n后文", nil, true)

	require.Len(t, images, 1)
	assert.Equal(t, "图 1", images[0].Caption)
	assert.Equal(t, "a.png", images[0].FileName)
	assert.Equal(t, []byte("image-bytes"), images[0].Data)
	assert.Equal(t, "前文\n\n后文", content)
}

func TestPrepareIMDisplayContent_KeepsImageMarkdownWhenUploadDisabled(t *testing.T) {
	svc := &Service{}

	content, images := svc.prepareIMDisplayContent(context.Background(),
		"前文\n\n![图 1](local://10000/exports/a.png)\n\n后文", nil, false)

	require.Empty(t, images)
	assert.Contains(t, content, "![图 1](local://10000/exports/a.png)")
}

func TestPrepareIMDisplayContent_DeduplicatesLocalImages(t *testing.T) {
	baseDir := t.TempDir()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", baseDir)
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "10000", "exports"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "10000", "exports", "a.png"), []byte("image-bytes"), 0o644))

	svc := &Service{}

	content, images := svc.prepareIMDisplayContent(context.Background(),
		"![one](local://10000/exports/a.png)\n\n正文\n\n![two](local://10000/exports/a.png)", nil, true)

	require.Len(t, images, 1)
	assert.Equal(t, "one", images[0].Caption)
	assert.Equal(t, "正文", content)
}

func TestScanIMMarkdownImages_WithTitleAndAngleDestination(t *testing.T) {
	spans := scanIMMarkdownImages(`x ![阶段图](<local://10000/exports/a b.png> "阶段 1) 图片") y`)

	require.Len(t, spans, 1)
	assert.Equal(t, "阶段图", spans[0].Alt)
	assert.Equal(t, "local://10000/exports/a b.png", spans[0].Path)
}
