package memory

import "time"

// Config holds memory system configuration.
type Config struct {
	MaxEntries      int           // 全局最大记忆条数（默认 500）
	ReflectInterval time.Duration // 反思触发间隔（默认 6h）
	RecallLimit     int           // 每次召回的最大记忆数（默认 10）
	ImportanceDecay float64       // 重要度衰减系数（默认 0.95）
	StoragePath     string        // brain.json 存储路径
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxEntries:      500,
		ReflectInterval: 6 * time.Hour,
		RecallLimit:     10,
		ImportanceDecay: 0.95,
		StoragePath:     "internal/memory/storage/brain.json",
	}
}
