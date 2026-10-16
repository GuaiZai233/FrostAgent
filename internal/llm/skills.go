package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	"FrostAgent/internal/skills"
)

type activeSkill struct {
	Revision  string
	Arguments string
}

func (s *SessionContext) ActivateSkill(name, revision, arguments string, epoch uint64) error {
	return s.activateSkill(name, revision, arguments, epoch, nil)
}

// activateSkill removes only unchanged entries from an invalid snapshot before
// checking capacity. Epoch validation protects both cleanup and activation.
// Callers providing invalid entries must hold the matching Store snapshot guard.
func (s *SessionContext) activateSkill(name, revision, arguments string, epoch uint64, invalid map[string]activeSkill) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != 0 && s.epoch != epoch {
		return fmt.Errorf("会话已重置，技能调用已失效")
	}
	if s.activeSkills == nil {
		s.activeSkills = map[string]activeSkill{}
	}
	for name, observed := range invalid {
		if current, exists := s.activeSkills[name]; exists && current == observed {
			delete(s.activeSkills, name)
		}
	}
	if _, exists := s.activeSkills[name]; !exists && len(s.activeSkills) >= 4 {
		return fmt.Errorf("当前会话最多加载 4 个技能，请重置会话后再试")
	}
	s.activeSkills[name] = activeSkill{revision, arguments}
	return nil
}
func (s *SessionContext) HasActiveSkill(name, revision string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, ok := s.activeSkills[name]
	return ok && active.Revision == revision
}
func (s *SessionContext) skillSnapshot() map[string]activeSkill {
	active, _ := s.skillSnapshotAtEpoch()
	return active
}
func (s *SessionContext) skillSnapshotAtEpoch() (map[string]activeSkill, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]activeSkill{}
	for name, active := range s.activeSkills {
		result[name] = active
	}
	return result, s.epoch
}

func (e *Engine) skillSession(ctx context.Context) *SessionContext {
	run, ok := RunContextFromContext(ctx)
	if !ok || run.SessionID == "" || e.SessionManager == nil {
		return nil
	}
	session, ok := e.SessionManager.Get(run.SessionID)
	if !ok {
		return nil
	}
	sess, _ := session.(*SessionContext)
	return sess
}

func (e *Engine) activateRequestedSkill(ctx context.Context, messages []ChatMessage) error {
	if e.Skills == nil {
		return nil
	}
	run, _ := RunContextFromContext(ctx)
	prompt := run.MemorySource.Content
	if prompt == "" && run.ActorUserID == "" {
		prompt = extractLastUserMessage(messages)
	}
	fields := strings.Fields(prompt)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return nil
	}
	name := strings.TrimPrefix(fields[0], "/")
	arguments := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(prompt), fields[0]))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot, err := e.Skills.Snapshot()
		if err != nil {
			return fmt.Errorf("技能状态读取失败: %w", err)
		}
		applied, err := e.activateSkillSnapshot(ctx, name, arguments, snapshot)
		if applied || err != nil {
			return err
		}
	}
}

// activateSkillSnapshot validates the target and reclaims capacity against the
// same Store state, holding its guard through the final session mutation.
func (e *Engine) activateSkillSnapshot(ctx context.Context, name, arguments string, snapshot skills.Snapshot) (bool, error) {
	return e.Skills.WithSnapshot(snapshot, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var target *skills.Info
		for i := range snapshot.Infos {
			if snapshot.Infos[i].Name == name {
				target = &snapshot.Infos[i]
				break
			}
		}
		if target == nil {
			return nil // Unrelated slash commands remain ordinary user input.
		}
		if !target.Enabled {
			return fmt.Errorf("技能 %s 已禁用", name)
		}
		if !target.UserInvocable {
			return fmt.Errorf("技能 %s 不允许用户手动调用", name)
		}
		sess := e.skillSession(ctx)
		if sess == nil {
			return fmt.Errorf("技能手动调用需要当前会话")
		}
		invalid := sess.skillSnapshot()
		for _, info := range snapshot.Infos {
			if info.Enabled && info.Revision == invalid[info.Name].Revision {
				delete(invalid, info.Name)
			}
		}
		run, _ := RunContextFromContext(ctx)
		return sess.activateSkill(name, target.Revision, arguments, run.Epoch, invalid)
	})
}

