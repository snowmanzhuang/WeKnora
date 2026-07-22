package im

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type channelSessionIsolationRow struct {
	ID          string `gorm:"primaryKey"`
	Platform    string
	UserID      string
	ChatID      string
	ThreadID    string
	SessionID   string
	TenantID    uint64
	AgentID     string
	IMChannelID string
	DeletedAt   *time.Time
}

func (channelSessionIsolationRow) TableName() string { return "im_channel_sessions" }

func newChannelSessionIsolationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&channelSessionIsolationRow{}))
	return db
}

func TestUserChannelSessionQueryIsolatesConcreteChannel(t *testing.T) {
	db := newChannelSessionIsolationDB(t)
	base := channelSessionIsolationRow{
		ID:        "mapping-a",
		Platform:  string(PlatformFeishu),
		UserID:    "user-1",
		ChatID:    "chat-1",
		TenantID:  10000,
		AgentID:   "builtin-quick-answer",
		SessionID: "session-a",
	}
	first := base
	first.IMChannelID = "channel-retina"
	second := base
	second.ID = "mapping-b"
	second.IMChannelID = "channel-cataract"
	second.SessionID = "session-b"
	require.NoError(t, db.Create(&first).Error)
	require.NoError(t, db.Create(&second).Error)

	msg := &IncomingMessage{Platform: PlatformFeishu, UserID: "user-1", ChatID: "chat-1"}
	var got ChannelSession
	err := userChannelSessionQuery(db, msg, 10000, "builtin-quick-answer", "channel-cataract").First(&got).Error
	require.NoError(t, err)
	require.Equal(t, "session-b", got.SessionID)
}

func TestThreadChannelSessionQueryIsolatesConcreteChannel(t *testing.T) {
	db := newChannelSessionIsolationDB(t)
	base := channelSessionIsolationRow{
		ID:        "mapping-a",
		Platform:  string(PlatformFeishu),
		UserID:    "user-1",
		ChatID:    "chat-1",
		ThreadID:  "thread-1",
		TenantID:  10000,
		AgentID:   "builtin-quick-answer",
		SessionID: "session-a",
	}
	first := base
	first.IMChannelID = "channel-retina"
	second := base
	second.ID = "mapping-b"
	second.IMChannelID = "channel-cataract"
	second.SessionID = "session-b"
	require.NoError(t, db.Create(&first).Error)
	require.NoError(t, db.Create(&second).Error)

	msg := &IncomingMessage{
		Platform: PlatformFeishu,
		ChatID:   "chat-1",
		ThreadID: "thread-1",
	}
	var got ChannelSession
	err := threadChannelSessionQuery(db, msg, 10000, "builtin-quick-answer", "channel-retina").First(&got).Error
	require.NoError(t, err)
	require.Equal(t, "session-a", got.SessionID)
}
