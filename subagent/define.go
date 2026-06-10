package subagent

type MessageType = int

const (
	MsgPrompt MessageType = iota
	MsgFinish
)

type Message struct {
	Sender    string
	Receiver  string
	Payload   string
	SessionID string
}