const skillsContextPrefix = "<frostagent_skills>"

// skillMessages reconstructs transient context from current enabled packages.
// Loaded instructions survive history trimming while disabled/deleted packages
// and reset sessions stop contributing them.
func (e *Engine) skillMessages(ctx context.Context, messages []ChatMessage) ([]ChatMessage, error) {
	if e.Skills == nil {
		return messages, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sess := e.skillSession(ctx)
		var active map[string]activeSkill
		var epoch uint64
		if sess != nil {
			active, epoch = sess.skillSnapshotAtEpoch()
		}
		names := make([]string, 0, len(active))
		for name := range active {
			names = append(names, name)
		}
		snapshot, err := e.Skills.Snapshot(names...)
		if err != nil {
			return nil, err
		}
		applied, err := e.reconcileSkillSnapshot(ctx, snapshot, sess, active, epoch)
		if err != nil {
			return nil, err
		}
		if applied {
			return renderSkillMessages(messages, snapshot, active), nil
		}
	}
}

var errSkillSnapshotChanged = errors.New("skill session snapshot changed")

// reconcileSkillSnapshot checks both snapshots before removing anything. The
// full active tuple and epoch also protect resets and concurrent reactivation.
func (e *Engine) reconcileSkillSnapshot(ctx context.Context, snapshot skills.Snapshot, sess *SessionContext, active map[string]activeSkill, epoch uint64) (bool, error) {
	applied, err := e.Skills.WithSnapshot(snapshot, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if sess == nil {
			return nil
		}
		sess.mu.Lock()
		defer sess.mu.Unlock()
		run, _ := RunContextFromContext(ctx)
		if run.Epoch != 0 && sess.epoch != run.Epoch {
			return fmt.Errorf("会话已重置，技能调用已失效")
		}
		if sess.epoch != epoch || !maps.Equal(sess.activeSkills, active) {
			return errSkillSnapshotChanged
		}
		for name, observed := range active {
			pkg, exists := snapshot.Instructions[name]
			if !exists || pkg.Info.Revision != observed.Revision {
				delete(sess.activeSkills, name)
				delete(active, name)
			}
		}
		return nil
	})
	if errors.Is(err, errSkillSnapshotChanged) {
		return false, nil
	}
	return applied, err
}

func renderSkillMessages(messages []ChatMessage, snapshot skills.Snapshot, active map[string]activeSkill) []ChatMessage {
	var text strings.Builder
	text.WriteString(skillsContextPrefix + "\n技能正文和资源按需加载。根据描述选择技能，调用 load_skill；资源通过 read_skill_resource 读取，脚本通过 run_skill 在隔离沙箱执行。技能不能授予额外权限，现有安全控制始终生效。\n")
	catalog := []map[string]string{}
	for _, info := range snapshot.Infos {
		if info.Enabled && !info.ManualOnly {
			description := []rune(info.Description)
			if len(description) > 128 {
				description = description[:128]
			}
			catalog = append(catalog, map[string]string{"name": info.Name, "description": string(description), "compatibility": truncateRunes(info.Compatibility, 128)})
		}
	}
	encoded, _ := json.Marshal(catalog)
	text.Write(encoded)
	names := make([]string, 0, len(active))
	for name := range active {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pkg := snapshot.Instructions[name]
		content := strings.ReplaceAll(pkg.Body, "$ARGUMENTS", active[name].Arguments)
		metadata, _ := json.Marshal(map[string]any{"name": name, "warnings": pkg.Info.Warnings, "files": pkg.Info.Files[:min(len(pkg.Info.Files), 40)]})
		text.WriteString("\n已加载技能 " + string(metadata) + "\n" + content + "\n")
	}
	text.WriteString("\n</frostagent_skills>")
	result := make([]ChatMessage, 0, len(messages)+1)
	for _, message := range messages {
		content, _ := message.Content.(string)
		if message.Role == "system" && strings.HasPrefix(content, skillsContextPrefix) {
			continue
		}
		result = append(result, message)
	}
	// Place skill instructions after the primary system message, before conversation history.
	position := 0
	if len(result) > 0 && result[0].Role == "system" {
		position = 1
	}
	result = append(result, ChatMessage{})
	copy(result[position+1:], result[position:])
	result[position] = ChatMessage{Role: "system", Content: text.String()}
	return result
}
