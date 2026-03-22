package agentutil

import (
	"strings"
	"sync"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
)

// Config captures the common per-thread provider configuration shared by
// built-in agents.
type Config struct {
	Dir             string
	ModelID         string
	SessionID       string
	ConfigOverrides map[string]string
}

// State stores the common mutable provider state shared by built-in agents.
type State struct {
	dir string

	mu              sync.RWMutex
	modelID         string
	sessionID       string
	configOverrides map[string]string
}

// NewState validates and normalizes common provider state.
func NewState(provider string, cfg Config) (*State, error) {
	dir, err := RequireDir(provider, cfg.Dir)
	if err != nil {
		return nil, err
	}
	return &State{
		dir:             dir,
		modelID:         strings.TrimSpace(cfg.ModelID),
		sessionID:       strings.TrimSpace(cfg.SessionID),
		configOverrides: normalizeConfigOverrides(cfg.ConfigOverrides),
	}, nil
}

// Dir returns the normalized working directory.
func (s *State) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// CurrentModelID returns the current selected model ID.
func (s *State) CurrentModelID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.modelID)
}

// SetModelID updates the selected model ID.
func (s *State) SetModelID(modelID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.modelID = strings.TrimSpace(modelID)
	s.mu.Unlock()
}

// CurrentSessionID returns the current bound ACP session ID.
func (s *State) CurrentSessionID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.sessionID)
}

// SetSessionID updates the bound ACP session ID.
func (s *State) SetSessionID(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sessionID = strings.TrimSpace(sessionID)
	s.mu.Unlock()
}

// CurrentConfigOverrides returns a cloned snapshot of non-model config values.
func (s *State) CurrentConfigOverrides() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfigOverrides(s.configOverrides)
}

// SetConfigOverride updates one non-model config override.
func (s *State) SetConfigOverride(configID, value string) {
	if s == nil {
		return
	}
	configID = strings.TrimSpace(configID)
	if configID == "" || strings.EqualFold(configID, "model") {
		return
	}
	value = strings.TrimSpace(value)

	s.mu.Lock()
	defer s.mu.Unlock()
	if value == "" {
		delete(s.configOverrides, configID)
		return
	}
	if s.configOverrides == nil {
		s.configOverrides = make(map[string]string)
	}
	s.configOverrides[configID] = value
}

// ApplyConfigOptionResult updates common state from ACP config option results.
func (s *State) ApplyConfigOptionResult(configID, requestedValue string, options []agents.ConfigOption) {
	if s == nil {
		return
	}
	configID = strings.TrimSpace(configID)
	requestedValue = strings.TrimSpace(requestedValue)
	if configID == "" {
		return
	}

	if strings.EqualFold(configID, "model") {
		current := acpmodel.CurrentValueForConfig(options, "model")
		if current == "" {
			current = requestedValue
		}
		s.SetModelID(current)
		return
	}

	current := acpmodel.CurrentValueForConfig(options, configID)
	if current == "" {
		current = requestedValue
	}
	s.SetConfigOverride(configID, current)
}

// ApplyConfigOptionsSnapshot updates common state from one full ACP config snapshot.
func (s *State) ApplyConfigOptionsSnapshot(options []agents.ConfigOption) {
	if s == nil || len(options) == 0 {
		return
	}

	modelID := ""
	overrides := make(map[string]string)
	for _, option := range options {
		configID := strings.TrimSpace(option.ID)
		if configID == "" {
			continue
		}
		if strings.EqualFold(configID, "model") || strings.EqualFold(strings.TrimSpace(option.Category), "model") {
			if current := strings.TrimSpace(option.CurrentValue); current != "" {
				modelID = current
			}
			continue
		}

		if current := strings.TrimSpace(option.CurrentValue); current != "" {
			overrides[configID] = current
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if modelID != "" {
		s.modelID = modelID
	}
	if len(overrides) == 0 {
		s.configOverrides = nil
		return
	}
	s.configOverrides = overrides
}

func normalizeConfigOverrides(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(input))
	for rawID, rawValue := range input {
		configID := strings.TrimSpace(rawID)
		value := strings.TrimSpace(rawValue)
		if configID == "" || value == "" || strings.EqualFold(configID, "model") {
			continue
		}
		normalized[configID] = value
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func cloneConfigOverrides(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for configID, value := range input {
		cloned[configID] = value
	}
	return cloned
}
