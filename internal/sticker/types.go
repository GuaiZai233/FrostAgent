package sticker

import "time"

type Status string

const (
	StatusReady        Status = "ready"
	StatusUnsummarized Status = "unsummarized"
)

type Entry struct {
	ID                     string   `json:"id"`
	FileName               string   `json:"file_name"`
	Description            string   `json:"description"`
	Keywords               []string `json:"keywords"`
	Weight                 int      `json:"weight"`
	Status                 Status   `json:"status"`
	SuspectedInappropriate bool     `json:"suspected_inappropriate,omitempty"`
	CreatedAt              int64    `json:"created_at"`
	UpdatedAt              int64    `json:"updated_at"`
}

func newEntry(id, fileName string) Entry {
	now := time.Now().Unix()
	return Entry{
		ID:        id,
		FileName:  fileName,
		Weight:    1,
		Status:    StatusUnsummarized,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

type Stats struct {
	Total        int `json:"total"`
	Ready        int `json:"ready"`
	Unsummarized int `json:"unsummarized"`
}
