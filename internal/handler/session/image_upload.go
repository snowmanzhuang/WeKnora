package session

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	appservice "github.com/Tencent/WeKnora/internal/application/service"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	maxImageSize   = 10 << 20 // 10MB per image
	maxImagesCount = 10
)

// saveImageAttachments decodes base64 images from the request and saves them to
// storage. The images slice is mutated in place: URL is populated.
// This is always called when images are present. VLM analysis is handled
// separately (either in the pipeline rewrite step for RAG paths, or via
// analyzeImageAttachments for pure chat paths with non-vision models).
func (h *Handler) saveImageAttachments(ctx context.Context, images []ImageAttachment, tenantID uint64, storageProvider string) error {
	if len(images) == 0 {
		return nil
	}
	if len(images) > maxImagesCount {
		return fmt.Errorf("too many images, max %d", maxImagesCount)
	}

	fileSvc := h.resolveImageFileService(ctx, storageProvider)

	for i := range images {
		img := &images[i]
		if img.Data == "" {
			continue
		}

		imgBytes, ext, err := decodeDataURI(img.Data)
		if err != nil {
			return fmt.Errorf("decode image %d: %w", i, err)
		}
		if len(imgBytes) > maxImageSize {
			return fmt.Errorf("image %d too large (%d bytes, max %d)", i, len(imgBytes), maxImageSize)
		}

		storedName := fmt.Sprintf("chat-images/%s%s", uuid.New().String(), ext)
		fileURL, err := fileSvc.SaveBytes(ctx, imgBytes, tenantID, storedName, false)
		if err != nil {
			return fmt.Errorf("save image %d: %w", i, err)
		}
		img.URL = fileURL
	}

	return nil
}

const ophthalmologyAuxiliaryVLMPrompt = appservice.OphthalmologyAuxiliaryVLMPrompt

// analyzeImageAttachments runs the legacy context-aware VLM analysis and
// populates Caption. It remains the fallback for paths that do not opt in to
// the ophthalmology-specific auxiliary report.
func (h *Handler) analyzeImageAttachments(
	ctx context.Context,
	images []ImageAttachment,
	vlmModelID string,
	userQuery string,
) int {
	return h.analyzeImageAttachmentsWithPrompt(
		ctx,
		images,
		vlmModelID,
		func(_ int) string { return buildImageAnalysisPrompt(userQuery) },
		false,
	)
}

// analyzeOphthalmologyImageAttachments sends all images from one user turn in
// one VLM request. The report keeps per-image observations but also performs a
// case-level comparison, which is both faster and clinically more coherent
// than serial, isolated image calls.
func (h *Handler) analyzeOphthalmologyImageAttachments(
	ctx context.Context,
	images []ImageAttachment,
	vlmModelID string,
) int {
	if len(images) == 0 || vlmModelID == "" {
		return 0
	}

	vlmModel, err := h.modelService.GetVLMModel(ctx, vlmModelID)
	if err != nil {
		logger.Warnf(ctx, "No VLM model available for joint image analysis, skipping: %v", err)
		return 0
	}

	imageBytesList := make([][]byte, 0, len(images))
	imageIndices := make([]int, 0, len(images))
	for i := range images {
		imgBytes, readErr := h.readImageBytesForAnalysis(ctx, images[i])
		if readErr != nil {
			logger.Warnf(ctx, "Failed to read image %d for joint VLM analysis: %v", i, readErr)
			continue
		}
		imageBytesList = append(imageBytesList, imgBytes)
		imageIndices = append(imageIndices, i)
	}
	if len(imageBytesList) == 0 {
		return 0
	}

	analysis, analysisErr := vlmModel.Predict(
		ctx,
		imageBytesList,
		appservice.BuildOphthalmologyAuxiliaryVLMPrompt(len(imageBytesList)),
	)
	if analysisErr != nil {
		logger.Warnf(ctx, "Joint VLM analysis failed for %d image(s): %v", len(imageBytesList), analysisErr)
		return 0
	}
	analysis = strings.TrimSpace(analysis)
	if analysis == "" {
		logger.Warnf(ctx, "Joint VLM analysis returned empty content for %d image(s)", len(imageBytesList))
		return 0
	}

	labels := make([]string, 0, len(imageIndices))
	for _, imageIndex := range imageIndices {
		labels = append(labels, fmt.Sprintf("%d", imageIndex+1))
	}
	primaryIndex := imageIndices[0]
	images[primaryIndex].Caption = fmt.Sprintf(
		"【图片%s：联合辅助视觉报告】\n%s",
		strings.Join(labels, "、"),
		analysis,
	)
	for _, imageIndex := range imageIndices[1:] {
		images[imageIndex].Caption = fmt.Sprintf(
			"【图片%d】已纳入本轮多图联合辅助视觉分析；完整联合报告见本轮首张成功解析图片的说明。",
			imageIndex+1,
		)
	}
	return len(imageIndices)
}

