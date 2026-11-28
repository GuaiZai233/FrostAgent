package llm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
)

type checkpointGroupItem struct {
	Sequence uint64
	Message  GroupCompactMessage
	Raw      string
}

type checkpointPrivateCompact struct {
	SummarySeq uint64
	AckSeq     uint64
	Batch      SummaryGroup
}

// SessionCheckpoint contains durable conversation data, never locks, execution
// lanes, callbacks, pending side effects, or active goroutine state.
type SessionCheckpoint struct {
	Version             int
	Deleted             bool
	ConversationID      string
	History             []ChatMessage
	HistoryRecordedAt   []time.Time `json:",omitempty"`
	HistorySeq          []uint64
	NextMsgSeq          uint64
	Epoch               uint64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ActiveSkills        map[string]activeSkill
	GroupName           string
	GroupSummary        string
	GroupBuffer         []checkpointGroupItem
	GroupSequence       uint64
	GroupGeneration     uint64
	SummaryGroups       []SummaryGroup
	PreviousSummary     string
	PreviousGroups      []SummaryGroup
	CommittedMessageIDs []string
	CommittedSenderIDs  []string
	RevokedMessageIDs   map[string]struct{}
	PrivateCompact      *checkpointPrivateCompact
	SystemPrompt        string
	UserPrompt          string
	Model               string
	DeliveryFailure     *DeliveryFailure
	ActualRequest       json.RawMessage
	RequestAt           time.Time
	SummaryRaw          [][]string
	PreviousRaw         [][]string
	PrivateRaw          [][]string
}

func (s *SessionContext) checkpointLocked() SessionCheckpoint {
	if s.checkpointDeleted {
		return SessionCheckpoint{Version: 1, Deleted: true, ConversationID: s.ConversationID, Epoch: s.epoch, UpdatedAt: s.UpdatedAt}
	}
	result := SessionCheckpoint{
		Version: 1, Deleted: s.checkpointDeleted, ConversationID: s.ConversationID,
		History: s.History, HistorySeq: s.historySeq, NextMsgSeq: s.nextMsgSeq, Epoch: s.epoch,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, ActiveSkills: s.activeSkills,
		GroupName: s.groupName, GroupSummary: s.groupCompactSummary,
		GroupSequence: s.groupCompactSequence, GroupGeneration: s.groupCompactGeneration,
		SummaryGroups: s.groupSummaryGroups, PreviousSummary: s.prevGroupCompactSummary,
		PreviousGroups: s.prevGroupSummaryGroups, CommittedMessageIDs: s.lastCommittedMessageIDs,
		CommittedSenderIDs: s.lastCommittedSenderIDs, RevokedMessageIDs: s.revokedMessageIDs,
		SystemPrompt: s.lastSystemPrompt, UserPrompt: s.lastUserPrompt, Model: s.lastModelName,
		DeliveryFailure: s.deliveryFailure,
		ActualRequest:   s.lastActualRequest, RequestAt: s.lastRequestAt,
	}
	for i, message := range s.History {
		if !message.RecordedAt.IsZero() {
			if result.HistoryRecordedAt == nil {
				result.HistoryRecordedAt = make([]time.Time, len(s.History))
			}
			result.HistoryRecordedAt[i] = message.RecordedAt
		}
	}
	for _, item := range s.groupCompactBuffer {
		// Staged messages have not passed admission yet and must not become
		// trusted history merely because the process restarted.
		if !item.staged {
			result.GroupBuffer = append(result.GroupBuffer, checkpointGroupItem{item.sequence, item.message, item.message.raw})
		}
	}
	if compact := s.privateCompact; compact != nil {
		result.PrivateCompact = &checkpointPrivateCompact{compact.summarySeq, compact.ackSeq, compact.batch}
		result.PrivateRaw = summaryRaw([]SummaryGroup{compact.batch})
	}
	result.SummaryRaw = summaryRaw(result.SummaryGroups)
	result.PreviousRaw = summaryRaw(result.PreviousGroups)
	return result
}

func summaryRaw(groups []SummaryGroup) [][]string {
	var result [][]string
	for _, group := range groups {
		var raw []string
		for _, message := range group.StructuredMsgs {
			raw = append(raw, message.raw)
		}
		result = append(result, raw)
	}
	return result
}

