package storage

// sessionImageSchema stores conversation images for console hover previews.
// data is base64 text so SQLite and Postgres share one column type; size is the
// decoded byte length used for quota accounting and the storage-usage view.
var sessionImageSchema = []string{
	`CREATE TABLE session_images (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		key TEXT NOT NULL, content_type TEXT NOT NULL, size BIGINT NOT NULL, data TEXT NOT NULL,
		created_at BIGINT NOT NULL, last_used_at BIGINT NOT NULL,
		PRIMARY KEY (instance_id, key)
	)`,
	`CREATE INDEX session_images_used_idx ON session_images(instance_id, last_used_at)`,
}
