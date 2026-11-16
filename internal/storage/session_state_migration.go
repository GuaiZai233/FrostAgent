package storage

var sessionStateSchema = []string{
	`CREATE TABLE session_states (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		session_id TEXT NOT NULL, revision BIGINT NOT NULL, payload TEXT NOT NULL,
		PRIMARY KEY (instance_id, session_id)
	)`,
}
