package storage

var chatHistorySchema = []string{
	`CREATE TABLE chat_history (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		id TEXT NOT NULL, platform TEXT NOT NULL, message_type TEXT NOT NULL,
		target_id TEXT NOT NULL, message_id TEXT NOT NULL, sender_id TEXT NOT NULL,
		sender_name TEXT NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL,
		search_text TEXT NOT NULL, reply_to TEXT NOT NULL, occurred_at BIGINT NOT NULL,
		delivery_status TEXT NOT NULL, PRIMARY KEY (instance_id, id)
	)`,
	`CREATE INDEX chat_history_conversation_idx ON chat_history(instance_id, platform, message_type, target_id, occurred_at DESC, id DESC)`,
}
