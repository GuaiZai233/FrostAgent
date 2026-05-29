package content

type MessageSegment struct {
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
	SubType  string         `json:"subType"`
	Url      string         `json:"url"`
	FileSize int64          `json:"file_size"`
}

type ContentBlock struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL map[string]string `json:"image_url,omitempty"`
}
