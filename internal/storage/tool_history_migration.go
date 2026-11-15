package storage

var toolHistorySchema = []string{
	`CREATE TABLE tool_history (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		ref_id TEXT NOT NULL, platform TEXT NOT NULL, message_type TEXT NOT NULL,
		target_id TEXT NOT NULL, source_message_id TEXT NOT NULL,
		occurred_at BIGINT NOT NULL, record_json TEXT NOT NULL,
		PRIMARY KEY (instance_id, ref_id)
	)`,
	`CREATE INDEX tool_history_message_idx ON tool_history(instance_id, platform, message_type, target_id, source_message_id, occurred_at, ref_id)`,
}
