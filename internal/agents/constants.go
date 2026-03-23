package agents

// Agent IDs for all supported agents.
const (
	AgentIDKimi     = "kimi"
	AgentIDClaude   = "claude"
	AgentIDCodex    = "codex"
	AgentIDCursor   = "cursor"
	AgentIDOpencode = "opencode"
	AgentIDQwen     = "qwen"
	AgentIDGemini   = "gemini"
	AgentIDBlackbox = "blackbox"
)

// AllAgentIDs returns all supported agent IDs.
func AllAgentIDs() []string {
	return []string{
		AgentIDCodex,
		AgentIDClaude,
		AgentIDCursor,
		AgentIDGemini,
		AgentIDKimi,
		AgentIDQwen,
		AgentIDOpencode,
		AgentIDBlackbox,
	}
}
