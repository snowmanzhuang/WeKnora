package im

import (
	"context"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type inboundImageTestAdapter struct {
	dataByKey map[string][]byte
	requests  []IncomingMessage
}

func (a *inboundImageTestAdapter) Platform() Platform { return PlatformFeishu }
func (a *inboundImageTestAdapter) VerifyCallback(*gin.Context) error {
	return nil
}
func (a *inboundImageTestAdapter) ParseCallback(*gin.Context) (*IncomingMessage, error) {
	return nil, nil
}
func (a *inboundImageTestAdapter) SendReply(context.Context, *IncomingMessage, *ReplyMessage) error {
	return nil
}
func (a *inboundImageTestAdapter) HandleURLVerification(*gin.Context) bool { return false }
func (a *inboundImageTestAdapter) DownloadFile(
	_ context.Context,
	msg *IncomingMessage,
) (io.ReadCloser, string, error) {
	a.requests = append(a.requests, *msg)
	return io.NopCloser(strings.NewReader(string(a.dataByKey[msg.FileKey]))), msg.FileName, nil
}

type inboundImageTestFileService struct {
	savedData   [][]byte
	savedTenant []uint64
	savedNames  []string
}

func (s *inboundImageTestFileService) CheckConnectivity(context.Context) error { return nil }
func (s *inboundImageTestFileService) SaveFile(
	context.Context,
	*multipart.FileHeader,
	uint64,
	string,
) (string, error) {
	return "", nil
}
func (s *inboundImageTestFileService) SaveBytes(
	_ context.Context,
	data []byte,
	tenantID uint64,
	name string,
	_ bool,
) (string, error) {
	s.savedData = append(s.savedData, append([]byte(nil), data...))
	s.savedTenant = append(s.savedTenant, tenantID)
	s.savedNames = append(s.savedNames, name)
	return "local://" + name, nil
}
func (s *inboundImageTestFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *inboundImageTestFileService) GetFileURL(context.Context, string) (string, error) {
	return "", nil
}
func (s *inboundImageTestFileService) DeleteFile(context.Context, string) error { return nil }
func (s *inboundImageTestFileService) CopyFile(
	context.Context,
	string,
	uint64,
	string,
) (string, error) {
	return "", nil
}

func TestPrepareIncomingImages_DownloadsPersistsAndBuildsQAPayload(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	adapter := &inboundImageTestAdapter{
		dataByKey: map[string][]byte{
			"img_first":  png,
			"img_second": png,
		},
	}
	fileService := &inboundImageTestFileService{}
	service := &Service{defaultFileSvc: fileService}
	msg := &IncomingMessage{
		Platform:    PlatformFeishu,
		MessageType: MessageTypeText,
		MessageID:   "message_1",
		Content:     "这是什么？",
		Images: []IncomingImage{
			{FileKey: "img_first", FileName: "first.png"},
			{FileKey: "img_second", FileName: "second.png"},
		},
	}

	err := service.prepareIncomingImages(
		context.Background(),
		msg,
		adapter,
		&types.Tenant{ID: 10000},
		&types.CustomAgent{},
	)
	require.NoError(t, err)
	require.Len(t, adapter.requests, 2)
	require.Equal(t, MessageTypeImage, adapter.requests[0].MessageType)
	require.Equal(t, "img_first", adapter.requests[0].FileKey)
	require.Len(t, fileService.savedData, 2)
	require.Equal(t, uint64(10000), fileService.savedTenant[0])
	require.True(t, strings.HasPrefix(fileService.savedNames[0], "chat-images/"))
	require.True(t, strings.HasSuffix(fileService.savedNames[0], ".png"))

	imageURLs, messageImages := imImagePayloads(msg.Images)
	require.Len(t, imageURLs, 2)
	require.True(t, strings.HasPrefix(imageURLs[0], "data:image/png;base64,"))
	require.Len(t, messageImages, 2)
	require.True(t, strings.HasPrefix(messageImages[0].URL, "local://chat-images/"))

	userMessage := createIMUserMessagePayload("session_1", msg.Content, "request_1", messageImages)
	require.Equal(t, "im", userMessage.Channel)
	require.Equal(t, messageImages, userMessage.Images)

	qaRequest := buildIMQARequest(
		&types.Session{ID: "session_1"},
		msg.Content,
		"assistant_1",
		"user_1",
		&types.CustomAgent{},
		nil,
		nil,
		imageURLs,
	)
	require.Equal(t, imageURLs, qaRequest.ImageURLs)
}

func TestDetectIMImage_RejectsNonImage(t *testing.T) {
	_, _, err := detectIMImage([]byte("not an image"))
	require.Error(t, err)
}
