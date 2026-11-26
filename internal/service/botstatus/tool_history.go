package botstatus

import (
	"context"
	"database/sql"
	"errors"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

// GetToolHistoryEntry loads parameters and original results only when an
// authenticated inspector expands a call. Quarantined records and private
// diagnostics are never returned.
func (s *Service) GetToolHistoryEntry(ctx context.Context, req *connect.Request[v1.GetToolHistoryEntryRequest]) (*connect.Response[v1.GetToolHistoryEntryResponse], error) {
	ref := req.Msg.GetRefId()
	if !toolhistory.ValidRef(ref) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("工具调用记录 ID 无效"))
	}
	if s.engine.ToolHistory == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("工具历史暂不可用"))
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	record, err := s.engine.ToolHistory.ReadForInspection(readCtx, ref)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("当前实例中未找到该工具记录"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("工具参数读取失败"))
	}
	visible := record.ForModel()
	available := !record.Restricted && record.State == "complete"
	response := &v1.GetToolHistoryEntryResponse{RefId: record.RefID, Tool: record.Tool,
		ArgumentsAvailable: available, ArgumentsPending: record.State != "complete"}
	if !req.Msg.GetIncludeResult() {
		response.ArgumentsRaw = visible.ArgumentsRaw
		return connect.NewResponse(response), nil
	}
	offset := int(req.Msg.GetResultOffset())
	if offset < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("结果偏移量无效"))
	}
	response.ResultAvailable = available
	if !available {
		return connect.NewResponse(response), nil
	}
	page, total, end, ok := resultPage(visible.ResultRaw, offset, toolHistoryResultPageChars)
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("结果偏移量超过记录长度"))
	}
	response.ResultRaw = page
	response.ResultOffset = int32(offset)
	response.ResultTotalChars = int32(total)
	response.ResultHasMore = end < total
	if response.ResultHasMore {
		response.ResultNextOffset = int32(end)
	}
	return connect.NewResponse(response), nil
}

// resultPage returns up to limit characters of text starting at character
// offset, along with the total character count and the offset just past the
// page. Pages are measured in characters, matching read_tool_history, so
// callers can concatenate pages without splitting a UTF-8 sequence. It scans
// the string in place instead of converting the whole result to runes.
func resultPage(text string, offset, limit int) (page string, total, end int, ok bool) {
	total = utf8.RuneCountInString(text)
	if offset > total {
		return "", total, 0, false
	}
	start := 0
	for skipped := 0; skipped < offset; skipped++ {
		_, size := utf8.DecodeRuneInString(text[start:])
		start += size
	}
	stop, taken := start, 0
	for stop < len(text) && taken < limit {
		_, size := utf8.DecodeRuneInString(text[stop:])
		stop += size
		taken++
	}
	return text[start:stop], total, offset + taken, true
}

// toolHistoryResultPageChars matches the read_tool_history page size, so a
// result page is bounded regardless of how large the archived result is.
const toolHistoryResultPageChars = 8000


// sessionTimelineToolNotes projects archived calls for the message IDs that are
// actually present in the inspector window. It returns metadata-only notes;
// arguments and results remain behind GetToolHistoryEntry's existing gate.
func (s *Service) sessionTimelineToolNotes(ctx context.Context, sessionID string, messages []string) ([]*v1.SessionHistoryMessageProto, error) {
	if s.engine.ToolHistory == nil || len(messages) == 0 {
		return nil, nil
	}
	scope, ok := chatHistoryScope(sessionID)
	if !ok {
		return nil, nil
	}
	visible := make(map[string]bool, len(messages))
	for _, id := range messages {
		if id != "" {
			visible[id] = true
		}
	}
	sources, err := s.engine.ToolHistory.RecentBySource(ctx, scope, 100, 50)
	if err != nil {
		return nil, err
	}
	type runNotes struct {
		runID string
		source string
		refs []string
	}
	runs := make([]runNotes, 0)
	byRun := make(map[string]int)
	for _, source := range sources {
		if !visible[source.SourceMessageID] {
			continue
		}
		for _, ref := range source.Refs {
			record, readErr := s.engine.ToolHistory.Read(ctx, scope, ref)
			if readErr != nil {
				if errors.Is(readErr, sql.ErrNoRows) {
					continue
				}
				return nil, readErr
			}
			key := record.RunID
			if key == "" {
				key = record.RefID
			}
			index, exists := byRun[key]
			if !exists {
				index = len(runs)
				byRun[key] = index
				runs = append(runs, runNotes{runID: key, source: record.SourceMessageID})
			}
			runs[index].refs = append(runs[index].refs, record.RefID+"\x00"+record.Tool)
		}
	}
	result := make([]*v1.SessionHistoryMessageProto, 0, len(runs))
	for _, run := range runs {
		sourceJSON, _ := json.Marshal(run.source)
		var body strings.Builder
		body.WriteString("【工具执行记录】\n来源消息: ")
		body.Write(sourceJSON)
		for _, item := range run.refs {
			parts := strings.SplitN(item, "\x00", 2)
			if len(parts) != 2 {
				continue
			}
			label := "工具调用"
			if parts[1] == "read_tool_history" {
				label = "历史回查"
			}
			fmt.Fprintf(&body, "\n- %s: %s (ref_id: %s)", label, parts[1], parts[0])
		}
		if len(run.refs) > 0 {
			result = append(result, &v1.SessionHistoryMessageProto{Role: "assistant", Content: body.String(), ToolHistoryRunId: run.runID})
		}
	}
	return result, nil
}
