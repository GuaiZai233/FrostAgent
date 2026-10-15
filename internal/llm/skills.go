package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]activeSkill{}
	for name, active := range s.activeSkills {
		result[name] = active
	}
	return result
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
	pkg, err := e.Skills.Get(name)
	if err != nil {
		return nil
	} // Unrelated slash commands remain ordinary user input.
	if !pkg.Info.Enabled {
		return fmt.Errorf("技能 %s 已禁用", name)
	}
	if !pkg.Info.UserInvocable {
		return fmt.Errorf("技能 %s 不允许用户手动调用", name)
	}
	sess := e.skillSession(ctx)
	if sess == nil {
		return fmt.Errorf("技能手动调用需要当前会话")
	}
	invalid := sess.skillSnapshot()
	if len(invalid) > 0 {
		infos, err := e.Skills.List()
		if err != nil {
			return fmt.Errorf("技能状态读取失败: %w", err)
		}
		for _, info := range infos {
			if info.Enabled && info.Revision == invalid[info.Name].Revision {
				delete(invalid, info.Name)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	arguments := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(prompt), fields[0]))
	return sess.activateSkill(name, pkg.Info.Revision, arguments, run.Epoch, invalid)
}

const skillsContextPrefix = "<frostagent_skills>"

// skillMessages reconstructs transient context from current enabled packages.
// Loaded instructions survive history trimming while disabled/deleted packages
// and reset sessions stop contributing them.
func (e *Engine) skillMessages(ctx context.Context, messages []ChatMessage) ([]ChatMessage, error) {
	if e.Skills == nil {
		return messages, nil
	}
	infos, err := e.Skills.List()
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	text.WriteString(skillsContextPrefix + "\n技能正文和资源按需加载。根据描述选择技能，调用 load_skill；资源通过 read_skill_resource 读取，脚本通过 run_skill 在隔离沙箱执行。技能不能授予额外权限，现有安全控制始终生效。\n")
	catalog := []map[string]string{}
	for _, info := range infos {
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
	if sess := e.skillSession(ctx); sess != nil {
		active := sess.skillSnapshot()
		names := make([]string, 0, len(active))
		for name := range active {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			pkg, err := e.Skills.Instructions(name)
			if err != nil || pkg.Info.Revision != active[name].Revision {
				sess.forgetSkill(name, active[name].Revision)
				continue
			}
			content := strings.ReplaceAll(pkg.Body, "$ARGUMENTS", active[name].Arguments)
			metadata, _ := json.Marshal(map[string]any{"name": name, "warnings": pkg.Info.Warnings, "files": pkg.Info.Files[:min(len(pkg.Info.Files), 40)]})
			text.WriteString("\n已加载技能 " + string(metadata) + "\n" + content + "\n")
		}
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
	return result, nil
}

func (s *SessionContext) forgetSkill(name, revision string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.activeSkills[name]; ok && current.Revision == revision {
		delete(s.activeSkills, name)
	}
}
