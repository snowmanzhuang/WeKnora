package im

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingIMStorageResolver struct {
	fileService interfaces.FileService
	contextSeen bool
	backendID   string
	provider    string
}

func (r *recordingIMStorageResolver) ResolveFileService(
	ctx context.Context,
	_ *types.Tenant,
	backendID string,
	provider string,
	_ string,
) (interfaces.FileService, string, error) {
	r.contextSeen = ctx.Value(imStorageResolverContextKey{}) == "request-context"
	r.backendID = backendID
	r.provider = provider
	return r.fileService, provider, nil
}

func (r *recordingIMStorageResolver) ResolveBackend(
	context.Context,
	*types.Tenant,
	string,
	string,
) (*types.StorageBackend, error) {
	return nil, nil
}

type imStorageResolverContextKey struct{}

type recordingInlineImageUploader struct {
	mu       sync.Mutex
	uploads  map[string]int
	failWith error
}

func (u *recordingInlineImageUploader) UploadInlineImage(
	_ context.Context,
	_ *IncomingMessage,
	image *OutboundImage,
) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.uploads == nil {
		u.uploads = make(map[string]int)
	}
	u.uploads[image.FileName]++
	if u.failWith != nil {
		return "", u.failWith
	}
	return "img_test_" + image.FileName, nil
}

func (u *recordingInlineImageUploader) uploadCount(fileName string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.uploads[fileName]
}

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

func TestPrepareIMDisplayContent_UsesWorkspaceStorageResolver(t *testing.T) {
	fileService := &stubIMFileService{
		getFile: func(_ context.Context, filePath string) (io.ReadCloser, error) {
			assert.Equal(t, "storage://backend-a/local://10000/exports/a.png", filePath)
			return io.NopCloser(strings.NewReader("workspace-image")), nil
		},
	}
	storageResolver := &recordingIMStorageResolver{fileService: fileService}
	svc := &Service{storageResolver: storageResolver}
	tenant := &types.Tenant{ID: 10000}
	ctx := context.WithValue(context.Background(), imStorageResolverContextKey{}, "request-context")

	content, images := svc.prepareIMDisplayContent(ctx,
		"![workspace](storage://backend-a/local://10000/exports/a.png)", tenant, true)

	require.Len(t, images, 1)
	assert.Empty(t, content)
	assert.Equal(t, []byte("workspace-image"), images[0].Data)
	assert.True(t, storageResolver.contextSeen)
	assert.Equal(t, "backend-a", storageResolver.backendID)
	assert.Equal(t, "local", storageResolver.provider)
}

func TestPrepareIMDisplayContent_ExtractsCanonicalResourceImage(t *testing.T) {
	const resourcePath = "resource://AbCdEfGhIjKlMnOpQrStUv"
	defaultFileService := &stubIMFileService{
		getFile: func(_ context.Context, filePath string) (io.ReadCloser, error) {
			assert.Equal(t, resourcePath, filePath)
			return io.NopCloser(strings.NewReader("resource-image")), nil
		},
	}
	svc := &Service{defaultFileSvc: defaultFileService}

	content, images := svc.prepareIMDisplayContent(context.Background(),
		"![resource]("+resourcePath+")", &types.Tenant{ID: 10000}, true)

	require.Len(t, images, 1)
	assert.Empty(t, content)
	assert.Equal(t, []byte("resource-image"), images[0].Data)
}

func TestInlineImageRewriter_PreservesPositionsAndCachesUploads(t *testing.T) {
	baseDir := t.TempDir()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", baseDir)
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "10000", "exports"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "10000", "exports", "a.png"), []byte("a-bytes"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "10000", "exports", "b.jpg"), []byte("b-bytes"), 0o644))

	uploader := &recordingInlineImageUploader{}
	rewriter := &imInlineImageRewriter{
		uploader: uploader,
		incoming: &IncomingMessage{Platform: PlatformFeishu},
		resolver: newIMFileServiceResolver(nil, nil, nil, nil),
		refs:     make(map[string]string),
		failures: make(map[string]time.Time),
		tracked:  make(map[string]struct{}),
		blocked:  make(map[string]struct{}),
	}
	input := "前文\n\n![图 A](local://10000/exports/a.png)\n\n中文\n\n![图 B](local://10000/exports/b.jpg)\n\n后文"

	first := rewriter.rewrite(context.Background(), input, false)
	second := rewriter.rewrite(context.Background(), input, true)

	assert.Equal(t, "前文\n\n![图 A](img_test_a.png)\n\n中文\n\n![图 B](img_test_b.jpg)\n\n后文", first)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, uploader.uploadCount("a.png"))
	assert.Equal(t, 1, uploader.uploadCount("b.jpg"))
}

func TestInlineImageRewriter_UploadFailureDegradesOnlyImage(t *testing.T) {
	baseDir := t.TempDir()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", baseDir)
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "10000", "exports"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "10000", "exports", "a.png"), []byte("a-bytes"), 0o644))

	uploader := &recordingInlineImageUploader{failWith: fmt.Errorf("upload unavailable")}
	rewriter := &imInlineImageRewriter{
		uploader: uploader,
		incoming: &IncomingMessage{Platform: PlatformFeishu},
		resolver: newIMFileServiceResolver(nil, nil, nil, nil),
		refs:     make(map[string]string),
		failures: make(map[string]time.Time),
		tracked:  make(map[string]struct{}),
		blocked:  make(map[string]struct{}),
	}

	output := rewriter.rewrite(context.Background(), "前文 ![眼底图](local://10000/exports/a.png) 后文", false)

	assert.Equal(t, "前文 *图片暂时无法显示* 后文", output)
	assert.NotContains(t, output, "local://")
	assert.Equal(t, 1, uploader.uploadCount("a.png"))
}

func TestScanIMMarkdownImages_WithTitleAndAngleDestination(t *testing.T) {
	spans := scanIMMarkdownImages(`x ![阶段图](<local://10000/exports/a b.png> "阶段 1) 图片") y`)

	require.Len(t, spans, 1)
	assert.Equal(t, "阶段图", spans[0].Alt)
	assert.Equal(t, "local://10000/exports/a b.png", spans[0].Path)
}
