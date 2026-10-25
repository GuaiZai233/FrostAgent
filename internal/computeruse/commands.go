package computeruse

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"FrostAgent/internal/core"
	"FrostAgent/internal/security"
)

// Candidate also drops privileged-looking commands from untrusted/group/mock
// ingress, so their source text never enters ordinary model/tool logging.
func Candidate(text string) bool {
	fields := strings.Fields(text)
	return len(fields) > 0 && fields[0] == "/computer"
}
func (c *Controller) AuthenticateTransport(authorization string) string {
	c.mu.Lock()
	token := c.config.AdapterToken
	c.mu.Unlock()
	if len(token) < 32 {
		return ""
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	provided := sha256.Sum256([]byte(authorization))
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		return ""
	}
	proof := sha256.Sum256([]byte("computer-use-adapter-proof\x00" + token))
	return hex.EncodeToString(proof[:])
}
func (c *Controller) trustedTransport(proof string) bool {
	c.mu.Lock()
	token := c.config.AdapterToken
	c.mu.Unlock()
	if len(token) < 32 || proof == "" {
		return false
	}
	expected := sha256.Sum256([]byte("computer-use-adapter-proof\x00" + token))
	return subtle.ConstantTimeCompare([]byte(proof), []byte(hex.EncodeToString(expected[:]))) == 1
}
func (c *Controller) HandleCommand(ctx context.Context, input core.ComputerUseIngress) (bool, string) {
	if !Candidate(input.Text) {
		return false, ""
	}
	a := actor{"qq_private", input.UserID}
	if input.Mock || input.Proactive || input.MessageType != "private" || security.CanonicalPlatform(input.Platform) != "qq" || !c.trustedTransport(input.TransportProof) || !c.allowed(a) {
		return true, ""
	}
	parts := strings.SplitN(strings.TrimSpace(input.Text), " ", 4)
	if len(parts) < 2 {
		return true, commandUsage
	}
	switch parts[1] {
	case "start":
		if len(parts) != 4 {
			return true, commandUsage
		}
		t, err := c.start(a, parts[2], parts[3])
		if err != nil {
			return true, publicError(err)
		}
		return true, Disclosure + "\n" + taskSummary(t) + " 请用 /computer status 查看状态，或在工作台查看结果。"
	case "pause":
		if len(parts) < 3 {
			return true, commandUsage
		}
		t, err := c.pause(ctx, parts[2], "owner_paused")
		if err != nil {
			return true, publicError(err)
		}
		return true, taskSummary(t) + " 此确认仅限本任务；整机接管请用 /computer takeover。"
	case "resume":
		if len(parts) != 4 {
			return true, commandUsage
		}
		t, err := c.resume(a, parts[2], parts[3], false)
		if err != nil {
			return true, publicError(err)
		}
		return true, taskSummary(t)
	case "reconcile":
		if len(parts) < 3 {
			return true, commandUsage
		}
		t, err := c.reconcile(ctx, parts[2])
		if err != nil {
			return true, publicError(err)
		}
		return true, taskSummary(t)
	case "takeover":
		_, err := c.desktopControl(ctx, a, true)
		if err != nil {
			return true, publicError(err)
		}
		return true, "整机 GUI 输入已停止并锁定，可人工接管；终端进程需另行核对。恢复请用 /computer desktop-resume。"
	case "desktop-resume":
		_, err := c.desktopControl(ctx, a, false)
		if err != nil {
			return true, publicError(err)
		}
		return true, "已解除人工桌面锁；任务仍需逐个明确继续，不会自动恢复。"
	case "status":
		c.mu.Lock()
		defer c.mu.Unlock()
		var lines []string
		for _, t := range c.tasks {
			if t.ProjectID != "control" {
				lines = append(lines, taskSummary(*t))
				if len(lines) >= 20 {
					break
				}
			}
		}
		if len(lines) == 0 {
			return true, "暂无 computer use 工作任务。"
		}
		return true, strings.Join(lines, "\n")
	default:
		return true, commandUsage
	}
}

const commandUsage = "/computer start <项目ID> <独立任务指令>\n/computer status\n/computer pause <任务ID>\n/computer reconcile <任务ID>\n/computer resume <任务ID> <新的指令>\n/computer takeover\n/computer desktop-resume\n结果未知的任务需先在真实工作台人工核对并确认。"
