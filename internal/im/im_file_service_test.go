package im

import (
	"context"
	"io"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubIMFileService implements interfaces.FileService for IM resolver tests.
type stubIMFileService struct {
	getFile    func(ctx context.Context, filePath string) (io.ReadCloser, error)
	getFileURL func(ctx context.Context, filePath string) (string, error)
}

func (s *stubIMFileService) CheckConnectivity(context.Context) error { return nil }

func (s *stubIMFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", nil
}

func (s *stubIMFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", nil
}

func (s *stubIMFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	if s.getFile != nil {
		return s.getFile(ctx, filePath)
	}
	return nil, nil
}

func (s *stubIMFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	if s.getFileURL != nil {
		return s.getFileURL(ctx, filePath)
	}
	return "https://global-storage.example/" + filePath, nil
}

func (s *stubIMFileService) DeleteFile(context.Context, string) error { return nil }

func (s *stubIMFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", nil
}

type stubIMResourceCatalog struct {
	resource *types.StoredResource
}

func (s *stubIMResourceCatalog) Register(context.Context, uint64, string, interfaces.ResourceRegistration) (string, error) {
	return "", nil
}
func (s *stubIMResourceCatalog) Resolve(context.Context, string) (*types.StoredResource, error) {
	return s.resource, nil
}
func (s *stubIMResourceCatalog) ResolvePath(_ context.Context, value string) (string, *types.StoredResource, error) {
	if _, ok := types.ParseResourcePath(value); ok && s.resource != nil {
		return s.resource.PhysicalPath, s.resource, nil
	}
	return value, nil, nil
}
func (s *stubIMResourceCatalog) Bind(context.Context, string, string, string, string) error {
	return nil
}
func (s *stubIMResourceCatalog) MarkDeleted(context.Context, string) error { return nil }
func (s *stubIMResourceCatalog) CreateAccessGrant(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (s *stubIMResourceCatalog) ResolveAccessGrant(context.Context, string) (*types.StoredResource, error) {
	return s.resource, nil
}

type stubIMStorageResolver struct {
	service   interfaces.FileService
	backendID string
	provider  string
}

func (s *stubIMStorageResolver) ResolveFileService(
	_ context.Context,
	_ *types.Tenant,
	backendID, provider, _ string,
) (interfaces.FileService, string, error) {
	s.backendID = backendID
	s.provider = provider
	return s.service, provider, nil
}

func (s *stubIMStorageResolver) ResolveBackend(
	context.Context,
	*types.Tenant,
	string,
	string,
) (*types.StorageBackend, error) {
	return nil, nil
}

func TestBuildIMFileServiceForProvider_FallbackToGlobal(t *testing.T) {
	stub := &stubIMFileService{}
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}

	svc := buildIMFileServiceForProvider(tenant, "minio", stub)
	require.NotNil(t, svc)
	got, err := svc.GetFileURL(context.Background(), "minio://wizard-test/10000/exports/a.png")
	require.NoError(t, err)
	assert.Equal(t, "https://global-storage.example/minio://wizard-test/10000/exports/a.png", got)
}

func TestIMFileServiceResolver_CachesPerProvider(t *testing.T) {
	stub := &stubIMFileService{}
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}
	r := newIMFileServiceResolver(tenant, stub, nil, nil)

	svc1 := r.resolve("minio://wizard-test/10000/a.png")
	svc2 := r.resolve("minio://wizard-test/10000/b.png")
	assert.Same(t, svc1, svc2, "same provider should reuse cached FileService")

	svc3 := r.resolve("local://10000/c.png")
	assert.NotSame(t, svc1, svc3, "different provider should use a different service")
}

func TestIMFileServiceResolver_ResourceUsesOwningStorageBackend(t *testing.T) {
	const (
		resourceRef = "resource://Ynh5EUsycOSg7yoPeX6ZIQ"
		backendID   = "a0ec0f7b-f248-48a0-b279-b42b7e017a2a"
		localPath   = "local://10000/exports/image.jpg"
	)
	catalog := &stubIMResourceCatalog{resource: &types.StoredResource{
		StorageBackendID: backendID,
		Provider:         "local",
		PhysicalPath:     types.BuildStorageBackendPath(backendID, localPath),
	}}

	var openedPath string
	physicalSvc := &stubIMFileService{getFile: func(_ context.Context, filePath string) (io.ReadCloser, error) {
		openedPath = filePath
		return io.NopCloser(strings.NewReader("image-bytes")), nil
	}}
	backendSvc := filesvc.NewBackendScopedFileService(backendID, physicalSvc)
	resourceSvc := filesvc.NewResourceCatalogFileService(backendSvc, catalog)
	storageResolver := &stubIMStorageResolver{service: resourceSvc}

	resolver := newIMFileServiceResolver(&types.Tenant{ID: 10000}, nil, storageResolver, catalog)
	svc := resolver.resolve(resourceRef)
	require.NotNil(t, svc)
	reader, err := svc.GetFile(context.Background(), resourceRef)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	assert.Equal(t, backendID, storageResolver.backendID)
	assert.Equal(t, "local", storageResolver.provider)
	assert.Equal(t, localPath, openedPath)
}

func TestRewriteStorageURLs_MinIOFallbackViaGlobal(t *testing.T) {
	stub := &stubIMFileService{
		getFileURL: func(_ context.Context, filePath string) (string, error) {
			return "https://minio.example/presigned?path=" + filePath, nil
		},
	}
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}
	in := `![知识助理"知识库"管理视图界面](minio://wizard-test/10000/exports/c91cf852.png)`
	resolver := newIMFileServiceResolver(tenant, stub, nil, nil)
	out := rewriteStorageURLs(context.Background(), in, resolver)
	assert.Contains(t, out, "https://minio.example/presigned")
	assert.NotContains(t, out, "](minio://")
}

func TestRewriteStorageURLs_ScopedPath(t *testing.T) {
	stub := &stubIMFileService{
		getFileURL: func(_ context.Context, filePath string) (string, error) {
			assert.Equal(t, "storage://backend-a/cos://bucket/ap-test/10000/exports/a.png", filePath)
			return "https://storage.example/a.png", nil
		},
	}
	input := "![img](storage://backend-a/cos://bucket/ap-test/10000/exports/a.png)"
	output := rewriteStorageURLs(context.Background(), input, newIMFileServiceResolver(&types.Tenant{}, stub, nil, nil))
	assert.Contains(t, output, "https://storage.example/a.png")
}

func TestCleanIMContent_MinIOFallbackIntegration(t *testing.T) {
	stub := &stubIMFileService{
		getFileURL: func(_ context.Context, _ string) (string, error) {
			return "https://minio.example/img.png", nil
		},
	}
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{DefaultProvider: "cos"},
	}
	in := "see ![x](minio://wizard-test/10000/exports/x.png) ok"
	out := cleanIMContent(context.Background(), in, tenant, stub)
	assert.Contains(t, out, "https://minio.example/img.png")
}
