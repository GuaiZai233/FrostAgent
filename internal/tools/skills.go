package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/skills"
)

func SkillTools(store *skills.Store, backend sandbox.Backend, sessions *llm.SessionManager) []Tool {
	if store == nil {
		return nil
	}
	parameters := map[string]any{"type": "object", "properties": map[string]any{
		"name":    map[string]any{"type": "string", "description": "技能名称"},
		"path":    map[string]any{"type": "string", "description": "技能目录内的相对资源路径"},
		"command": map[string]any{"type": "string", "description": "在包含技能所有资源的沙箱目录内执行的 shell 命令"},
		"timeout": map[string]any{"type": "integer", "description": "超时秒数，默认 30，最大 120"},
	}, "required": []string{"name"}}
	makeTool := func(name, description string) Tool {
		return Tool{name: name, description: description, parameter: parameters, executeContext: func(ctx context.Context, args string) (string, error) {
			var request struct {
				Name    string `json:"name"`
				Path    string `json:"path"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			}
			if err := json.Unmarshal([]byte(args), &request); err != nil {
				return "", err
			}
			pkg, err := store.Enabled(request.Name)
			if err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			run, ok := llm.RunContextFromContext(ctx)
			if !ok || run.SessionID == "" || sessions == nil {
				return "", errors.New("技能工具需要当前会话")
			}
			session, ok := sessions.Get(run.SessionID)
			if !ok {
				return "", errors.New("当前会话不存在")
			}
			sess, ok := session.(*llm.SessionContext)
			if !ok {
				return "", errors.New("当前会话不支持技能")
			}
			if run.Epoch != 0 && sess.Epoch() != run.Epoch {
				return "", errors.New("会话已重置，技能调用已失效")
			}
			if run.SkillState != nil {
				sess = run.SkillState
			}
			if name == "load_skill" {
				if pkg.Info.ManualOnly {
					return "", errors.New("该技能仅允许用户通过 /" + pkg.Info.Name + " 手动调用")
				}
				if err := sess.ActivateSkill(pkg.Info.Name, pkg.Info.Revision, "", run.Epoch); err != nil {
					return "", err
				}
				return "技能已加载；正文将加入当前会话的系统上下文。\n" + strings.Join(pkg.Info.Warnings, "\n") + "\n资源文件: " + strings.Join(pkg.Info.Files[:min(len(pkg.Info.Files), 40)], ", "), nil
			}
			if !sess.HasActiveSkill(pkg.Info.Name, pkg.Info.Revision) {
				return "", errors.New("请先 load_skill 或由用户手动调用该技能")
			}
			if name == "read_skill_resource" {
				if !skills.ValidPath(request.Path) {
					return "", errors.New("资源路径无效")
				}
				data, exists := pkg.Files[request.Path]
				if !exists {
					return "", errors.New("资源不存在")
				}
				if len(data) > skills.MaxResourceBytes || !utf8.Valid(data) {
					return "", errors.New("资源为二进制或超过 48 KiB，请通过 run_skill 在沙箱中使用")
				}
				return string(data), nil
			}
			if request.Timeout == 0 {
				request.Timeout = 30
			}
			result, cwd, err := skills.Run(ctx, backend, run.SessionID, pkg, request.Command, time.Duration(request.Timeout)*time.Second)
			if err != nil {
				return "", fmt.Errorf("技能沙箱执行失败: %w", err)
			}
			stdout, outTruncated := boundStream(result.Stdout, executeCommandMaxStreamEncodedBytes)
			stderr, errTruncated := boundStream(result.Stderr, executeCommandMaxStreamEncodedBytes)
			encoded, err := json.Marshal(struct {
				CommandToolOutput
				Cwd string `json:"cwd"`
			}{CommandToolOutput{Stdout: stdout, Stderr: stderr, ExitCode: result.ExitCode, TimedOut: result.TimedOut, StdoutTruncated: result.StdoutTruncated, StderrTruncated: result.StderrTruncated, FrostAgentStdoutTruncated: outTruncated, FrostAgentStderrTruncated: errTruncated, DurationMs: result.Duration.Milliseconds()}, cwd})
			return string(encoded), err
		}}
	}
	return []Tool{
		makeTool("load_skill", "按需加载已启用的技能。读取技能描述判断是否适用；手动技能只能由用户通过 /技能名 触发。"),
		makeTool("read_skill_resource", "读取当前已加载技能内的文本资源，支持 references、scripts 等相对路径。"),
		makeTool("run_skill", "在隔离沙箱中准备当前技能全部文件，然后执行 shell 命令；SKILL_DIR 和 CLAUDE_SKILL_DIR 指向技能目录。需要启用沙箱，绝不在宿主机执行。"),
	}
}