// unlockCheckpoint captures one coherent revision under the history lock, then
// releases it before local disk I/O. Store.Save rejects reordered callbacks.
func (s *SessionContext) unlockCheckpoint() {
	store := s.checkpoints
	if store == nil || s.IsMock() {
		s.mu.Unlock()
		return
	}
	payload, err := json.Marshal(s.checkpointLocked())
	hash := sha256.Sum256(payload)
	if err != nil || hash == s.checkpointHash {
		s.mu.Unlock()
		if err != nil {
			s.scope.Log().Warn(logs.SYSTEM, "会话快照编码失败，聊天继续")
		}
		return
	}
	s.checkpointHash = hash
	s.checkpointRevision++
	// A deleted session must never write again after a new session takes its ID.
	if s.checkpointDeleted {
		s.checkpoints = nil
	}
	record := sessionstate.Record{SessionID: memory.CanonicalSessionKey(s.ConversationID), Revision: s.checkpointRevision, Payload: payload}
	s.mu.Unlock()
	if err := store.Save(record); err != nil {
		s.scope.Log().Warn(logs.SYSTEM, "本地会话快照保存失败，聊天继续；未提交内容可能无法跨重启恢复")
	}
}

func (sm *SessionManager) EnablePersistence(store *sessionstate.Store) error {
	store.SetValidator(ValidateCheckpoint)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	records, loadErr := store.Load(ctx)
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.Checkpoints = store
	for _, record := range records {
		var snapshot SessionCheckpoint
		if err := json.Unmarshal(record.Payload, &snapshot); err != nil || snapshot.Version != 1 ||
			memory.CanonicalSessionKey(snapshot.ConversationID) != record.SessionID {
			loadErr = fmt.Errorf("invalid session checkpoint; some sessions could not be restored")
			continue
		}
		if snapshot.Deleted {
			continue
		}
		sm.cold[record.SessionID] = snapshot.UpdatedAt
		sm.groupNames[record.SessionID] = snapshot.GroupName
	}
	return loadErr
}

func ValidateCheckpoint(record sessionstate.Record) error {
	var snapshot SessionCheckpoint
	if err := json.Unmarshal(record.Payload, &snapshot); err != nil {
		return err
	}
	if snapshot.Version != 1 || record.SessionID == "" || memory.CanonicalSessionKey(snapshot.ConversationID) != record.SessionID || record.Revision < 1 {
		return fmt.Errorf("invalid session checkpoint identity or version")
	}
	if len(snapshot.HistoryRecordedAt) != 0 && len(snapshot.HistoryRecordedAt) != len(snapshot.History) {
		return fmt.Errorf("invalid session checkpoint history timestamps")
	}
	return nil
}