func (h *Handler) analyzeImageAttachmentsWithPrompt(
	ctx context.Context,
	images []ImageAttachment,
	vlmModelID string,
	promptForImage func(index int) string,
	labelImages bool,
) int {
	if len(images) == 0 || vlmModelID == "" {
		return 0
	}

	vlmModel, err := h.modelService.GetVLMModel(ctx, vlmModelID)
	if err != nil {
		logger.Warnf(ctx, "No VLM model available for image analysis, skipping: %v", err)
		return 0
	}

	analyzed := 0
	for i := range images {
		img := &images[i]
		imgBytes, readErr := h.readImageBytesForAnalysis(ctx, *img)
		if readErr != nil {
			logger.Warnf(ctx, "Failed to read image %d for VLM analysis: %v", i, readErr)
			continue
		}
		prompt := promptForImage(i)
		analysis, analysisErr := vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)
		if analysisErr != nil {
			logger.Warnf(ctx, "VLM analysis failed for image %d: %v", i, analysisErr)
		} else {
			analysis = strings.TrimSpace(analysis)
			if analysis == "" {
				logger.Warnf(ctx, "VLM analysis returned empty content for image %d", i)
				continue
			}
			if labelImages {
				img.Caption = fmt.Sprintf("【图片%d：辅助视觉报告】\n%s", i+1, analysis)
			} else {
				img.Caption = analysis
			}
			analyzed++
		}
	}
	return analyzed
}

// readImageBytesForAnalysis supports both inline images (IM/embed/API) and
// pre-uploaded temporary image references (authenticated web chat).
func (h *Handler) readImageBytesForAnalysis(ctx context.Context, image ImageAttachment) ([]byte, error) {
	if image.Data != "" {
		imageBytes, _, err := decodeDataURI(image.Data)
		return imageBytes, err
	}
	if strings.TrimSpace(image.URL) == "" {
		return nil, fmt.Errorf("image has neither inline data nor a stored URL")
	}
	reader, err := h.fileService.GetFile(ctx, image.URL)
	if err != nil {
		return nil, fmt.Errorf("open stored image: %w", err)
	}
	defer reader.Close()
	imageBytes, err := io.ReadAll(io.LimitReader(reader, maxImageSize+1))
	if err != nil {
		return nil, fmt.Errorf("read stored image: %w", err)
	}
	if len(imageBytes) == 0 {
		return nil, fmt.Errorf("stored image is empty")
	}
	if len(imageBytes) > maxImageSize {
		return nil, fmt.Errorf("stored image exceeds %d bytes", maxImageSize)
	}
	return imageBytes, nil
}

// buildImageAnalysisPrompt generates a context-aware VLM prompt based on the
// user's question. Instead of doing generic OCR + Caption separately, we do a
// single analysis call that is tailored to the user's intent.
func buildImageAnalysisPrompt(userQuery string) string {
	if strings.TrimSpace(userQuery) == "" {
		return "请分析这张图片的内容。如果包含文字，请提取关键文字信息；如果是自然图片，请描述其主要内容。用简洁的中文回答。"
	}
	return fmt.Sprintf(
		"用户的问题是：%s\n\n请分析图片中与用户问题相关的内容。"+
			"如果图片包含文字/文档/表格，请提取与问题相关的关键信息。"+
			"如果是自然图片/截图/图表，请描述与问题相关的视觉内容。"+
			"用简洁的中文回答，只输出分析结果。",
		userQuery,
	)
}

func decodeDataURI(dataURI string) ([]byte, string, error) {
	if !strings.HasPrefix(dataURI, "data:") {
		return nil, "", fmt.Errorf("not a data URI")
	}
	idx := strings.Index(dataURI, ";base64,")
	if idx < 0 {
		return nil, "", fmt.Errorf("unsupported data URI encoding (expected base64)")
	}
	mimeType := dataURI[5:idx]
	decoded, err := base64.StdEncoding.DecodeString(dataURI[idx+8:])
	if err != nil {
		return nil, "", fmt.Errorf("base64 decode: %w", err)
	}
	ext := mimeToExt(mimeType)
	return decoded, ext, nil
}

func mimeToExt(mime string) string {
	switch strings.ToLower(mime) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func (h *Handler) resolveImageFileService(ctx context.Context, storageProvider string) interfaces.FileService {
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		return h.fileService
	}
	if h.storageResolver != nil {
		svc, resolvedProvider, err := h.storageResolver.ResolveFileService(ctx, tenant, "", storageProvider, "")
		if err == nil && svc != nil {
			logger.Infof(ctx, "[image-storage] using storage instance provider=%s for image uploads", resolvedProvider)
			return svc
		}
		if err != nil {
			logger.Warnf(ctx, "[image-storage] failed to resolve storage instance for provider=%s: %v", storageProvider, err)
		}
	}
	if strings.TrimSpace(storageProvider) == "" || tenant.StorageEngineConfig == nil {
		return h.fileService
	}

	svc, resolvedProvider, err := filesvc.NewFileServiceFromStorageConfig(storageProvider, tenant.StorageEngineConfig, "")
	if err != nil {
		logger.Warnf(ctx, "[image-storage] failed to create %s file service: %v, fallback to default", storageProvider, err)
		return h.fileService
	}
	logger.Infof(ctx, "[image-storage] using provider=%s for image uploads", resolvedProvider)
	return svc
}
