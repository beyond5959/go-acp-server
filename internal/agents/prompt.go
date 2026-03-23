package agents

import (
	"fmt"
	"strings"
)

const (
	// PromptContentTypeText is one ACP text prompt content block.
	PromptContentTypeText = "text"
	// PromptContentTypeResourceLink is one ACP resource_link prompt content block.
	PromptContentTypeResourceLink = "resource_link"
)

// PromptContent is one normalized ACP prompt content block.
type PromptContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	URI      string `json:"uri,omitempty"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

// Prompt is one normalized user prompt payload sent to the agent.
type Prompt struct {
	Content []PromptContent `json:"content,omitempty"`
}

// TextPrompt returns one normalized text-only prompt.
func TextPrompt(text string) Prompt {
	text = strings.TrimSpace(text)
	if text == "" {
		return Prompt{}
	}
	return Prompt{
		Content: []PromptContent{{
			Type: PromptContentTypeText,
			Text: text,
		}},
	}
}

// NormalizePrompt removes invalid prompt blocks and normalizes metadata.
func NormalizePrompt(prompt Prompt) Prompt {
	if len(prompt.Content) == 0 {
		return Prompt{}
	}

	normalized := make([]PromptContent, 0, len(prompt.Content))
	for _, item := range prompt.Content {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case PromptContentTypeText:
			text := strings.TrimSpace(item.Text)
			if text == "" {
				continue
			}
			normalized = append(normalized, PromptContent{
				Type: PromptContentTypeText,
				Text: text,
			})
		case PromptContentTypeResourceLink:
			uri := strings.TrimSpace(item.URI)
			name := strings.TrimSpace(item.Name)
			if uri == "" {
				continue
			}
			normalized = append(normalized, PromptContent{
				Type:     PromptContentTypeResourceLink,
				URI:      uri,
				Name:     name,
				MimeType: strings.TrimSpace(item.MimeType),
				Size:     maxPromptSize(item.Size),
			})
		}
	}

	if len(normalized) == 0 {
		return Prompt{}
	}
	return Prompt{Content: normalized}
}

// Clone returns a deep copy of the prompt.
func (p Prompt) Clone() Prompt {
	normalized := NormalizePrompt(p)
	if len(normalized.Content) == 0 {
		return Prompt{}
	}
	cloned := make([]PromptContent, len(normalized.Content))
	copy(cloned, normalized.Content)
	return Prompt{Content: cloned}
}

// Text returns the concatenated text blocks in display order.
func (p Prompt) Text() string {
	normalized := NormalizePrompt(p)
	if len(normalized.Content) == 0 {
		return ""
	}

	parts := make([]string, 0, len(normalized.Content))
	for _, item := range normalized.Content {
		if item.Type != PromptContentTypeText {
			continue
		}
		if item.Text == "" {
			continue
		}
		parts = append(parts, item.Text)
	}
	return strings.Join(parts, "\n\n")
}

// HasResourceLinks reports whether the prompt contains at least one ACP resource_link block.
func (p Prompt) HasResourceLinks() bool {
	for _, item := range NormalizePrompt(p).Content {
		if item.Type == PromptContentTypeResourceLink {
			return true
		}
	}
	return false
}

// ACPContent returns JSON-serializable ACP prompt content blocks.
func (p Prompt) ACPContent() []map[string]any {
	normalized := NormalizePrompt(p)
	if len(normalized.Content) == 0 {
		return nil
	}

	items := make([]map[string]any, 0, len(normalized.Content))
	for _, item := range normalized.Content {
		switch item.Type {
		case PromptContentTypeText:
			items = append(items, map[string]any{
				"type": PromptContentTypeText,
				"text": item.Text,
			})
		case PromptContentTypeResourceLink:
			payload := map[string]any{
				"type": PromptContentTypeResourceLink,
				"uri":  item.URI,
			}
			if item.Name != "" {
				payload["name"] = item.Name
			}
			if item.MimeType != "" {
				payload["mimeType"] = item.MimeType
			}
			if item.Size > 0 {
				payload["size"] = item.Size
			}
			items = append(items, payload)
		}
	}
	return items
}

// LegacyText returns a plain-text fallback representation for non-ACP-aware paths.
func (p Prompt) LegacyText() string {
	normalized := NormalizePrompt(p)
	if len(normalized.Content) == 0 {
		return ""
	}

	text := normalized.Text()
	resourceLines := make([]string, 0, len(normalized.Content))
	for _, item := range normalized.Content {
		if item.Type != PromptContentTypeResourceLink {
			continue
		}

		label := item.Name
		if label == "" {
			label = item.URI
		}
		meta := make([]string, 0, 3)
		if item.MimeType != "" {
			meta = append(meta, item.MimeType)
		}
		if item.Size > 0 {
			meta = append(meta, fmt.Sprintf("%d bytes", item.Size))
		}
		if item.URI != "" {
			meta = append(meta, item.URI)
		}
		if len(meta) == 0 {
			resourceLines = append(resourceLines, "- "+label)
			continue
		}
		resourceLines = append(resourceLines, fmt.Sprintf("- %s (%s)", label, strings.Join(meta, ", ")))
	}

	if len(resourceLines) == 0 {
		return text
	}

	var builder strings.Builder
	if text != "" {
		builder.WriteString(text)
		builder.WriteString("\n\n")
	}
	builder.WriteString("[Attached Resources]\n")
	builder.WriteString(strings.Join(resourceLines, "\n"))
	return strings.TrimSpace(builder.String())
}

// EventPayload returns one JSON-serializable prompt event payload for storage/history.
func (p Prompt) EventPayload(turnID string) map[string]any {
	payload := map[string]any{
		"turnId": strings.TrimSpace(turnID),
	}
	if prompt := p.ACPContent(); len(prompt) > 0 {
		payload["prompt"] = prompt
	}
	return payload
}

func maxPromptSize(size int64) int64 {
	if size < 0 {
		return 0
	}
	return size
}