func (sm *SessionManager) restoreCheckpoint(record sessionstate.Record) (*SessionContext, error) {
	var snapshot SessionCheckpoint
	if err := json.Unmarshal(record.Payload, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.Version != 1 || snapshot.Deleted || memory.CanonicalSessionKey(snapshot.ConversationID) != record.SessionID {
		return nil, fmt.Errorf("invalid session checkpoint")
	}
	if len(snapshot.HistoryRecordedAt) != 0 && len(snapshot.HistoryRecordedAt) != len(snapshot.History) {
		return nil, fmt.Errorf("invalid session checkpoint history timestamps")
	}
	for i := range snapshot.History {
		if len(snapshot.HistoryRecordedAt) > 0 {
			snapshot.History[i].RecordedAt = snapshot.HistoryRecordedAt[i]
		}
		if _, ok := snapshot.History[i].Content.([]any); ok {
			data, err := json.Marshal(snapshot.History[i].Content)
			if err != nil {
				return nil, err
			}
			var parts []core.ContentPart
			if err := json.Unmarshal(data, &parts); err != nil {
				return nil, err
			}
			snapshot.History[i].Content = parts
		}
	}
	restoreRaw := func(groups []SummaryGroup, raw [][]string) {
		for i := range groups {
			for j := range groups[i].StructuredMsgs {
				if i < len(raw) && j < len(raw[i]) {
					groups[i].StructuredMsgs[j].raw = raw[i][j]
				}
			}
		}
	}
	restoreRaw(snapshot.SummaryGroups, snapshot.SummaryRaw)
	restoreRaw(snapshot.PreviousGroups, snapshot.PreviousRaw)
	if snapshot.PrivateCompact != nil {
		restoreRaw([]SummaryGroup{snapshot.PrivateCompact.Batch}, snapshot.PrivateRaw)
	}
	session := &SessionContext{
		ConversationID: snapshot.ConversationID, History: snapshot.History,
		historySeq: snapshot.HistorySeq, nextMsgSeq: snapshot.NextMsgSeq, epoch: snapshot.Epoch,
		CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt, activeSkills: snapshot.ActiveSkills,
		groupName: snapshot.GroupName, groupCompactSummary: snapshot.GroupSummary,
		groupCompactSequence: snapshot.GroupSequence, groupCompactGeneration: snapshot.GroupGeneration,
		groupSummaryGroups: snapshot.SummaryGroups, prevGroupCompactSummary: snapshot.PreviousSummary,
		prevGroupSummaryGroups: snapshot.PreviousGroups, lastCommittedMessageIDs: snapshot.CommittedMessageIDs,
		lastCommittedSenderIDs: snapshot.CommittedSenderIDs, revokedMessageIDs: snapshot.RevokedMessageIDs,
		lastSystemPrompt: snapshot.SystemPrompt, lastUserPrompt: snapshot.UserPrompt, lastModelName: snapshot.Model,
		deliveryFailure: snapshot.DeliveryFailure, scope: sm.Scope,
		lastActualRequest: snapshot.ActualRequest, lastRequestAt: snapshot.RequestAt,
		checkpoints: sm.Checkpoints, checkpointRevision: record.Revision, checkpointHash: sha256.Sum256(record.Payload),
	}
	for _, item := range snapshot.GroupBuffer {
		item.Message.raw = item.Raw
		session.groupCompactBuffer = append(session.groupCompactBuffer, groupCompactItem{sequence: item.Sequence, message: item.Message})
	}
	if compact := snapshot.PrivateCompact; compact != nil {
		session.privateCompact = &privateCompactInspection{compact.SummarySeq, compact.AckSeq, compact.Batch}
	}
	if len(session.historySeq) != len(session.History) {
		session.historySeq = make([]uint64, len(session.History))
		for i := range session.History {
			session.nextMsgSeq++
			session.historySeq[i] = session.nextMsgSeq
		}
	}
	return session, nil
}

// SetLastActualRequest preserves the latest provider-neutral request, including
// inline images, independently of the bounded log archive. It never resumes work.
// Audio and video are request-local and replaced by placeholders.
func (s *SessionContext) SetLastActualRequest(epoch uint64, model string, request core.ChatRequest) {
	request.Messages = core.WithoutInlineMedia(request.Messages)
	data, err := json.Marshal(struct {
		Model   string           `json:"model"`
		Request core.ChatRequest `json:"request"`
	}{model, request})
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.unlockCheckpoint()
	if s.IsMock() || (epoch != 0 && epoch != s.epoch) {
		return
	}
	s.lastActualRequest = data
	s.lastRequestAt = time.Now()
}

func (s *SessionContext) LastActualRequest() (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.lastActualRequest), s.lastRequestAt
}

func (sm *SessionManager) restoreColdLocked(id string) (*SessionContext, bool) {
	if _, ok := sm.cold[id]; !ok {
		return nil, false
	}
	record, err := sm.Checkpoints.Read(id)
	if err == nil {
		var session *SessionContext
		session, err = sm.restoreCheckpoint(record)
		if err == nil {
			sm.sessions[id] = session
			delete(sm.cold, id)
			return session, true
		}
	}
	sm.Log().Warn(logs.SYSTEM, "会话快照暂时无法恢复，已停止该会话以保护历史，请检查存储状态")
	// Keep the cold ID so the next request can retry recovery. This transient
	// context cannot persist or execute work and is never a new live session.
	return &SessionContext{ConversationID: id, scope: sm.Scope, epoch: 1, checkpointReadErr: err}, true
}

func (s *SessionContext) hasLiveSequencesLocked() bool {
	for _, lane := range s.sequences {
		lane.mu.Lock()
		active := lane.tail != nil || lane.active != nil
		lane.mu.Unlock()
		if active {
			return true
		}
	}
	return false
}

// InspectionContent renders multimodal content without Go pointer addresses.
// Full attachment bytes remain in history/checkpoint and actual request JSON.
func InspectionContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []core.ContentPart:
		return userContentText(value)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return "[无法显示的内容]"
		}
		return string(data)
	}
}

func (sm *SessionManager) HasCheckpoint(id string) bool {
	return sm != nil && sm.Checkpoints != nil && sm.Checkpoints.Revision(memory.CanonicalSessionKey(id)) > 0
}

// RecoveryError identifies a transient context returned for an existing session
// whose durable copies cannot currently be recovered. It is immutable.
func (s *SessionContext) RecoveryError() error {
	if s == nil {
		return nil
	}
	return s.checkpointReadErr
}
