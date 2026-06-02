package tools

import "fmt"

func SendMsgTool() Tool {
	return Tool{
		Name:        "send_message",
		Description: "Delivers messages (plain, image, record, video, file, mention_user) to the user. Strictly use this for sending media or initiating proactive tasks; standard text replies must be output directly without invoking this tool.",
		//json schema
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"messages": map[string]any{
					"type":        "array",
					"description": "A sequence of message payload objects. Include a `mention_user` component to ping a specific user.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"description": "The payload format. Allowed values: plain, image, record (voice audio), video, file, mention_user.",
							},
							"text": map[string]any{
								"type":        "string",
								"description": "The string content when type is `plain`.",
							},
							"path": map[string]any{
								"type":        "string",
								"description": "The local or sandbox file path required for media/file types.",
							},
							"url": map[string]any{
								"type":        "string",
								"description": "The web link for media/file types.",
							},
							"mention_user_id": map[string]any{
								"type":        "string",
								"description": "The identifier of the user to ping (used with `mention_user` type).",
							},
						},
						"required": []string{"type"},
					},
				},
				"session": map[string]any{
					"type":        "string",
					"description": "Target session identifier (format: 'platform_id:message_type:session_id'). Omit to default to the active session.",
				},
			},
			"required": []string{"messages"},
		},

		Execute: func(args string) (string, error) {
			return fmt.Sprintf("消息已发送"), nil
		},
	}
}
