-- Migration: 000074_im_thread_user_index
-- Description: Keep user-mode uniqueness from blocking multiple thread sessions.

DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id, agent_id, im_channel_id)
    WHERE deleted_at IS NULL AND thread_id = '';
