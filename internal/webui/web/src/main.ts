import './style.css'
import { store } from './store.ts'
import { api } from './api.ts'
import { applyTheme, settingsPanel } from './components/settings-panel.ts'
import { newThreadModal } from './components/new-thread-modal.ts'
import { mountPermissionCard, PERMISSION_TIMEOUT_MS } from './components/permission-card.ts'
import { renderMarkdown, bindMarkdownControls } from './markdown.ts'
import type {
  Thread,
  Message,
  MessageSegment,
  ConfigOption,
  ConfigOptionValue,
  SlashCommand,
  Turn,
  StreamState,
  TurnEvent,
  PlanEntry,
  SessionInfo,
  SessionTranscriptMessage,
  ToolCall,
} from './types.ts'
import type {
  TurnStream,
  PermissionRequiredPayload,
  PlanUpdatePayload,
  ReasoningDeltaPayload,
  SessionBoundPayload,
  ToolCallPayload,
} from './sse.ts'
import { copyText, escHtml, formatRelativeTime, formatTimestamp, generateUUID } from './utils.ts'

// ── Theme ─────────────────────────────────────────────────────────────────

applyTheme(store.get().theme)
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
  if (store.get().theme === 'system') applyTheme('system')
})

// ── Icons ─────────────────────────────────────────────────────────────────

const iconPlus = `<svg width="15" height="15" viewBox="0 0 15 15" fill="none" aria-hidden="true">
  <path d="M7.5 2v11M2 7.5h11" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>
</svg>`

const iconSend = `<svg width="14" height="14" viewBox="0 0 15 15" fill="none" aria-hidden="true">
  <path d="M1.5 7.5h12M8.5 2l5 5.5-5 5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

const iconSettings = `<svg width="14" height="14" viewBox="0 0 15 15" fill="none" aria-hidden="true">
  <circle cx="7.5" cy="7.5" r="2" stroke="currentColor" stroke-width="1.5"/>
  <path d="M7.5 1v1.5M7.5 12.5V14M1 7.5h1.5M12.5 7.5H14M3.05 3.05l1.06 1.06M10.9 10.9l1.05 1.05M3.05 11.95l1.06-1.06M10.9 4.1l1.05-1.05"
    stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
</svg>`

const iconMenu = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <path d="M2 4h12M2 8h12M2 12h12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
</svg>`

const iconCheck = `<svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <path d="M3.5 8.5l3 3 6-7" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

const iconCopy = `<svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <rect x="6" y="3" width="7" height="10" rx="1.5" stroke="currentColor" stroke-width="1.4"/>
  <path d="M4.5 11H4A1.5 1.5 0 0 1 2.5 9.5V4A1.5 1.5 0 0 1 4 2.5h5.5" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>
</svg>`

const iconInfo = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <circle cx="8" cy="8" r="6.25" stroke="currentColor" stroke-width="1.5"/>
  <path d="M8 7v3.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
  <circle cx="8" cy="4.5" r="0.8" fill="currentColor"/>
</svg>`

const iconSlashCommand = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
  <path d="m7 11 2-2-2-2" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/>
  <path d="M11 13h4" stroke="currentColor" stroke-width="1.9" stroke-linecap="round"/>
  <rect x="3.5" y="3.5" width="17" height="17" rx="2.5" stroke="currentColor" stroke-width="1.7"/>
</svg>`

const iconRefresh = `<svg width="15" height="15" viewBox="0 0 15 15" fill="none" aria-hidden="true">
  <path d="M12.5 7.5a5 5 0 1 1-1.47-3.53" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
  <path d="M12.5 2.5v3h-3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

const iconSparkles = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
  <path d="M11.017 2.814a1 1 0 0 1 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a1 1 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 0-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051-5.558a2 2 0 0 0-1.594-1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 0 1.594-1.594z" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
  <path d="M20 2v4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>
  <path d="M22 4h-4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>
  <circle cx="4" cy="20" r="1.6" fill="currentColor"/>
</svg>`

const iconTool = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true">
  <path d="M14.7 6.3a4 4 0 0 0 3 5.96l-7.03 7.04a1.5 1.5 0 0 1-2.12 0l-1.85-1.85a1.5 1.5 0 0 1 0-2.12l7.04-7.03a4 4 0 0 0 5.96 3" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
  <path d="M18 6a2 2 0 0 0-2-2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>
</svg>`

const iconChevronRight = `<svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true">
  <path d="m9 18 6-6-6-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

const codexIconURL = '/codex-icon.png'
const geminiIconURL = '/gemini-icon.png'
const claudeIconURL = '/claude-icon.png'
const kimiIconURL = '/kimi-icon.png'
const opencodeIconURL = '/opencode-icon.png'
const qwenIconURL = '/qwen-icon.png'

const defaultConfigCatalogCacheKey = '__default__'
const threadConfigCache = new Map<string, ConfigOption[]>()
const agentConfigCatalogCache = new Map<string, ConfigOption[]>()
const agentConfigCatalogInFlight = new Map<string, Promise<ConfigOption[]>>()
const agentSlashCommandsCache = new Map<string, SlashCommand[]>()
const agentSlashCommandsInFlight = new Map<string, Promise<SlashCommand[]>>()
const threadConfigSwitching = new Set<string>()
const sessionSwitchingThreads = new Set<string>()
const freshSessionNonceByThread = new Map<string, string>()
let slashCommandSelectedIndex = 0

interface SessionPanelState {
  supported: boolean | null
  sessions: SessionInfo[]
  nextCursor: string
  loading: boolean
  loadingMore: boolean
  error: string
}

const sessionPanelStateByThread = new Map<string, SessionPanelState>()
const sessionPanelRequestSeqByThread = new Map<string, number>()
const sessionPanelScrollTopByThread = new Map<string, number>()
let sessionPanelRequestSeq = 0

function cloneConfigOptions(options: ConfigOption[]): ConfigOption[] {
  return options.map(option => ({
    ...option,
    options: [...(option.options ?? [])],
  }))
}

function normalizeConfigOptions(options: ConfigOption[], includeCurrentValue = true): ConfigOption[] {
  const byId = new Set<string>()
  const normalized: ConfigOption[] = []

  for (const rawOption of options) {
    const id = rawOption.id?.trim() ?? ''
    if (!id || byId.has(id)) continue
    byId.add(id)

    const seenValue = new Set<string>()
    const values: ConfigOptionValue[] = []
    for (const rawValue of rawOption.options ?? []) {
      const value = rawValue.value?.trim() ?? ''
      if (!value || seenValue.has(value)) continue
      seenValue.add(value)
      values.push({
        value,
        name: (rawValue.name || value).trim() || value,
        description: rawValue.description?.trim() || undefined,
      })
    }

    const currentValue = rawOption.currentValue?.trim() ?? ''
    if (includeCurrentValue && currentValue && !seenValue.has(currentValue)) {
      values.unshift({ value: currentValue, name: currentValue })
      seenValue.add(currentValue)
    }

    normalized.push({
      id,
      category: rawOption.category?.trim() || undefined,
      name: rawOption.name?.trim() || id,
      description: rawOption.description?.trim() || undefined,
      type: rawOption.type?.trim() || undefined,
      currentValue,
      options: values,
    })
  }
  return normalized
}

function normalizeConfigCatalogOptions(options: ConfigOption[]): ConfigOption[] {
  return normalizeConfigOptions(options, false).map(option => ({
    ...option,
    currentValue: '',
    options: [...(option.options ?? [])],
  }))
}

function normalizeAgentConfigCatalogKey(agentId: string, modelId = ''): string {
  const normalizedAgentID = agentId.trim().toLowerCase()
  if (!normalizedAgentID) return ''
  const normalizedModelID = modelId.trim() || defaultConfigCatalogCacheKey
  return `${normalizedAgentID}::${normalizedModelID}`
}

function clonePlanEntries(entries: PlanEntry[] | null | undefined): PlanEntry[] | undefined {
  if (!entries?.length) return undefined

  const cloned: PlanEntry[] = []
  for (const entry of entries) {
    const content = entry.content?.trim() ?? ''
    if (!content) continue
    cloned.push({
      content,
      status: entry.status?.trim() || undefined,
      priority: entry.priority?.trim() || undefined,
    })
  }
  return cloned.length ? cloned : undefined
}

function cloneJSONValue<T>(value: T): T {
  if (value === undefined) return value
  return JSON.parse(JSON.stringify(value)) as T
}

let toolCallPreId = 0

function nextToolCallPreID(): string {
  toolCallPreId += 1
  return `tool-call-pre-${toolCallPreId}`
}

function cloneToolCalls(toolCalls: ToolCall[] | null | undefined): ToolCall[] | undefined {
  if (!toolCalls?.length) return undefined

  const cloned: ToolCall[] = []
  const seen = new Set<string>()
  for (const rawToolCall of toolCalls) {
    const toolCallId = rawToolCall.toolCallId?.trim() ?? ''
    if (!toolCallId || seen.has(toolCallId)) continue
    seen.add(toolCallId)
    cloned.push({
      toolCallId,
      title: rawToolCall.title?.trim() || undefined,
      kind: rawToolCall.kind?.trim() || undefined,
      status: rawToolCall.status?.trim() || undefined,
      content: Array.isArray(rawToolCall.content) ? cloneJSONValue(rawToolCall.content) : undefined,
      locations: Array.isArray(rawToolCall.locations) ? cloneJSONValue(rawToolCall.locations) : undefined,
      rawInput: rawToolCall.rawInput === undefined ? undefined : cloneJSONValue(rawToolCall.rawInput),
      rawOutput: rawToolCall.rawOutput === undefined ? undefined : cloneJSONValue(rawToolCall.rawOutput),
    })
  }
  return cloned.length ? cloned : undefined
}

function nextMessageSegmentID(prefix: string, kind: MessageSegment['kind'], index: number): string {
  return `${prefix}-${kind}-${index}`
}

function cloneMessageSegments(segments: MessageSegment[] | null | undefined): MessageSegment[] | undefined {
  if (!segments?.length) return undefined

  const cloned: MessageSegment[] = []
  for (const segment of segments) {
    const id = segment.id?.trim() ?? ''
    if (!id) continue
    if (segment.kind === 'tool_call') {
      const toolCall = cloneToolCalls(segment.toolCall ? [segment.toolCall] : undefined)?.[0]
      if (!toolCall) continue
      cloned.push({
        id,
        kind: 'tool_call',
        toolCall,
      })
      continue
    }

    cloned.push({
      id,
      kind: segment.kind,
      content: segment.content ?? '',
    })
  }
  return cloned.length ? cloned : undefined
}

function appendTextSegment(
  segments: MessageSegment[] | null | undefined,
  kind: 'content' | 'reasoning',
  delta: string,
  idPrefix: string,
): MessageSegment[] {
  if (!delta) return cloneMessageSegments(segments) ?? []

  const next = cloneMessageSegments(segments) ?? []
  const last = next[next.length - 1]
  if (last?.kind === kind) {
    next[next.length - 1] = {
      ...last,
      content: (last.content ?? '') + delta,
    }
    return next
  }

  next.push({
    id: nextMessageSegmentID(idPrefix, kind, next.length + 1),
    kind,
    content: delta,
  })
  return next
}

function applyToolCallSegmentEvent(
  segments: MessageSegment[] | null | undefined,
  payload: Record<string, unknown>,
  idPrefix: string,
): MessageSegment[] {
  const toolCallId = typeof payload.toolCallId === 'string' ? payload.toolCallId.trim() : ''
  if (!toolCallId) return cloneMessageSegments(segments) ?? []

  const next = cloneMessageSegments(segments) ?? []
  const existingIndex = next.findIndex(segment => (
    segment.kind === 'tool_call' && segment.toolCall?.toolCallId === toolCallId
  ))
  const current = existingIndex >= 0 && next[existingIndex].toolCall
    ? [next[existingIndex].toolCall]
    : []
  const merged = applyToolCallEvent(current, payload)[0]
  if (!merged) return next

  if (existingIndex >= 0) {
    next[existingIndex] = {
      ...next[existingIndex],
      toolCall: merged,
    }
  } else {
    next.push({
      id: nextMessageSegmentID(idPrefix, 'tool_call', next.length + 1),
      kind: 'tool_call',
      toolCall: merged,
    })
  }

  return next
}

function findToolCallSegmentID(
  segments: MessageSegment[] | null | undefined,
  toolCallId: string | undefined,
): string | null {
  const normalizedToolCallID = toolCallId?.trim() ?? ''
  if (!normalizedToolCallID) return null
  return (segments ?? []).find(segment => (
    segment.kind === 'tool_call' && segment.toolCall?.toolCallId === normalizedToolCallID
  ))?.id ?? null
}

function messageSegmentsContent(segments: MessageSegment[] | null | undefined): string {
  return (cloneMessageSegments(segments) ?? [])
    .filter(segment => segment.kind === 'content')
    .map(segment => segment.content ?? '')
    .join('')
}

function messageSegmentsReasoning(segments: MessageSegment[] | null | undefined): string {
  return (cloneMessageSegments(segments) ?? [])
    .filter(segment => segment.kind === 'reasoning')
    .map(segment => segment.content ?? '')
    .join('')
}

function messageSegmentsToolCalls(segments: MessageSegment[] | null | undefined): ToolCall[] | undefined {
  const toolCalls = (cloneMessageSegments(segments) ?? [])
    .filter(segment => segment.kind === 'tool_call')
    .map(segment => segment.toolCall)
    .filter((toolCall): toolCall is ToolCall => !!toolCall)
  return cloneToolCalls(toolCalls)
}

function messageHasContentSegment(segments: MessageSegment[] | null | undefined): boolean {
  return (segments ?? []).some(segment => segment.kind === 'content' && !!segment.content)
}

function applyToolCallEvent(toolCalls: ToolCall[], payload: Record<string, unknown>): ToolCall[] {
  const toolCallId = typeof payload.toolCallId === 'string' ? payload.toolCallId.trim() : ''
  if (!toolCallId) return cloneToolCalls(toolCalls) ?? []

  const next = cloneToolCalls(toolCalls) ?? []
  const existingIndex = next.findIndex(toolCall => toolCall.toolCallId === toolCallId)
  const current: ToolCall = existingIndex >= 0 ? next[existingIndex] : { toolCallId }
  const merged: ToolCall = { ...current, toolCallId }

  if (Object.prototype.hasOwnProperty.call(payload, 'title')) {
    merged.title = typeof payload.title === 'string' && payload.title.trim()
      ? payload.title.trim()
      : undefined
  }
  if (Object.prototype.hasOwnProperty.call(payload, 'kind')) {
    merged.kind = typeof payload.kind === 'string' && payload.kind.trim()
      ? payload.kind.trim()
      : undefined
  }
  if (Object.prototype.hasOwnProperty.call(payload, 'status')) {
    merged.status = typeof payload.status === 'string' && payload.status.trim()
      ? payload.status.trim()
      : undefined
  }
  if (Object.prototype.hasOwnProperty.call(payload, 'content')) {
    merged.content = Array.isArray(payload.content) ? cloneJSONValue(payload.content) : undefined
  }
  if (Object.prototype.hasOwnProperty.call(payload, 'locations')) {
    merged.locations = Array.isArray(payload.locations) ? cloneJSONValue(payload.locations) : undefined
  }
  if (Object.prototype.hasOwnProperty.call(payload, 'rawInput')) {
    merged.rawInput = payload.rawInput === undefined || payload.rawInput === null
      ? undefined
      : cloneJSONValue(payload.rawInput)
  }
  if (Object.prototype.hasOwnProperty.call(payload, 'rawOutput')) {
    merged.rawOutput = payload.rawOutput === undefined || payload.rawOutput === null
      ? undefined
      : cloneJSONValue(payload.rawOutput)
  }

  if (existingIndex >= 0) {
    next[existingIndex] = merged
  } else {
    next.push(merged)
  }
  return next
}

function hasReasoningText(value: string | null | undefined): value is string {
  return typeof value === 'string' && value.trim().length > 0
}

function normalizeAgentKey(agentId: string): string {
  return agentId.trim().toLowerCase()
}

function cloneSlashCommands(commands: SlashCommand[] | null | undefined): SlashCommand[] {
  if (!commands?.length) return []

  const cloned: SlashCommand[] = []
  const seen = new Set<string>()
  for (const command of commands) {
    const name = command.name?.trim() ?? ''
    if (!name || seen.has(name)) continue
    seen.add(name)
    cloned.push({
      name,
      description: command.description?.trim() || undefined,
      inputHint: command.inputHint?.trim() || undefined,
    })
  }
  return cloned
}

function cacheAgentSlashCommands(agentId: string, commands: SlashCommand[]): SlashCommand[] {
  const key = normalizeAgentKey(agentId)
  const normalized = cloneSlashCommands(commands)
  if (key) {
    agentSlashCommandsCache.set(key, normalized)
  }
  return normalized
}

function hasAgentSlashCommandsCache(agentId: string): boolean {
  const key = normalizeAgentKey(agentId)
  return !!key && agentSlashCommandsCache.has(key)
}

function getAgentSlashCommands(agentId: string): SlashCommand[] {
  const key = normalizeAgentKey(agentId)
  if (!key) return []
  return cloneSlashCommands(agentSlashCommandsCache.get(key))
}

function parsePlanEntries(value: unknown): PlanEntry[] | undefined {
  if (!Array.isArray(value)) return undefined
  const parsed: PlanEntry[] = []
  for (const item of value) {
    if (!item || typeof item !== 'object') continue
    const entry = item as Record<string, unknown>
    const content = typeof entry.content === 'string' ? entry.content : ''
    if (!content.trim()) continue
    parsed.push({
      content,
      status: typeof entry.status === 'string' ? entry.status : undefined,
      priority: typeof entry.priority === 'string' ? entry.priority : undefined,
    })
  }
  return clonePlanEntries(parsed)
}

function extractTurnPlanEntries(events: TurnEvent[] | undefined): PlanEntry[] | undefined {
  let latest: PlanEntry[] | undefined
  for (const event of events ?? []) {
    if (event.type !== 'plan_update') continue
    latest = parsePlanEntries(event.data.entries)
  }
  return clonePlanEntries(latest)
}

function extractTurnToolCalls(events: TurnEvent[] | undefined): ToolCall[] | undefined {
  let toolCalls: ToolCall[] = []
  for (const event of events ?? []) {
    if (event.type !== 'tool_call' && event.type !== 'tool_call_update') continue
    toolCalls = applyToolCallEvent(toolCalls, event.data)
  }
  return cloneToolCalls(toolCalls)
}

function extractTurnReasoning(events: TurnEvent[] | undefined): string {
  let reasoning = ''
  for (const event of events ?? []) {
    if (event.type !== 'reasoning_delta' && event.type !== 'thought_delta') continue
    if (typeof event.data.delta !== 'string') continue
    reasoning += event.data.delta
  }
  return reasoning
}

function sortTurnEvents(events: TurnEvent[] | undefined): TurnEvent[] {
  return [...(events ?? [])].sort((left, right) => {
    if (left.seq !== right.seq) return left.seq - right.seq
    return left.eventId - right.eventId
  })
}

function buildMessageSegmentsFromTurn(
  turnId: string,
  events: TurnEvent[] | undefined,
  responseText: string,
): MessageSegment[] | undefined {
  let segments: MessageSegment[] = []
  const idPrefix = `${turnId}-segment`

  for (const event of sortTurnEvents(events)) {
    switch (event.type) {
      case 'message_delta': {
        if (typeof event.data.delta !== 'string' || !event.data.delta) continue
        segments = appendTextSegment(segments, 'content', event.data.delta, idPrefix)
        break
      }
      case 'reasoning_delta':
      case 'thought_delta': {
        if (typeof event.data.delta !== 'string' || !event.data.delta) continue
        segments = appendTextSegment(segments, 'reasoning', event.data.delta, idPrefix)
        break
      }
      case 'tool_call':
      case 'tool_call_update':
        segments = applyToolCallSegmentEvent(segments, event.data, idPrefix)
        break
      default:
        break
    }
  }

  if (!messageHasContentSegment(segments) && responseText) {
    segments = appendTextSegment(segments, 'content', responseText, idPrefix)
  }

  return cloneMessageSegments(segments)
}

function fallbackMessageSegments(msg: Message): MessageSegment[] | undefined {
  const segments: MessageSegment[] = []

  if (hasReasoningText(msg.reasoning)) {
    segments.push({
      id: nextMessageSegmentID(msg.id, 'reasoning', segments.length + 1),
      kind: 'reasoning',
      content: msg.reasoning,
    })
  }

  for (const toolCall of cloneToolCalls(msg.toolCalls) ?? []) {
    segments.push({
      id: nextMessageSegmentID(msg.id, 'tool_call', segments.length + 1),
      kind: 'tool_call',
      toolCall,
    })
  }

  if (msg.content) {
    segments.push({
      id: nextMessageSegmentID(msg.id, 'content', segments.length + 1),
      kind: 'content',
      content: msg.content,
    })
  }

  return cloneMessageSegments(segments)
}

function resolveMessageSegments(msg: Message): MessageSegment[] | undefined {
  return cloneMessageSegments(msg.segments) ?? fallbackMessageSegments(msg)
}

function hasAgentConfigCatalog(agentId: string, modelId = ''): boolean {
  const key = normalizeAgentConfigCatalogKey(agentId, modelId)
  return !!key && agentConfigCatalogCache.has(key)
}

function getAgentConfigCatalog(agentId: string, modelId = ''): ConfigOption[] {
  const key = normalizeAgentConfigCatalogKey(agentId, modelId)
  if (!key) return []
  return agentConfigCatalogCache.get(key) ?? []
}

function cacheAgentConfigCatalog(agentId: string, modelId: string, options: ConfigOption[]): ConfigOption[] {
  const cacheKey = normalizeAgentConfigCatalogKey(agentId, modelId)
  const normalized = normalizeConfigCatalogOptions(options)
  if (cacheKey) {
    agentConfigCatalogCache.set(cacheKey, normalized)
  }
  return normalized
}

function cacheThreadConfigOptions(thread: Thread, options: ConfigOption[], selectedModelID?: string): ConfigOption[] {
  const normalized = normalizeConfigOptions(options)
  threadConfigCache.set(thread.threadId, normalized)
  cacheAgentConfigCatalog(thread.agent ?? '', selectedModelID ?? fallbackThreadModelID(thread), normalized)
  return normalized
}

function findModelOption(options: ConfigOption[]): ConfigOption | null {
  for (const option of options) {
    const category = option.category?.trim().toLowerCase() ?? ''
    const id = option.id.trim().toLowerCase()
    if (category === 'model' || id === 'model') {
      return option
    }
  }
  return null
}

function fallbackThreadModelID(thread: Thread): string {
  const model = thread.agentOptions?.modelId
  return typeof model === 'string' ? model.trim() : ''
}

function threadSessionID(thread: Thread | null | undefined): string {
  const value = thread?.agentOptions?.sessionId
  return typeof value === 'string' ? value.trim() : ''
}

function threadSessionScopeKey(threadId: string, sessionID = ''): string {
  return `${threadId}::${sessionID.trim()}`
}

function threadFreshSessionScopeKey(threadId: string): string {
  const nonce = freshSessionNonceByThread.get(threadId)?.trim() ?? ''
  if (!nonce) return ''
  return threadSessionScopeKey(threadId, `@fresh:${nonce}`)
}

function isFreshSessionScopeKey(scopeKey: string): boolean {
  const parts = scopeKey.split('::', 2)
  return parts.length === 2 && parts[1].startsWith('@fresh:')
}

function threadChatScopeKey(thread: Thread | null | undefined): string {
  if (!thread) return ''
  const sessionID = threadSessionID(thread)
  if (sessionID) {
    return threadSessionScopeKey(thread.threadId, sessionID)
  }
  return threadFreshSessionScopeKey(thread.threadId) || threadSessionScopeKey(thread.threadId)
}

function buildThreadAgentOptionsWithSession(
  base: Record<string, unknown>,
  sessionID: string,
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...base }
  sessionID = sessionID.trim()
  if (sessionID) {
    next.sessionId = sessionID
  } else {
    delete next.sessionId
  }
  return next
}

function activateFreshSessionScope(
  threadId: string,
  messages: Record<string, Message[]>,
): Record<string, Message[]> {
  freshSessionNonceByThread.set(threadId, generateUUID())
  const scopeKey = threadFreshSessionScopeKey(threadId)
  loadedHistoryScopeKeys.add(scopeKey)
  if (Object.prototype.hasOwnProperty.call(messages, scopeKey)) {
    return messages
  }
  return {
    ...messages,
    [scopeKey]: [],
  }
}

async function loadThreadConfigOptions(threadId: string): Promise<ConfigOption[]> {
  const thread = store.get().threads.find(item => item.threadId === threadId)
  if (!thread) return []
  const selectedModelID = fallbackThreadModelID(thread)
  const catalogKey = normalizeAgentConfigCatalogKey(thread.agent ?? '', selectedModelID)

  if (threadConfigCache.has(thread.threadId) || hasAgentConfigCatalog(thread.agent ?? '', selectedModelID)) {
    return cloneConfigOptions(getThreadConfigOptionsForRender(thread))
  }

  const inFlight = catalogKey ? agentConfigCatalogInFlight.get(catalogKey) : undefined
  if (inFlight) {
    return inFlight.then(() => cloneConfigOptions(getThreadConfigOptionsForRender(thread)))
  }

  const task = api.getThreadConfigOptions(thread.threadId)
    .then(options => {
      cacheThreadConfigOptions(thread, options, selectedModelID)
      return cloneConfigOptions(getThreadConfigOptionsForRender(thread))
    })
    .finally(() => {
      if (catalogKey) agentConfigCatalogInFlight.delete(catalogKey)
    })

  if (catalogKey) agentConfigCatalogInFlight.set(catalogKey, task)
  return task
}

async function loadThreadSlashCommands(threadId: string, force = false): Promise<SlashCommand[]> {
  const thread = store.get().threads.find(item => item.threadId === threadId)
  if (!thread) return []

  const agentKey = normalizeAgentKey(thread.agent ?? '')
  if (!agentKey) return []
  if (!force && agentSlashCommandsCache.has(agentKey)) {
    return getAgentSlashCommands(thread.agent ?? '')
  }

  const inFlight = agentSlashCommandsInFlight.get(agentKey)
  if (inFlight) {
    return inFlight.then(commands => cloneSlashCommands(commands))
  }

  const task = api.getThreadSlashCommands(thread.threadId)
    .then(commands => cacheAgentSlashCommands(thread.agent ?? '', commands))
    .finally(() => {
      agentSlashCommandsInFlight.delete(agentKey)
    })

  agentSlashCommandsInFlight.set(agentKey, task)
  return task.then(commands => cloneSlashCommands(commands))
}

// ── Active stream state (DOM-managed, per chat scope) ──────────────────────

/**
 * Non-null while a streaming bubble is live in the DOM.
 * We use this to prevent updateMessageList() from wiping the in-progress bubble.
 */
let activeStreamMsgId: string | null = null
let activeStreamScopeKey = ''
const streamsByScope = new Map<string, TurnStream>()
const streamBufferByScope = new Map<string, string>()
const streamPlanByScope = new Map<string, PlanEntry[]>()
const streamSegmentsByScope = new Map<string, MessageSegment[]>()
const activeContentSegmentIdByScope = new Map<string, string>()
const activeReasoningSegmentIdByScope = new Map<string, string>()
const activeToolCallSegmentIdByScope = new Map<string, string>()
const streamStartedAtByScope = new Map<string, string>()
type PendingPermission = PermissionRequiredPayload & { deadlineMs: number }
const pendingPermissionsByScope = new Map<string, Map<string, PendingPermission>>()
let slashCommandLookupThreadId: string | null = null

/** Last threadId that triggered a full chat-area re-render. */
let lastRenderThreadId: string | null = null
/** Last (threadId, sessionId) scope rendered into the chat pane. */
let lastRenderChatScopeKey = ''
/** Chat scope keys whose filtered history was loaded. */
const loadedHistoryScopeKeys = new Set<string>()
/** Segment ids whose final Thinking panel is currently expanded in the UI. */
const expandedReasoningSegmentIds = new Set<string>()
/** Segment ids whose final tool-call panel is currently expanded in the UI. */
const expandedToolCallSegmentIds = new Set<string>()
let openThreadActionMenuId: string | null = null
let renamingThreadId: string | null = null
let renamingThreadDraft = ''
let sessionPanelExpanded = true

// ── Scroll helpers ────────────────────────────────────────────────────────

/** True when the list is within 100px of its bottom — safe to auto-scroll. */
function isNearBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < 100
}

// ── Message store helpers ─────────────────────────────────────────────────

function addMessageToStore(scopeKey: string, msg: Message): void {
  const { messages } = store.get()
  store.set({ messages: { ...messages, [scopeKey]: [...(messages[scopeKey] ?? []), msg] } })
}

function activeContentSegmentID(scopeKey: string): string | null {
  return activeContentSegmentIdByScope.get(scopeKey) ?? null
}

function setActiveContentSegmentID(scopeKey: string, segmentID: string | null | undefined): void {
  if (!scopeKey) return
  const nextID = segmentID?.trim() ?? ''
  if (nextID) {
    activeContentSegmentIdByScope.set(scopeKey, nextID)
    return
  }
  activeContentSegmentIdByScope.delete(scopeKey)
}

function activeReasoningSegmentID(scopeKey: string): string | null {
  return activeReasoningSegmentIdByScope.get(scopeKey) ?? null
}

function setActiveReasoningSegmentID(scopeKey: string, segmentID: string | null | undefined): void {
  if (!scopeKey) return
  const nextID = segmentID?.trim() ?? ''
  if (nextID) {
    activeReasoningSegmentIdByScope.set(scopeKey, nextID)
    return
  }
  activeReasoningSegmentIdByScope.delete(scopeKey)
}

function activeToolCallSegmentID(scopeKey: string): string | null {
  return activeToolCallSegmentIdByScope.get(scopeKey) ?? null
}

function setActiveToolCallSegmentID(scopeKey: string, segmentID: string | null | undefined): void {
  if (!scopeKey) return
  const nextID = segmentID?.trim() ?? ''
  if (nextID) {
    activeToolCallSegmentIdByScope.set(scopeKey, nextID)
    return
  }
  activeToolCallSegmentIdByScope.delete(scopeKey)
}

function omitThreadCompletionBadge(
  badges: Record<string, boolean>,
  threadId: string,
): Record<string, boolean> {
  if (!threadId || !badges[threadId]) return badges
  const next = { ...badges }
  delete next[threadId]
  return next
}

function markThreadCompletionBadge(threadId: string): void {
  if (!threadId) return
  const state = store.get()
  if (state.activeThreadId === threadId || state.threadCompletionBadges[threadId]) return
  store.set({
    threadCompletionBadges: {
      ...state.threadCompletionBadges,
      [threadId]: true,
    },
  })
}

function activateThread(threadId: string): void {
  if (!threadId) return
  const state = store.get()
  const clearedThreadActions = resetThreadActionMenuState()
  const nextThreadCompletionBadges = omitThreadCompletionBadge(state.threadCompletionBadges, threadId)
  if (threadId === state.activeThreadId) {
    if (nextThreadCompletionBadges !== state.threadCompletionBadges) {
      store.set({ threadCompletionBadges: nextThreadCompletionBadges })
    } else if (clearedThreadActions) {
      updateThreadList()
    }
    return
  }

  store.set({
    activeThreadId: threadId,
    threadCompletionBadges: nextThreadCompletionBadges,
  })
}

function resetThreadActionMenuState(): boolean {
  const changed = openThreadActionMenuId !== null || renamingThreadId !== null || renamingThreadDraft !== ''
  if (!changed) return false
  openThreadActionMenuId = null
  renamingThreadId = null
  renamingThreadDraft = ''
  return true
}

function cancelThreadRename(threadId: string): void {
  if (renamingThreadId !== threadId) return
  renamingThreadId = null
  renamingThreadDraft = ''
  updateThreadList()
}

function toggleThreadActionMenu(threadId: string): void {
  if (!threadId) return
  if (openThreadActionMenuId === threadId) {
    resetThreadActionMenuState()
    updateThreadList()
    return
  }

  openThreadActionMenuId = threadId
  renamingThreadId = null
  renamingThreadDraft = ''
  updateThreadList()
}

function beginRenameThread(threadId: string): void {
  const thread = store.get().threads.find(item => item.threadId === threadId)
  if (!thread) return

  openThreadActionMenuId = threadId
  renamingThreadId = threadId
  renamingThreadDraft = thread.title || threadTitle(thread)
  updateThreadList()
  requestAnimationFrame(() => {
    const input = document.querySelector<HTMLInputElement>('.thread-rename-input')
    input?.focus()
    input?.select()
  })
}

function getThreadMenuTrigger(threadId: string): HTMLButtonElement | null {
  return Array.from(document.querySelectorAll<HTMLButtonElement>('.thread-item-menu-trigger'))
    .find(btn => btn.dataset.threadId === threadId) ?? null
}

function renderThreadActionPopover(t: Thread): string {
  const isOpen = openThreadActionMenuId === t.threadId
  if (!isOpen) return ''

  if (renamingThreadId === t.threadId) {
    return `
      <div class="thread-action-popover thread-action-popover--rename" data-thread-id="${escHtml(t.threadId)}">
        <form class="thread-rename-form" data-thread-id="${escHtml(t.threadId)}">
          <input
            class="thread-rename-input"
            data-thread-id="${escHtml(t.threadId)}"
            type="text"
            value="${escHtml(renamingThreadDraft)}"
            placeholder="Agent name"
            maxlength="120"
            aria-label="Rename agent"
          />
          <div class="thread-rename-actions">
            <button class="btn btn-primary btn-sm" type="submit">Save</button>
            <button class="btn btn-ghost btn-sm thread-rename-cancel-btn" type="button" data-thread-id="${escHtml(t.threadId)}">
              Cancel
            </button>
          </div>
        </form>
      </div>`
  }

  return `
    <div class="thread-action-popover thread-action-menu" data-thread-id="${escHtml(t.threadId)}" role="menu" aria-label="Agent actions">
      <button class="thread-action-menu-item" type="button" data-thread-id="${escHtml(t.threadId)}" data-action="rename" role="menuitem">
        Rename
      </button>
      <button
        class="thread-action-menu-item thread-action-menu-item--danger"
        type="button"
        data-thread-id="${escHtml(t.threadId)}"
        data-action="delete"
        role="menuitem"
      >
        Delete
      </button>
    </div>`
}

function renderThreadActionLayer(): void {
  const layer = document.getElementById('thread-action-layer')
  if (!layer) return
  if (!openThreadActionMenuId) {
    layer.innerHTML = ''
    layer.hidden = true
    return
  }

  const thread = store.get().threads.find(item => item.threadId === openThreadActionMenuId)
  const trigger = getThreadMenuTrigger(openThreadActionMenuId)
  const sidebar = document.getElementById('sidebar')
  if (!thread || !trigger || !sidebar) {
    resetThreadActionMenuState()
    layer.innerHTML = ''
    layer.hidden = true
    return
  }

  layer.hidden = false
  layer.innerHTML = renderThreadActionPopover(thread)

  const popover = layer.querySelector<HTMLElement>('.thread-action-popover')
  if (!popover) return

  const margin = 8
  const offset = 8
  const triggerRect = trigger.getBoundingClientRect()
  const sidebarRect = sidebar.getBoundingClientRect()
  const popoverWidth = popover.offsetWidth
  const popoverHeight = popover.offsetHeight
  const maxLeft = Math.max(margin, sidebar.clientWidth - popoverWidth - margin)
  const maxTop = Math.max(margin, sidebar.clientHeight - popoverHeight - margin)

  let left = triggerRect.right - sidebarRect.left - popoverWidth
  left = Math.min(Math.max(left, margin), maxLeft)

  let top = triggerRect.bottom - sidebarRect.top + offset
  if (top > maxTop) {
    top = triggerRect.top - sidebarRect.top - popoverHeight - offset
  }
  top = Math.min(Math.max(top, margin), maxTop)

  popover.style.left = `${left}px`
  popover.style.top = `${top}px`

  popover.addEventListener('click', e => e.stopPropagation())
  popover.addEventListener('keydown', e => e.stopPropagation())

  layer.querySelectorAll<HTMLButtonElement>('.thread-action-menu-item').forEach(btn => {
    btn.addEventListener('click', e => {
      e.preventDefault()
      e.stopPropagation()
      const id = btn.dataset.threadId ?? ''
      if (!id) return
      if (btn.dataset.action === 'rename') {
        beginRenameThread(id)
        return
      }
      if (btn.dataset.action === 'delete') {
        void handleDeleteThread(id)
      }
    })
  })

  layer.querySelectorAll<HTMLFormElement>('.thread-rename-form').forEach(form => {
    form.addEventListener('submit', e => {
      e.preventDefault()
      e.stopPropagation()
      const threadId = form.dataset.threadId ?? ''
      const input = form.querySelector<HTMLInputElement>('.thread-rename-input')
      if (!threadId || !input) return

      const controls = Array.from(form.querySelectorAll<HTMLInputElement | HTMLButtonElement>('input, button'))
      controls.forEach(control => { control.disabled = true })
      void handleRenameThread(threadId, input.value).finally(() => {
        controls.forEach(control => {
          if (control.isConnected) control.disabled = false
        })
      })
    })
  })

  layer.querySelectorAll<HTMLInputElement>('.thread-rename-input').forEach(input => {
    input.addEventListener('input', () => {
      const threadId = input.dataset.threadId ?? ''
      if (!threadId || renamingThreadId !== threadId) return
      renamingThreadDraft = input.value
    })
    input.addEventListener('keydown', e => {
      e.stopPropagation()
      if (e.key === 'Escape') {
        e.preventDefault()
        const threadId = input.dataset.threadId ?? ''
        if (threadId) cancelThreadRename(threadId)
      }
    })
  })

  layer.querySelectorAll<HTMLButtonElement>('.thread-rename-cancel-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.preventDefault()
      e.stopPropagation()
      const id = btn.dataset.threadId ?? ''
      if (!id) return
      cancelThreadRename(id)
    })
  })
}

function activeChatScopeKey(): string {
  const { activeThreadId, threads } = store.get()
  if (!activeThreadId) return ''
  const thread = threads.find(item => item.threadId === activeThreadId)
  return threadChatScopeKey(thread)
}

function getScopeStreamState(scopeKey: string): StreamState | null {
  if (!scopeKey) return null
  return store.get().streamStates[scopeKey] ?? null
}

function getActiveChatStreamState(): StreamState | null {
  return getScopeStreamState(activeChatScopeKey())
}

function hasMountedActiveStream(scopeKey: string): boolean {
  return !!scopeKey && activeStreamMsgId !== null && activeStreamScopeKey === scopeKey
}

function hasThreadStream(threadId: string | null): boolean {
  if (!threadId) return false
  return Object.values(store.get().streamStates).some(streamState => streamState.threadId === threadId)
}

function setScopeStreamState(scopeKey: string, next: StreamState | null): void {
  const { streamStates } = store.get()
  const updated = { ...streamStates }
  if (next) {
    updated[scopeKey] = next
  } else {
    delete updated[scopeKey]
  }
  store.set({ streamStates: updated })
}

function appendOrRestoreStreamingBubble(thread: Thread): void {
  const scopeKey = threadChatScopeKey(thread)
  const streamState = getScopeStreamState(scopeKey)
  if (!streamState) return

  const listEl = document.getElementById('message-list')
  if (!listEl) return

  const bubbleID = `bubble-${streamState.messageId}`
  if (document.getElementById(bubbleID)) {
    activeStreamMsgId = streamState.messageId
    activeStreamScopeKey = scopeKey
    return
  }

  listEl.querySelector('.empty-state')?.remove()
  listEl.querySelector('.message-list-loading')?.remove()
  const startedAt = streamStartedAtByScope.get(scopeKey) ?? new Date().toISOString()
  const div = document.createElement('div')
  div.className = 'message message--agent'
  div.dataset.msgId = streamState.messageId
  const livePlanEntries = streamPlanByScope.get(scopeKey)
  const liveSegments = streamSegmentsByScope.get(scopeKey)
  div.innerHTML = `
    <div class="message-group">
      ${renderStreamingBubbleHTML(
        streamState.messageId,
        liveSegments,
        livePlanEntries,
        activeContentSegmentID(scopeKey),
        activeReasoningSegmentID(scopeKey),
        activeToolCallSegmentID(scopeKey),
      )}
      <div class="message-meta">
        <span class="message-time">${formatTimestamp(startedAt)}</span>
      </div>
    </div>`
  bindMarkdownControls(div)

  listEl.appendChild(div)
  activeStreamMsgId = streamState.messageId
  activeStreamScopeKey = scopeKey

  updateStreamingBubblePlan(streamState.messageId, livePlanEntries)
  updateStreamingBubbleSegments(
    streamState.messageId,
    liveSegments,
    activeContentSegmentID(scopeKey),
    activeReasoningSegmentID(scopeKey),
    activeToolCallSegmentID(scopeKey),
  )
  listEl.scrollTop = listEl.scrollHeight
}

function clearScopeStreamRuntime(scopeKey: string): void {
  streamsByScope.delete(scopeKey)
  streamBufferByScope.delete(scopeKey)
  streamPlanByScope.delete(scopeKey)
  streamSegmentsByScope.delete(scopeKey)
  activeContentSegmentIdByScope.delete(scopeKey)
  activeReasoningSegmentIdByScope.delete(scopeKey)
  activeToolCallSegmentIdByScope.delete(scopeKey)
  streamStartedAtByScope.delete(scopeKey)
  setScopeStreamState(scopeKey, null)
  if (activeChatScopeKey() === scopeKey) {
    activeStreamMsgId = null
    activeStreamScopeKey = ''
  }
}

function rebindScopeRuntime(oldScopeKey: string, nextScopeKey: string, nextSessionID: string): void {
  oldScopeKey = oldScopeKey.trim()
  nextScopeKey = nextScopeKey.trim()
  nextSessionID = nextSessionID.trim()
  if (!oldScopeKey || !nextScopeKey || oldScopeKey === nextScopeKey) return

  if (streamsByScope.has(oldScopeKey)) {
    const stream = streamsByScope.get(oldScopeKey)
    streamsByScope.delete(oldScopeKey)
    if (stream) streamsByScope.set(nextScopeKey, stream)
  }
  if (streamBufferByScope.has(oldScopeKey)) {
    const buffered = streamBufferByScope.get(oldScopeKey) ?? ''
    streamBufferByScope.delete(oldScopeKey)
    streamBufferByScope.set(nextScopeKey, buffered)
  }
  if (streamPlanByScope.has(oldScopeKey)) {
    const plans = streamPlanByScope.get(oldScopeKey) ?? []
    streamPlanByScope.delete(oldScopeKey)
    streamPlanByScope.set(nextScopeKey, plans)
  }
  if (streamSegmentsByScope.has(oldScopeKey)) {
    const segments = streamSegmentsByScope.get(oldScopeKey) ?? []
    streamSegmentsByScope.delete(oldScopeKey)
    streamSegmentsByScope.set(nextScopeKey, segments)
  }
  if (activeContentSegmentIdByScope.has(oldScopeKey)) {
    const segmentID = activeContentSegmentIdByScope.get(oldScopeKey) ?? ''
    activeContentSegmentIdByScope.delete(oldScopeKey)
    if (segmentID) activeContentSegmentIdByScope.set(nextScopeKey, segmentID)
  }
  if (activeReasoningSegmentIdByScope.has(oldScopeKey)) {
    const segmentID = activeReasoningSegmentIdByScope.get(oldScopeKey) ?? ''
    activeReasoningSegmentIdByScope.delete(oldScopeKey)
    if (segmentID) activeReasoningSegmentIdByScope.set(nextScopeKey, segmentID)
  }
  if (activeToolCallSegmentIdByScope.has(oldScopeKey)) {
    const segmentID = activeToolCallSegmentIdByScope.get(oldScopeKey) ?? ''
    activeToolCallSegmentIdByScope.delete(oldScopeKey)
    if (segmentID) activeToolCallSegmentIdByScope.set(nextScopeKey, segmentID)
  }
  if (streamStartedAtByScope.has(oldScopeKey)) {
    const startedAt = streamStartedAtByScope.get(oldScopeKey) ?? ''
    streamStartedAtByScope.delete(oldScopeKey)
    streamStartedAtByScope.set(nextScopeKey, startedAt)
  }
  if (pendingPermissionsByScope.has(oldScopeKey)) {
    const pending = pendingPermissionsByScope.get(oldScopeKey)
    pendingPermissionsByScope.delete(oldScopeKey)
    if (pending) pendingPermissionsByScope.set(nextScopeKey, pending)
  }
  if (loadedHistoryScopeKeys.has(oldScopeKey)) {
    loadedHistoryScopeKeys.delete(oldScopeKey)
    loadedHistoryScopeKeys.add(nextScopeKey)
  }
  if (activeStreamScopeKey === oldScopeKey) {
    activeStreamScopeKey = nextScopeKey
  }

  const state = store.get()
  const nextMessages = { ...state.messages }
  const oldMessages = nextMessages[oldScopeKey] ?? []
  if (oldMessages.length) {
    nextMessages[nextScopeKey] = nextMessages[nextScopeKey]?.length
      ? [...nextMessages[nextScopeKey], ...oldMessages]
      : oldMessages
  }
  delete nextMessages[oldScopeKey]

  const nextStreamStates = { ...state.streamStates }
  const streamState = nextStreamStates[oldScopeKey]
  if (streamState) {
    nextStreamStates[nextScopeKey] = { ...streamState, sessionId: nextSessionID }
    delete nextStreamStates[oldScopeKey]
  }

  store.set({
    messages: nextMessages,
    streamStates: nextStreamStates,
  })
}

function upsertPendingPermission(scopeKey: string, event: PermissionRequiredPayload): PendingPermission {
  let byID = pendingPermissionsByScope.get(scopeKey)
  if (!byID) {
    byID = new Map<string, PendingPermission>()
    pendingPermissionsByScope.set(scopeKey, byID)
  }
  const existing = byID.get(event.permissionId)
  if (existing) return existing

  const pending: PendingPermission = {
    ...event,
    deadlineMs: Date.now() + PERMISSION_TIMEOUT_MS,
  }
  byID.set(event.permissionId, pending)
  return pending
}

function removePendingPermission(scopeKey: string, permissionId: string): void {
  const byID = pendingPermissionsByScope.get(scopeKey)
  if (!byID) return
  byID.delete(permissionId)
  if (byID.size === 0) {
    pendingPermissionsByScope.delete(scopeKey)
  }
}

function clearPendingPermissions(scopeKey: string): void {
  pendingPermissionsByScope.delete(scopeKey)
}

function mountPendingPermissionCard(scopeKey: string, pending: PendingPermission): void {
  if (activeChatScopeKey() !== scopeKey) return
  if (document.getElementById(`perm-card-${pending.permissionId}`)) return

  const listEl = document.getElementById('message-list')
  if (!listEl) return

  mountPermissionCard(listEl, pending, {
    deadlineMs: pending.deadlineMs,
    onResolved: () => removePendingPermission(scopeKey, pending.permissionId),
  })
}

function renderPendingPermissionCards(scopeKey: string): void {
  const byID = pendingPermissionsByScope.get(scopeKey)
  if (!byID) return
  byID.forEach(pending => mountPendingPermissionCard(scopeKey, pending))
}

function emptySessionPanelState(): SessionPanelState {
  return {
    supported: null,
    sessions: [],
    nextCursor: '',
    loading: false,
    loadingMore: false,
    error: '',
  }
}

function sessionPanelState(threadId: string): SessionPanelState {
  return sessionPanelStateByThread.get(threadId) ?? emptySessionPanelState()
}

function setSessionPanelState(threadId: string, next: SessionPanelState): void {
  sessionPanelStateByThread.set(threadId, {
    ...next,
    sessions: dedupeSessionItems(next.sessions),
    nextCursor: next.nextCursor.trim(),
    error: next.error.trim(),
  })
}

function dedupeSessionItems(items: SessionInfo[]): SessionInfo[] {
  const deduped: SessionInfo[] = []
  const seen = new Set<string>()
  for (const item of items) {
    const sessionId = item.sessionId?.trim() ?? ''
    if (!sessionId || seen.has(sessionId)) continue
    seen.add(sessionId)
    deduped.push({
      ...item,
      sessionId,
      cwd: item.cwd?.trim() || undefined,
      title: item.title?.trim() || undefined,
      updatedAt: item.updatedAt?.trim() || undefined,
    })
  }
  return deduped
}

function updateThreadSessionID(threadId: string, sessionID: string): void {
  sessionID = sessionID.trim()
  const state = store.get()
  const nextThreads = state.threads.map(thread => {
    if (thread.threadId !== threadId) return thread
    return {
      ...thread,
      agentOptions: buildThreadAgentOptionsWithSession(thread.agentOptions, sessionID),
    }
  })
  store.set({ threads: nextThreads })
}

async function loadThreadSessions(threadId: string, append = false): Promise<void> {
  const thread = store.get().threads.find(item => item.threadId === threadId)
  if (!thread) return

  const current = sessionPanelState(threadId)
  if (append) {
    if (!current.nextCursor || current.loadingMore || current.loading) return
    setSessionPanelState(threadId, {
      ...current,
      loadingMore: true,
      error: '',
    })
  } else {
    setSessionPanelState(threadId, {
      ...current,
      loading: true,
      loadingMore: false,
      error: '',
      nextCursor: '',
    })
  }
  updateSessionPanel()

  const requestSeq = ++sessionPanelRequestSeq
  sessionPanelRequestSeqByThread.set(threadId, requestSeq)
  try {
    const response = await api.getThreadSessions(threadId, append ? current.nextCursor : '')
    if (sessionPanelRequestSeqByThread.get(threadId) !== requestSeq) return

    const base = append ? sessionPanelState(threadId).sessions : []
    setSessionPanelState(threadId, {
      supported: response.supported,
      sessions: [...base, ...response.sessions],
      nextCursor: response.nextCursor,
      loading: false,
      loadingMore: false,
      error: '',
    })
  } catch (err) {
    if (sessionPanelRequestSeqByThread.get(threadId) !== requestSeq) return
    const message = err instanceof Error ? err.message : 'Failed to load sessions.'
    setSessionPanelState(threadId, {
      ...sessionPanelState(threadId),
      loading: false,
      loadingMore: false,
      error: message,
    })
  }

  if (store.get().activeThreadId === threadId) {
    updateSessionPanel()
  }
}

async function switchThreadSession(thread: Thread, nextSessionID: string): Promise<void> {
  const targetSessionID = nextSessionID.trim()
  const currentSessionID = threadSessionID(thread)
  if (targetSessionID && currentSessionID === targetSessionID) return
  if (!targetSessionID && !currentSessionID) {
    const state = store.get()
    store.set({
      messages: activateFreshSessionScope(thread.threadId, state.messages),
    })
    return
  }
  if (sessionSwitchingThreads.has(thread.threadId)) return

  sessionSwitchingThreads.add(thread.threadId)
  updateSessionPanel()
  if (store.get().activeThreadId === thread.threadId) {
    updateInputState()
  }
  try {
    const updatedThread = await api.updateThread(thread.threadId, {
      agentOptions: buildThreadAgentOptionsWithSession(thread.agentOptions, targetSessionID),
    })
    const state = store.get()
    const nextMessages = !targetSessionID
      ? activateFreshSessionScope(thread.threadId, state.messages)
      : state.messages
    store.set({
      threads: state.threads.map(item => (item.threadId === thread.threadId ? updatedThread : item)),
      messages: nextMessages,
    })
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Failed to update session.'
    window.alert(message)
  } finally {
    sessionSwitchingThreads.delete(thread.threadId)
    if (store.get().activeThreadId === thread.threadId) {
      updateInputState()
      updateSessionPanel()
    }
  }
}

function renderSessionItem(item: SessionInfo, active: boolean, loading: boolean): string {
  const title = item.title?.trim() || item.sessionId
  const updatedLabel = item.updatedAt ? formatRelativeTime(item.updatedAt) : ''
  return `
    <button
      class="session-item ${active ? 'session-item--active' : ''}"
      type="button"
      data-session-id="${escHtml(item.sessionId)}"
      aria-pressed="${active ? 'true' : 'false'}"
      title="${escHtml(title)}"
    >
      <div class="session-item-main">
        <div class="session-item-title-row">
          ${renderSessionStatusIndicator(loading)}
          <div class="session-item-title">${escHtml(title)}</div>
        </div>
        ${updatedLabel ? `<div class="session-item-meta">${escHtml(updatedLabel)}</div>` : ''}
      </div>
    </button>`
}

function renderSessionPanel(): string {
  const { activeThreadId, threads, streamStates } = store.get()
  const thread = activeThreadId ? threads.find(item => item.threadId === activeThreadId) : null
  if (!thread) {
    return ''
  }

  if (!sessionPanelExpanded) {
    return `
      <div class="session-panel-collapsed">
        <button class="btn btn-icon session-sidebar-toggle-btn" type="button">
          ${iconChevronRight}
        </button>
        <div class="session-panel-collapsed-marker" title="${escHtml(threadTitle(thread))}">${escHtml(threadMonogramLabel(thread))}</div>
      </div>`
  }

  const state = sessionPanelState(thread.threadId)
  const selectedSessionID = threadSessionID(thread)
  const switching = sessionSwitchingThreads.has(thread.threadId)
  const disabled = switching
  const refreshDisabled = disabled || state.loading || state.loadingMore

  const knownIDs = new Set(state.sessions.map(item => item.sessionId))
  const sessions = [...state.sessions]
  if (selectedSessionID && !knownIDs.has(selectedSessionID)) {
    sessions.unshift({ sessionId: selectedSessionID, title: selectedSessionID })
  }
  const loadingSessionIDs = new Set(
    Object.values(streamStates)
      .filter(streamState => streamState.threadId === thread.threadId && !!streamState.sessionId)
      .map(streamState => streamState.sessionId),
  )

  let bodyHTML = ''
  if (state.loading && !sessions.length) {
    bodyHTML = `<div class="session-panel-empty">Loading sessions…</div>`
  } else if (state.error && !sessions.length) {
    bodyHTML = `<div class="session-panel-empty session-panel-empty--error">${escHtml(state.error)}</div>`
  } else if (state.supported === false) {
    bodyHTML = `<div class="session-panel-empty">This agent does not expose ACP session history.</div>`
  } else {
    const itemsHTML = sessions.length
      ? sessions.map(item => renderSessionItem(
          item,
          item.sessionId === selectedSessionID,
          loadingSessionIDs.has(item.sessionId),
        )).join('')
      : `<div class="session-panel-empty">No previous sessions for this working directory.</div>`
    const showMoreHTML = state.nextCursor
      ? `<button class="btn btn-ghost session-show-more-btn" type="button" ${state.loadingMore || disabled ? 'disabled' : ''}>
          ${state.loadingMore ? 'Loading…' : 'Show more'}
        </button>`
      : ''
    bodyHTML = `
      <div class="session-list">${itemsHTML}</div>
      ${showMoreHTML}
      ${state.error && sessions.length
        ? `<div class="session-panel-inline-error">${escHtml(state.error)}</div>`
        : ''}`
  }

  return `
    <div class="session-panel-header">
      <div class="session-panel-heading-row">
        <div class="session-panel-heading-copy">
          <div class="session-panel-title-row">
            <h3 class="session-panel-title" title="${escHtml(threadTitle(thread))}">${escHtml(threadTitle(thread))}</h3>
            <span class="badge badge--agent">${escHtml(thread.agent ?? '')}</span>
          </div>
          <div class="session-panel-subtitle" title="${escHtml(thread.cwd)}">${escHtml(thread.cwd)}</div>
        </div>
        <div class="session-panel-actions">
          <button
            class="btn btn-icon session-refresh-btn ${state.loading ? 'session-refresh-btn--loading' : ''}"
            type="button"
            title="${state.loading ? 'Refreshing sessions' : 'Refresh sessions'}"
            aria-label="${state.loading ? 'Refreshing sessions' : 'Refresh sessions'}"
            ${refreshDisabled ? 'disabled' : ''}>
            ${iconRefresh}
          </button>
          <button class="btn btn-icon session-sidebar-toggle-btn" type="button">
            ${iconChevronRight}
          </button>
        </div>
      </div>
      <button
        class="btn btn-ghost session-new-btn session-new-btn--full"
        type="button"
        title="New session"
        aria-label="New session"
        ${disabled ? 'disabled' : ''}>
        ${iconPlus}
        <span>New session</span>
      </button>
    </div>
    <div class="session-panel-body">
      ${bodyHTML}
    </div>`
}

function updateSessionPanel(): void {
  const el = document.getElementById('session-sidebar')
  if (!el) return

  const renderedThreadID = el.dataset.threadId?.trim() ?? ''
  const previousBody = el.querySelector<HTMLElement>('.session-panel-body')
  if (renderedThreadID && previousBody) {
    sessionPanelScrollTopByThread.set(renderedThreadID, previousBody.scrollTop)
  }

  el.innerHTML = renderSessionPanel()
  syncSidebarChrome()
  const { activeThreadId, threads } = store.get()
  const thread = activeThreadId ? threads.find(item => item.threadId === activeThreadId) : null

  el.querySelectorAll<HTMLButtonElement>('.session-sidebar-toggle-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      setSessionPanelExpanded(!sessionPanelExpanded)
    })
  })

  if (!thread) {
    delete el.dataset.threadId
    return
  }

  el.dataset.threadId = thread.threadId
  const nextBody = el.querySelector<HTMLElement>('.session-panel-body')
  if (nextBody) {
    nextBody.scrollTop = sessionPanelScrollTopByThread.get(thread.threadId) ?? 0
  }

  const state = sessionPanelState(thread.threadId)
  if (sessionPanelExpanded && state.supported === null && !state.loading && !state.loadingMore && !state.error) {
    void loadThreadSessions(thread.threadId)
  }

  el.querySelector<HTMLButtonElement>('.session-refresh-btn')?.addEventListener('click', () => {
    void loadThreadSessions(thread.threadId)
  })

  el.querySelector<HTMLButtonElement>('.session-new-btn')?.addEventListener('click', () => {
    void switchThreadSession(thread, '')
  })

  el.querySelectorAll<HTMLButtonElement>('.session-item[data-session-id]').forEach(btn => {
    btn.addEventListener('click', () => {
      const sessionID = btn.dataset.sessionId?.trim() ?? ''
      if (!sessionID || sessionID === threadSessionID(thread)) return
      void switchThreadSession(thread, sessionID)
    })
  })

  el.querySelector<HTMLButtonElement>('.session-show-more-btn')?.addEventListener('click', () => {
    void loadThreadSessions(thread.threadId, true)
  })
}

// ── Thread list rendering ─────────────────────────────────────────────────

function skeletonItems(): string {
  return Array.from({ length: 3 }, () => `
    <div class="thread-skeleton">
      <div class="skeleton thread-skeleton-avatar"></div>
      <div class="thread-skeleton-lines">
        <div class="skeleton thread-skeleton-line" style="width:70%"></div>
        <div class="skeleton thread-skeleton-line" style="width:50%"></div>
      </div>
    </div>`).join('')
}

function threadTitle(t: Thread): string {
  if (t.title) return t.title
  return t.cwd.split('/').filter(Boolean).pop() ?? t.cwd
}

function monogramLabel(value: string): string {
  const firstChar = Array.from(value.trim())[0] ?? 'A'
  return /^[A-Za-z]$/.test(firstChar) ? firstChar.toUpperCase() : firstChar
}

function threadMonogramLabel(thread: Thread): string {
  const label = threadTitle(thread) || thread.agent || 'A'
  return monogramLabel(label)
}

function syncSidebarChrome(): void {
  const sidebar = document.getElementById('sidebar')
  if (sidebar) {
    sidebar.classList.add('sidebar--expanded')
  }

  const { activeThreadId, threads } = store.get()
  const hasActiveThread = !!(activeThreadId && threads.some(thread => thread.threadId === activeThreadId))
  const sessionSidebar = document.getElementById('session-sidebar')
  if (sessionSidebar) {
    sessionSidebar.hidden = !hasActiveThread
    sessionSidebar.classList.toggle('session-sidebar--expanded', hasActiveThread && sessionPanelExpanded)
    sessionSidebar.classList.toggle('session-sidebar--collapsed', hasActiveThread && !sessionPanelExpanded)
  }

  document.querySelectorAll<HTMLButtonElement>('.session-sidebar-toggle-btn').forEach(btn => {
    const label = sessionPanelExpanded ? 'Collapse session list' : 'Expand session list'
    btn.setAttribute('aria-label', label)
    btn.setAttribute('title', label)
    btn.setAttribute('aria-expanded', sessionPanelExpanded ? 'true' : 'false')
  })
}

function setSessionPanelExpanded(expanded: boolean): void {
  const next = !!expanded
  if (sessionPanelExpanded === next) {
    syncSidebarChrome()
    return
  }

  sessionPanelExpanded = next
  syncSidebarChrome()
  updateSessionPanel()
}

type ConfigPickerState = 'loading' | 'empty' | 'ready'

interface ConfigPickerOption {
  value: string
  name: string
  description: string
}

interface ConfigPickerLabels {
  loadingLabel: string
  emptyLabel: string
}

interface ConfigPickerData {
  state: ConfigPickerState
  configId: string
  selectedValue: string
  selectedLabel: string
  options: ConfigPickerOption[]
}

function findReasoningOption(options: ConfigOption[]): ConfigOption | null {
  for (const option of options) {
    const category = option.category?.trim().toLowerCase() ?? ''
    const id = option.id.trim().toLowerCase()
    if (category === 'reasoning' || id === 'reasoning') {
      return option
    }
  }
  return null
}

function countConfigOptionChoices(configOption: ConfigOption | null): number {
  if (!configOption) return 0

  const values = new Set<string>()
  for (const option of configOption.options ?? []) {
    const value = option.value?.trim() ?? ''
    if (!value) continue
    values.add(value)
  }
  return values.size
}

function shouldShowReasoningSwitch(configOption: ConfigOption | null): boolean {
  return countConfigOptionChoices(configOption) > 1
}

function fallbackThreadConfigValue(thread: Thread, configId: string): string {
  const trimmedConfigID = configId.trim()
  if (!trimmedConfigID) return ''
  if (trimmedConfigID.toLowerCase() === 'model') {
    return fallbackThreadModelID(thread)
  }

  const rawOverrides = thread.agentOptions?.configOverrides
  if (!rawOverrides || typeof rawOverrides !== 'object') return ''
  const value = (rawOverrides as Record<string, unknown>)[trimmedConfigID]
  return typeof value === 'string' ? value.trim() : ''
}

function currentValueForConfig(options: ConfigOption[], configId: string): string {
  const trimmedConfigID = configId.trim()
  if (!trimmedConfigID) return ''
  const option = options.find(item => item.id === trimmedConfigID)
  return option?.currentValue?.trim() ?? ''
}

function getThreadConfigOptionsForRender(thread: Thread): ConfigOption[] {
  const threadOptions = threadConfigCache.get(thread.threadId) ?? []
  const agentCatalog = getAgentConfigCatalog(thread.agent ?? '', fallbackThreadModelID(thread))

  if (!agentCatalog.length) {
    return cloneConfigOptions(threadOptions)
  }

  const merged: ConfigOption[] = []
  const seen = new Set<string>()

  for (const catalogOption of agentCatalog) {
    const configId = catalogOption.id.trim()
    if (!configId) continue
    seen.add(configId)

    const currentValue = currentValueForConfig(threadOptions, configId) || fallbackThreadConfigValue(thread, configId)
    merged.push({
      ...catalogOption,
      currentValue,
      options: [...(catalogOption.options ?? [])],
    })
  }

  for (const threadOption of threadOptions) {
    const configId = threadOption.id.trim()
    if (!configId || seen.has(configId)) continue
    merged.push({
      ...threadOption,
      options: [...(threadOption.options ?? [])],
    })
  }

  return merged
}

function resolveConfigPickerData(
  configOption: ConfigOption | null,
  fallbackValue: string,
  loading: boolean,
  labels: ConfigPickerLabels,
): ConfigPickerData {
  if (loading) {
    return {
      state: 'loading',
      configId: configOption?.id?.trim() ?? '',
      selectedValue: '',
      selectedLabel: labels.loadingLabel,
      options: [],
    }
  }

  const rawOptions = configOption?.options ?? []
  const options: ConfigPickerOption[] = rawOptions
    .map(option => ({
      value: option.value.trim(),
      name: (option.name || option.value).trim() || option.value.trim(),
      description: option.description?.trim() || '',
    }))
    .filter(option => !!option.value)

  if (!options.length) {
    if (fallbackValue) {
      return {
        state: 'ready',
        configId: configOption?.id?.trim() ?? '',
        selectedValue: fallbackValue,
        selectedLabel: fallbackValue,
        options: [{ value: fallbackValue, name: fallbackValue, description: '' }],
      }
    }
    return {
      state: 'empty',
      configId: configOption?.id?.trim() ?? '',
      selectedValue: '',
      selectedLabel: labels.emptyLabel,
      options: [],
    }
  }

  const selectedValue = configOption?.currentValue?.trim() || fallbackValue || options[0].value
  const selectedOption = options.find(option => option.value === selectedValue) ?? options[0]
  return {
    state: 'ready',
    configId: configOption?.id?.trim() ?? '',
    selectedValue: selectedOption.value,
    selectedLabel: selectedOption.name,
    options,
  }
}

function renderConfigMenuOptions(
  options: ConfigPickerOption[],
  selectedValue: string,
  state: ConfigPickerState,
  labels: ConfigPickerLabels,
): string {
  if (state === 'loading') {
    return `<div class="thread-model-option-item thread-model-option-item--disabled">
      <div class="thread-model-option-name">${escHtml(labels.loadingLabel)}</div>
    </div>`
  }
  if (state === 'empty' || !options.length) {
    return `<div class="thread-model-option-item thread-model-option-item--disabled">
      <div class="thread-model-option-name">${escHtml(labels.emptyLabel)}</div>
    </div>`
  }

  return options.map(option => {
    const activeClass = option.value === selectedValue ? ' thread-model-option-item--active' : ''
    const descHTML = option.description
      ? `<div class="thread-model-option-desc">${escHtml(option.description)}</div>`
      : ''
    return `<button
      class="thread-model-option-item${activeClass}"
      type="button"
      data-value="${escHtml(option.value)}"
      role="option"
      aria-selected="${option.value === selectedValue ? 'true' : 'false'}"
    >
      <div class="thread-model-option-name">${escHtml(option.name)}</div>
      ${descHTML}
    </button>`
  }).join('')
}

function buildThreadAgentOptions(
  base: Record<string, unknown>,
  options: ConfigOption[],
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...base }
  const modelValue = findModelOption(options)?.currentValue?.trim() ?? ''
  if (modelValue) {
    next.modelId = modelValue
  } else {
    delete next.modelId
  }

  const configOverrides: Record<string, string> = {}
  for (const option of options) {
    const configId = option.id.trim()
    if (!configId || configId.toLowerCase() === 'model') continue
    const value = option.currentValue?.trim() ?? ''
    if (!value) continue
    configOverrides[configId] = value
  }
  if (Object.keys(configOverrides).length) {
    next.configOverrides = configOverrides
  } else {
    delete next.configOverrides
  }
  return next
}

function renderComposerConfigSwitch(
  key: 'model' | 'reasoning',
  label: string,
  pickerData: ConfigPickerData,
  labels: ConfigPickerLabels,
  disabled: boolean,
): string {
  return `
    <div class="thread-model-switch thread-model-switch--composer" data-picker-key="${escHtml(key)}">
      <button
        id="thread-${escHtml(key)}-trigger"
        class="thread-model-trigger"
        type="button"
        data-state="${escHtml(pickerData.state)}"
        data-selected-value="${escHtml(pickerData.selectedValue)}"
        data-config-id="${escHtml(pickerData.configId)}"
        aria-haspopup="listbox"
        aria-expanded="false"
        aria-label="${escHtml(label)}"
        ${disabled || pickerData.state !== 'ready' ? 'disabled' : ''}
      >
        <span class="thread-model-trigger-copy">
          <span class="thread-model-trigger-value">${escHtml(pickerData.selectedLabel)}</span>
        </span>
        <span class="thread-model-trigger-arrow">▾</span>
      </button>
      <div class="thread-model-menu" id="thread-${escHtml(key)}-menu" role="listbox" hidden>
        ${renderConfigMenuOptions(pickerData.options, pickerData.selectedValue, pickerData.state, labels)}
      </div>
    </div>`
}

const modelPickerLabels: ConfigPickerLabels = {
  loadingLabel: 'Loading models…',
  emptyLabel: 'No models available',
}

const reasoningPickerLabels: ConfigPickerLabels = {
  loadingLabel: 'Loading reasoning…',
  emptyLabel: 'No reasoning',
}

function renderAgentAvatar(agentId: string, variant: 'thread' | 'message'): string {
  const normalized = (agentId || '').trim().toLowerCase()
  const cls = variant === 'thread' ? 'thread-item-avatar-icon' : 'message-avatar-icon'
  if (normalized === 'codex') {
    return `<img src="${codexIconURL}" alt="Codex" class="${cls}" loading="lazy" decoding="async">`
  }
  if (normalized === 'gemini') {
    return `<img src="${geminiIconURL}" alt="Gemini CLI" class="${cls}" loading="lazy" decoding="async">`
  }
  if (normalized === 'claude') {
    return `<img src="${claudeIconURL}" alt="Claude Code" class="${cls}" loading="lazy" decoding="async">`
  }
  if (normalized === 'kimi') {
    return `<img src="${kimiIconURL}" alt="Kimi CLI" class="${cls} ${cls}--contain" loading="lazy" decoding="async">`
  }
  if (normalized === 'opencode') {
    return `<img src="${opencodeIconURL}" alt="OpenCode" class="${cls} ${cls}--contain" loading="lazy" decoding="async">`
  }
  if (normalized === 'qwen') {
    return `<img src="${qwenIconURL}" alt="Qwen Code" class="${cls}" loading="lazy" decoding="async">`
  }
  return escHtml((agentId || 'A').slice(0, 1).toUpperCase())
}

type ThreadActivityIndicator = 'loading' | 'done' | null

function renderThreadStatusIndicator(status: ThreadActivityIndicator): string {
  if (status === 'loading') {
    return `
      <span
        class="thread-status-indicator thread-status-indicator--loading"
        role="status"
        aria-label="Agent is working"
        title="Agent is working"
      >
        <span class="thread-status-spinner" aria-hidden="true"></span>
      </span>`
  }
  if (status === 'done') {
    return `
      <span
        class="thread-status-indicator thread-status-indicator--done"
        role="img"
        aria-label="Latest turn finished"
        title="Latest turn finished"
      >
        ${iconCheck}
      </span>`
  }
  return ''
}

function renderSessionStatusIndicator(loading: boolean): string {
  if (!loading) return ''
  return `
      <span
        class="thread-status-indicator session-status-indicator thread-status-indicator--loading"
        role="status"
        aria-label="Session is working"
        title="Session is working"
      >
        <span class="thread-status-spinner" aria-hidden="true"></span>
      </span>`
}

function renderThreadItem(
  t: Thread,
  activeId: string | null,
  activityIndicator: ThreadActivityIndicator,
): string {
  const isActive = t.threadId === activeId
  const isMenuOpen = openThreadActionMenuId === t.threadId
  const avatar = renderAgentAvatar(t.agent ?? '', 'thread')
  const displayTitle = threadTitle(t)
  const relTime = t.updatedAt ? formatRelativeTime(t.updatedAt) : ''

  return `
    <div class="thread-item ${isActive ? 'thread-item--active' : ''} ${isMenuOpen ? 'thread-item--menu-open' : ''}"
         data-thread-id="${escHtml(t.threadId)}"
         role="button"
         tabindex="0"
         aria-label="${escHtml(displayTitle)}">
      <div class="thread-item-avatar ${isActive ? '' : 'thread-item-avatar--inactive'}">${avatar}</div>
      <div class="thread-item-body">
        <div class="thread-item-title">${escHtml(displayTitle)}</div>
        <div class="thread-item-foot">
          <span class="badge badge--agent">${escHtml(t.agent ?? '')}</span>
          <span class="thread-item-time">${relTime}</span>
        </div>
      </div>
      <div class="thread-item-actions">
        ${renderThreadStatusIndicator(activityIndicator)}
        <button class="btn btn-ghost btn-sm thread-item-menu-trigger" type="button"
                data-thread-id="${escHtml(t.threadId)}"
                aria-expanded="${isMenuOpen ? 'true' : 'false'}"
                aria-label="Agent actions">
          ...
        </button>
      </div>
    </div>`
}

function updateThreadList(): void {
  const el = document.getElementById('thread-list')
  if (!el) return

  const { threads, activeThreadId, streamStates, threadCompletionBadges } = store.get()
  const filtered = threads

  if (!filtered.length) {
    el.innerHTML = `
      <div class="thread-list-empty">
        No agents yet.<br>Click <strong>+</strong> to start one.
      </div>`
    renderThreadActionLayer()
    return
  }

  el.innerHTML = filtered
    .map(t => {
      const isActive = t.threadId === activeThreadId
      const activityIndicator: ThreadActivityIndicator = Object.values(streamStates).some(streamState => streamState.threadId === t.threadId)
        ? 'loading'
        : (!isActive && threadCompletionBadges[t.threadId] ? 'done' : null)
      return renderThreadItem(t, activeThreadId, activityIndicator)
    })
    .join('')

  el.querySelectorAll<HTMLButtonElement>('.thread-item-menu-trigger').forEach(btn => {
    btn.addEventListener('click', e => {
      e.preventDefault()
      e.stopPropagation()
      const id = btn.dataset.threadId ?? ''
      if (!id) return
      toggleThreadActionMenu(id)
    })
    btn.addEventListener('keydown', e => e.stopPropagation())
  })

  el.querySelectorAll<HTMLElement>('.thread-item').forEach(item => {
    const handler = (event?: Event) => {
      const target = event?.target as HTMLElement | null
      if (target?.closest('.thread-item-menu-trigger') || target?.closest('.thread-action-popover')) return
      const id = item.dataset.threadId ?? ''
      activateThread(id)
      // Close mobile sidebar on thread select
      document.getElementById('sidebar')?.classList.remove('sidebar--open')
    }
    item.addEventListener('click', handler)
    item.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') handler(e)
    })
  })

  renderThreadActionLayer()
}

async function handleRenameThread(threadId: string, nextTitle: string): Promise<void> {
  const snapshot = store.get()
  const thread = snapshot.threads.find(t => t.threadId === threadId)
  if (!thread) return

  const title = nextTitle.trim()
  if (title === thread.title) {
    cancelThreadRename(threadId)
    return
  }

  let updatedThread: Thread
  try {
    updatedThread = await api.updateThread(threadId, { title })
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Unknown error'
    window.alert(`Failed to rename agent: ${message}`)
    return
  }

  resetThreadActionMenuState()
  const state = store.get()
  store.set({
    threads: state.threads.map(item => (item.threadId === threadId ? updatedThread : item)),
  })
  if (state.activeThreadId === threadId) {
    updateChatArea()
  }
}

async function handleDeleteThread(threadId: string): Promise<void> {
  const snapshot = store.get()
  const thread = snapshot.threads.find(t => t.threadId === threadId)
  if (!thread) return

  const label = threadTitle(thread)
  if (!window.confirm(`Delete agent "${label}"? This will permanently remove its history.`)) return

  try {
    await api.deleteThread(threadId)
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Unknown error'
    window.alert(`Failed to delete agent: ${message}`)
    return
  }

  resetThreadActionMenuState()
  const state = store.get()
  const nextThreads = state.threads.filter(t => t.threadId !== threadId)
  const nextMessages = { ...state.messages }
  const threadScopePrefix = `${threadId}::`
  Object.keys(nextMessages).forEach(scopeKey => {
    if (scopeKey.startsWith(threadScopePrefix)) {
      delete nextMessages[scopeKey]
    }
  })

  const deletingActive = state.activeThreadId === threadId
  const nextActiveThreadId = deletingActive ? (nextThreads[0]?.threadId ?? null) : state.activeThreadId
  Array.from(streamsByScope.entries()).forEach(([scopeKey, stream]) => {
    if (!scopeKey.startsWith(threadScopePrefix)) return
    stream.abort()
    clearScopeStreamRuntime(scopeKey)
  })
  Array.from(pendingPermissionsByScope.keys()).forEach(scopeKey => {
    if (scopeKey.startsWith(threadScopePrefix)) {
      clearPendingPermissions(scopeKey)
    }
  })
  threadConfigCache.delete(threadId)
  threadConfigSwitching.delete(threadId)
  Array.from(loadedHistoryScopeKeys).forEach(scopeKey => {
    if (scopeKey.startsWith(threadScopePrefix)) {
      loadedHistoryScopeKeys.delete(scopeKey)
    }
  })
  sessionPanelStateByThread.delete(threadId)
  sessionPanelRequestSeqByThread.delete(threadId)
  sessionPanelScrollTopByThread.delete(threadId)
  sessionSwitchingThreads.delete(threadId)
  freshSessionNonceByThread.delete(threadId)
  let nextThreadCompletionBadges = omitThreadCompletionBadge(state.threadCompletionBadges, threadId)
  if (nextActiveThreadId) {
    nextThreadCompletionBadges = omitThreadCompletionBadge(nextThreadCompletionBadges, nextActiveThreadId)
  }

  store.set({
    threads: nextThreads,
    messages: nextMessages,
    activeThreadId: nextActiveThreadId,
    threadCompletionBadges: nextThreadCompletionBadges,
  })
}

// ── History helpers ───────────────────────────────────────────────────────

/** Convert server Turn[] to the client Message[] model. */
function turnsToMessages(turns: Turn[]): Message[] {
  const msgs: Message[] = []
  for (const t of turns) {
    if (t.isInternal) continue

    if (t.requestText) {
      msgs.push({
        id:        `${t.turnId}-u`,
        role:      'user',
        content:   t.requestText,
        timestamp: t.createdAt,
        status:    'done',
        turnId:    t.turnId,
      })
    }

    if (t.status !== 'running') {
      const planEntries = extractTurnPlanEntries(t.events)
      const reasoning = extractTurnReasoning(t.events)
      const toolCalls = extractTurnToolCalls(t.events)
      const segments = buildMessageSegmentsFromTurn(t.turnId, t.events, t.responseText)
      const agentStatus: Message['status'] =
        t.status === 'cancelled' ? 'cancelled' :
        t.status === 'error'     ? 'error'     :
        'done'

      msgs.push({
        id:           `${t.turnId}-a`,
        role:         'agent',
        content:      t.responseText,
        timestamp:    t.completedAt || t.createdAt,
        status:       agentStatus,
        turnId:       t.turnId,
        stopReason:   t.stopReason   || undefined,
        errorMessage: t.errorMessage || undefined,
        segments,
        planEntries,
        toolCalls,
        reasoning: hasReasoningText(reasoning) ? reasoning : undefined,
      })
    }
  }
  return msgs
}

function extractTurnSessionID(events: TurnEvent[] | undefined): string {
  let sessionID = ''
  for (const event of events ?? []) {
    if (event.type !== 'session_bound') continue
    const value = event.data?.sessionId
    if (typeof value !== 'string') continue
    const nextSessionID = value.trim()
    if (nextSessionID) sessionID = nextSessionID
  }
  return sessionID
}

function filterTurnsBySession(turns: Turn[], sessionID: string): Turn[] {
  sessionID = sessionID.trim()
  const assignments = turns.map(turn => ({
    turn,
    sessionID: extractTurnSessionID(turn.events),
  }))
  const annotatedSessions = new Set(assignments.map(item => item.sessionID).filter(Boolean))
  const isEphemeralCancelledTurn = (turn: Turn): boolean => turn.status === 'cancelled' && !turn.responseText.trim()

  // Legacy turns created before session-bound persistence have no per-turn session marker.
  // If the thread has no annotated turns at all, keep showing the history instead of hiding everything.
  if (annotatedSessions.size === 0) {
    return turns.filter(turn => !isEphemeralCancelledTurn(turn))
  }

  if (!sessionID) {
    return assignments
      .filter(item => item.sessionID === '' && !isEphemeralCancelledTurn(item.turn))
      .map(item => item.turn)
  }

  const hasMatchedAnnotatedTurns = assignments.some(item => item.sessionID === sessionID)
  if (!hasMatchedAnnotatedTurns) {
    return []
  }

  const includeUnannotatedLegacyTurns = annotatedSessions.size === 1 && annotatedSessions.has(sessionID)
  return assignments
    .filter(item => item.sessionID === sessionID || (includeUnannotatedLegacyTurns && item.sessionID === ''))
    .map(item => item.turn)
}

function sessionTranscriptToMessages(messages: SessionTranscriptMessage[], sessionID: string): Message[] {
  return messages
    .filter(message => !!message.content)
    .map((message, index) => ({
      id: `session-${sessionID}-${index}`,
      role: message.role === 'assistant' ? 'agent' : 'user',
      content: message.content,
      timestamp: message.timestamp || '',
      status: 'done',
    }))
}

function messageReplayKey(message: Message): string {
  return `${message.role}\n${message.content}`
}

function mergeSessionReplayMessages(replayMessages: Message[], localMessages: Message[]): Message[] {
  if (!replayMessages.length) return localMessages
  if (!localMessages.length) return replayMessages

  let overlap = 0
  const maxOverlap = Math.min(replayMessages.length, localMessages.length)
  for (let size = maxOverlap; size > 0; size -= 1) {
    let matches = true
    for (let index = 0; index < size; index += 1) {
      const replayMessage = replayMessages[replayMessages.length - size + index]
      const localMessage = localMessages[index]
      if (messageReplayKey(replayMessage) !== messageReplayKey(localMessage)) {
        matches = false
        break
      }
    }
    if (matches) {
      overlap = size
      break
    }
  }

  return [...replayMessages, ...localMessages.slice(overlap)]
}

async function loadHistory(threadId: string): Promise<void> {
  const requestedThread = store.get().threads.find(item => item.threadId === threadId)
  const requestedSessionID = threadSessionID(requestedThread)
  const requestedScopeKey = threadChatScopeKey(requestedThread)
  if (!requestedScopeKey) return
  if (!requestedSessionID && isFreshSessionScopeKey(requestedScopeKey)) return
  try {
    const turns = await api.getHistory(threadId)
    const state = store.get()
    if (state.activeThreadId !== threadId) return
    const activeThread = state.threads.find(item => item.threadId === threadId)
    if (!activeThread || threadSessionID(activeThread) !== requestedSessionID) return
    if (getScopeStreamState(requestedScopeKey)) return

    const localMessages = turnsToMessages(filterTurnsBySession(turns, requestedSessionID))
    const cachedMessages = state.messages[requestedScopeKey] ?? []
    let nextMessages = localMessages
    if (requestedSessionID) {
      // When a fresh ACP session is created from "Current: new", Codex transcripts
      // include the injected context prompt. Reuse the in-memory turn messages in
      // that transition instead of replaying transcript noise back into the chat.
      if (!loadedHistoryScopeKeys.has(requestedScopeKey) && loadedHistoryScopeKeys.has(threadSessionScopeKey(threadId, '')) && localMessages.length && cachedMessages.length) {
        nextMessages = mergeSessionReplayMessages(cachedMessages, localMessages)
      } else {
        try {
          const replay = await api.getThreadSessionHistory(threadId, requestedSessionID)
          const transcriptState = store.get()
          if (transcriptState.activeThreadId !== threadId) return
          const transcriptThread = transcriptState.threads.find(item => item.threadId === threadId)
          if (!transcriptThread || threadSessionID(transcriptThread) !== requestedSessionID) return
          if (getScopeStreamState(requestedScopeKey)) return

          if (replay.supported && replay.messages.length) {
            const replayMessages = sessionTranscriptToMessages(replay.messages, requestedSessionID)
            nextMessages = mergeSessionReplayMessages(replayMessages, localMessages)
          }
        } catch {
          nextMessages = localMessages
        }
      }
    }

    const finalState = store.get()
    if (finalState.activeThreadId !== threadId) return
    const finalThread = finalState.threads.find(item => item.threadId === threadId)
    if (!finalThread || threadSessionID(finalThread) !== requestedSessionID) return
    if (getScopeStreamState(requestedScopeKey)) return

    loadedHistoryScopeKeys.add(requestedScopeKey)
    store.set({
      messages: {
        ...finalState.messages,
        [requestedScopeKey]: nextMessages,
      },
    })
  } catch {
    if (store.get().activeThreadId !== threadId) return
    if (threadSessionID(store.get().threads.find(item => item.threadId === threadId)) !== requestedSessionID) return
    // Show error only if no matching local history was already rendered.
    if (!loadedHistoryScopeKeys.has(requestedScopeKey)) {
      const listEl = document.getElementById('message-list')
      if (listEl) {
        listEl.innerHTML = `<div class="thread-list-empty" style="color:var(--error)">Failed to load history.</div>`
      }
    }
  }
}

// ── Message rendering ─────────────────────────────────────────────────────

function formatPlanLabel(value: string | undefined): string {
  return (value ?? '').replace(/_/g, ' ').trim()
}

function planStatusClassName(status: string | undefined): string {
  const normalized = (status ?? '').trim().toLowerCase()
  if (!normalized || !/^[a-z_]+$/.test(normalized)) return ''
  return ` message-plan__item--${normalized}`
}

function renderPlanInnerHTML(entries: PlanEntry[]): string {
  return `
    <div class="message-plan__header">Plan</div>
    <ol class="message-plan__list">
      ${entries.map(entry => {
        const status = formatPlanLabel(entry.status)
        const priority = formatPlanLabel(entry.priority)
        const meta = [status, priority]
          .filter(Boolean)
          .map(text => `<span class="message-plan__tag">${escHtml(text)}</span>`)
          .join('')
        const statusClass = planStatusClassName(entry.status)
        return `
          <li class="message-plan__item${statusClass}">
            <span class="message-plan__content">${escHtml(entry.content)}</span>
            ${meta ? `<span class="message-plan__meta">${meta}</span>` : ''}
          </li>`
      }).join('')}
    </ol>`
}

function renderPlanSectionHTML(entries: PlanEntry[] | undefined, extraClass = ''): string {
  const normalized = clonePlanEntries(entries)
  if (!normalized?.length) return ''
  return `<div class="message-plan${extraClass}">${renderPlanInnerHTML(normalized)}</div>`
}

function formatToolCallLabel(value: string | undefined): string {
  return (value ?? '').replace(/_/g, ' ').trim()
}

function toolCallDisplayTitle(toolCall: ToolCall): string {
  const title = toolCall.title?.trim()
  if (title) return title
  const kind = formatToolCallLabel(toolCall.kind)
  if (kind) return kind
  return 'Tool call'
}

function toolCallStatusClassName(status: string | undefined): string {
  const normalized = (status ?? '').trim().toLowerCase()
  if (!normalized || !/^[a-z_]+$/.test(normalized)) return ''
  return ` message-tool-call__card--${normalized}`
}

function renderToolCallPreHTML(text: string, collapsible = false): string {
  const preID = nextToolCallPreID()
  const collapsedClass = collapsible ? ' message-tool-call__pre--collapsed' : ''
  const expandBtn = collapsible
    ? `<button class="message-tool-call__expand-btn" data-target="${preID}" type="button" hidden>Show all</button>`
    : ''
  return `
    <div class="message-tool-call__pre-wrap">
      <pre class="message-tool-call__pre${collapsedClass}" id="${preID}">${escHtml(text)}</pre>
      ${expandBtn}
    </div>`
}

function renderToolCallJSON(value: unknown, collapsible = false): string {
  if (value === undefined) return ''
  const formatted = JSON.stringify(value, null, 2)
  return renderToolCallPreHTML(formatted ?? String(value), collapsible)
}

function renderToolCallTagHTML(value: string, extraClass = ''): string {
  const className = `message-tool-call__tag${extraClass ? ` ${extraClass}` : ''}`
  return `<span class="${className}" title="${escHtml(value)}">${escHtml(value)}</span>`
}

function renderToolCallLocationHTML(location: unknown): string {
  if (location && typeof location === 'object') {
    const record = location as Record<string, unknown>
    const path = typeof record.path === 'string' ? record.path.trim() : ''
    if (path) {
      const meta = Object.entries(record)
        .filter(([key]) => key !== 'path')
        .map(([key, value]) => `${key}: ${String(value)}`)
        .join(' · ')
      return `
        <li class="message-tool-call__location-item">
          <span class="message-tool-call__path">${escHtml(path)}</span>
          ${meta ? `<span class="message-tool-call__location-meta">${escHtml(meta)}</span>` : ''}
        </li>`
    }
  }
  return `<li class="message-tool-call__location-item">${renderToolCallJSON(location)}</li>`
}

function renderToolCallContentHTML(item: unknown): string {
  if (!item || typeof item !== 'object') {
    return renderToolCallJSON(item, true)
  }

  const record = item as Record<string, unknown>
  const type = typeof record.type === 'string' ? record.type.trim() : ''
  const path = typeof record.path === 'string' ? record.path.trim() : ''
  const command = typeof record.command === 'string' ? record.command.trim() : ''
  const heading = [formatToolCallLabel(type), path].filter(Boolean).join(' · ')

  if (type === 'content' && record.content && typeof record.content === 'object') {
    const nested = record.content as Record<string, unknown>
    const nestedType = typeof nested.type === 'string' ? nested.type.trim() : ''
    const text = typeof nested.text === 'string' ? nested.text : ''
    if (nestedType === 'text' && text) {
      return `
        <div class="message-tool-call__content-item">
          ${heading ? `<div class="message-tool-call__content-label">${escHtml(heading)}</div>` : ''}
          ${renderToolCallPreHTML(text, true)}
        </div>`
    }
  }

  if (type === 'command' && command) {
    return `
      <div class="message-tool-call__content-item">
        ${heading ? `<div class="message-tool-call__content-label">${escHtml(heading)}</div>` : ''}
        ${renderToolCallPreHTML(command, true)}
      </div>`
  }

  if (type === 'diff') {
    const oldText = typeof record.oldText === 'string' ? record.oldText : ''
    const newText = typeof record.newText === 'string' ? record.newText : ''
    return `
      <div class="message-tool-call__content-item">
        ${heading ? `<div class="message-tool-call__content-label">${escHtml(heading)}</div>` : ''}
        ${oldText ? `<div class="message-tool-call__diff-block"><div class="message-tool-call__diff-label">Before</div>${renderToolCallPreHTML(oldText, true)}</div>` : ''}
        ${newText ? `<div class="message-tool-call__diff-block"><div class="message-tool-call__diff-label">After</div>${renderToolCallPreHTML(newText, true)}</div>` : ''}
      </div>`
  }

  return `
    <div class="message-tool-call__content-item">
      ${heading ? `<div class="message-tool-call__content-label">${escHtml(heading)}</div>` : ''}
      ${renderToolCallJSON(item, true)}
    </div>`
}

function renderToolCallCardHTML(toolCall: ToolCall): string {
  const title = toolCallDisplayTitle(toolCall)
  const kind = formatToolCallLabel(toolCall.kind)
  const status = formatToolCallLabel(toolCall.status)
  const meta = [
    kind ? renderToolCallTagHTML(kind) : '',
    status ? renderToolCallTagHTML(status, 'message-tool-call__tag--status') : '',
  ].filter(Boolean).join('')
  const contentHTML = (toolCall.content ?? []).map(renderToolCallContentHTML).join('')
  const locationsHTML = toolCall.locations?.length
    ? `
      <div class="message-tool-call__section">
        <div class="message-tool-call__section-title">Locations</div>
        <ul class="message-tool-call__location-list">
          ${toolCall.locations.map(renderToolCallLocationHTML).join('')}
        </ul>
      </div>`
    : ''
  const rawInputHTML = toolCall.rawInput === undefined
    ? ''
    : `
      <div class="message-tool-call__section">
        <div class="message-tool-call__section-title">Input</div>
        ${renderToolCallJSON(toolCall.rawInput, true)}
      </div>`
  const rawOutputHTML = toolCall.rawOutput === undefined
    ? ''
    : `
      <div class="message-tool-call__section">
        <div class="message-tool-call__section-title">Output</div>
        ${renderToolCallJSON(toolCall.rawOutput, true)}
      </div>`

  return `
    <article class="message-tool-call__card${toolCallStatusClassName(toolCall.status)}">
      <div class="message-tool-call__header-row">
        <div class="message-tool-call__title" title="${escHtml(title)}">${escHtml(title)}</div>
        ${meta ? `<div class="message-tool-call__meta">${meta}</div>` : ''}
      </div>
      ${contentHTML ? `<div class="message-tool-call__section"><div class="message-tool-call__section-title">Content</div>${contentHTML}</div>` : ''}
      ${locationsHTML}
      ${rawInputHTML}
      ${rawOutputHTML}
    </article>`
}

function toolCallContentID(segmentID: string): string {
  return `tool-call-content-${segmentID}`
}

function renderToolCallPanelHTML(
  segmentID: string,
  toolCall: ToolCall,
  expanded = false,
  streaming = false,
): string {
  const state = reasoningPanelState(expanded)
  const contentID = toolCallContentID(segmentID)
  const title = toolCallDisplayTitle(toolCall)
  const kind = formatToolCallLabel(toolCall.kind)
  const status = formatToolCallLabel(toolCall.status)
  return `
    <div
      class="message-tool-call${streaming ? ' message-tool-call--streaming' : ''}"
      data-segment-id="${escHtml(segmentID)}"
      data-state="${state}"
    >
      <button
        class="message-tool-call__toggle"
        type="button"
        data-segment-id="${escHtml(segmentID)}"
        data-state="${state}"
        aria-expanded="${expanded ? 'true' : 'false'}"
        aria-controls="${escHtml(contentID)}"
      >
        <span class="message-tool-call__toggle-main">
          <span class="message-tool-call__toggle-icon" aria-hidden="true">${iconTool}</span>
          <span class="message-tool-call__toggle-title" title="${escHtml(title)}">${escHtml(title)}</span>
        </span>
        <span class="message-tool-call__toggle-meta">
          ${kind ? renderToolCallTagHTML(kind) : ''}
          ${status ? renderToolCallTagHTML(status, 'message-tool-call__tag--status') : ''}
          <span class="message-tool-call__chevron" aria-hidden="true">${iconChevronRight}</span>
        </span>
      </button>
      <div
        class="message-tool-call__panel"
        id="${escHtml(contentID)}"
        data-state="${state}"
        ${expanded ? '' : 'hidden'}
      >${renderToolCallCardHTML(toolCall)}</div>
    </div>`
}

function reasoningPanelState(expanded: boolean): 'open' | 'closed' {
  return expanded ? 'open' : 'closed'
}

function reasoningContentID(segmentID: string): string {
  return `reasoning-content-${segmentID}`
}

function renderReasoningSectionHTML(
  segmentID: string,
  reasoning: string | undefined,
  extraClass = '',
  expanded = false,
  renderMarkdownContent = false,
  label = 'Thinking',
): string {
  if (!hasReasoningText(reasoning)) return ''
  const state = reasoningPanelState(expanded)
  const contentID = reasoningContentID(segmentID)
  const contentClass = renderMarkdownContent
    ? 'message-reasoning__content message-reasoning__content--md'
    : 'message-reasoning__content'
  const contentHTML = renderMarkdownContent ? renderMarkdown(reasoning) : escHtml(reasoning)
  return `
    <div
      class="message-reasoning${extraClass}"
      data-segment-id="${escHtml(segmentID)}"
      data-state="${state}"
    >
      <button
        class="message-reasoning__toggle"
        type="button"
        data-segment-id="${escHtml(segmentID)}"
        data-state="${state}"
        aria-expanded="${expanded ? 'true' : 'false'}"
        aria-controls="${escHtml(contentID)}"
      >
        <span class="message-reasoning__icon" aria-hidden="true">${iconSparkles}</span>
        <span class="message-reasoning__header">${escHtml(label)}</span>
        <span class="message-reasoning__chevron" aria-hidden="true">${iconChevronRight}</span>
      </button>
      <div
        class="${contentClass}"
        id="${escHtml(contentID)}"
        data-state="${state}"
        ${expanded ? '' : 'hidden'}
      >${contentHTML}</div>
    </div>`
}

function renderMessageSegmentContentHTML(
  segment: MessageSegment,
  status: Message['status'],
  streaming: boolean,
  showTypingIndicator: boolean,
  timestamp: string,
): string {
  const content = segment.content ?? ''
  if (!streaming && !content) return ''

  let blockClass = 'message-answer'
  let blockContent = ''

  if (streaming) {
    blockClass += ' message-answer--streaming'
    blockContent = `<div class="message-answer__text">${escHtml(content)}</div>`
    if (showTypingIndicator) {
      blockContent += `<div class="typing-indicator" aria-hidden="true"><span></span><span></span><span></span></div>`
    }
  } else if (status === 'done' || status === 'streaming') {
    blockClass += ' message-answer--md'
    blockContent = renderMarkdown(content)
  } else if (status === 'cancelled') {
    blockClass += ' message-answer--cancelled'
    blockContent = escHtml(content)
  } else {
    blockContent = escHtml(content)
  }

  const metaHTML = !streaming && content
    ? `
      <div class="message-segment__meta">
        <span class="message-time">${formatTimestamp(timestamp)}</span>
        <button
          class="msg-copy-btn msg-copy-btn--segment"
          data-copy-text="${encodeURIComponent(content)}"
          type="button"
          title="Copy segment"
          aria-label="Copy segment"
        >⎘</button>
      </div>`
    : ''

  return `
    <div class="message-segment message-segment--content">
      <div class="${blockClass}">${blockContent}</div>
      ${metaHTML}
    </div>`
}

function renderMessageSegmentToolCallHTML(segment: MessageSegment, streaming: boolean): string {
  if (!segment.toolCall) return ''
  const isActiveStreamingSegment = streaming
  const isExpanded = isActiveStreamingSegment || expandedToolCallSegmentIds.has(segment.id)
  return `
    <div class="message-segment message-segment--tool">
      ${renderToolCallPanelHTML(segment.id, segment.toolCall, isExpanded, isActiveStreamingSegment)}
    </div>`
}

function renderMessageSegmentReasoningHTML(segment: MessageSegment, streaming: boolean): string {
  const isActiveStreamingSegment = streaming
  const isExpanded = isActiveStreamingSegment || expandedReasoningSegmentIds.has(segment.id)
  return `
    <div class="message-segment message-segment--reasoning">
      ${renderReasoningSectionHTML(
        segment.id,
        segment.content,
        isActiveStreamingSegment ? ' message-reasoning--streaming' : '',
        isExpanded,
        !isActiveStreamingSegment,
        'Thought',
      )}
    </div>`
}

function renderMessageSegmentsHTML(
  messageID: string,
  segments: MessageSegment[] | undefined,
  status: Message['status'],
  streaming = false,
  activeStreamingContentSegmentID: string | null = null,
  activeStreamingReasoningSegmentID: string | null = null,
  activeStreamingToolCallSegmentID: string | null = null,
  timestamp = '',
): string {
  const normalized = cloneMessageSegments(segments) ?? []
  const displaySegments = normalized.length || !streaming
    ? normalized
    : [{
        id: nextMessageSegmentID(messageID, 'content', 1),
        kind: 'content' as const,
        content: '',
      }]
  const showStreamingTail = streaming && !activeStreamingContentSegmentID

  const content = displaySegments.map((segment, index) => {
    switch (segment.kind) {
      case 'reasoning':
        return renderMessageSegmentReasoningHTML(
          segment,
          streaming && segment.id === activeStreamingReasoningSegmentID,
        )
      case 'tool_call':
        return renderMessageSegmentToolCallHTML(
          segment,
          streaming && segment.id === activeStreamingToolCallSegmentID,
        )
      case 'content':
        return renderMessageSegmentContentHTML(
          segment,
          status,
          streaming && segment.id === activeStreamingContentSegmentID,
          streaming && segment.id === activeStreamingContentSegmentID && index === displaySegments.length - 1,
          timestamp,
        )
      default:
        return ''
    }
  }).join('')

  const tail = showStreamingTail
    ? `
      <div class="message-segment message-segment--tail">
        <div class="message-stream-tail">
          <div class="typing-indicator" aria-hidden="true"><span></span><span></span><span></span></div>
        </div>
      </div>`
    : ''

  return `<div class="message-segments">${content}${tail}</div>`
}

function renderMessageStatusBubble(msg: Message): string {
  if (msg.status === 'error') {
    const bodyText = (msg.errorCode ? `[${msg.errorCode}] ` : '') + (msg.errorMessage ?? 'Unknown error')
    return `<div class="message-segment message-segment--status">
      <div class="message-bubble message-bubble--error">${escHtml(bodyText)}</div>
    </div>`
  }
  if (msg.status === 'cancelled' && !msg.content) {
    return `<div class="message-segment message-segment--status">
      <div class="message-bubble message-bubble--cancelled">…</div>
    </div>`
  }
  return ''
}

function renderMessage(msg: Message): string {
  const renderMessageCopyBtn = (text: string): string => `
    <button
      class="msg-copy-btn"
      data-copy-text="${escHtml(encodeURIComponent(text))}"
      title="Copy message"
      aria-label="Copy message"
      type="button"
    >⎘</button>`

  if (msg.role === 'user') {
    const copyBtn = renderMessageCopyBtn(msg.content)
    return `
      <div class="message message--user" data-msg-id="${escHtml(msg.id)}">
        <div class="message-group">
          <div class="message-prompt">${escHtml(msg.content)}</div>
          <div class="message-meta">
            <span class="message-time">${formatTimestamp(msg.timestamp)}</span>
            ${copyBtn}
          </div>
        </div>
      </div>`
  }

  const isCancelled = msg.status === 'cancelled'

  const planHTML = renderPlanSectionHTML(msg.planEntries)
  const segments = resolveMessageSegments(msg)
  const segmentsHTML = renderMessageSegmentsHTML(msg.id, segments, msg.status, false, null, null, null, msg.timestamp)
  const statusHTML = renderMessageStatusBubble(msg)
  const hasContent = messageHasContentSegment(segments)

  const stopTag  = isCancelled ? `<span class="message-stop-reason">Cancelled</span>` : ''
  const footerMeta = (!hasContent || stopTag)
    ? `
      <div class="message-meta">
        ${!hasContent ? `<span class="message-time">${formatTimestamp(msg.timestamp)}</span>` : ''}
        ${stopTag}
      </div>`
    : ''

  return `
    <div class="message message--agent" data-msg-id="${escHtml(msg.id)}">
      <div class="message-group">
        ${planHTML}
        ${segmentsHTML}
        ${statusHTML}
        ${footerMeta}
      </div>
    </div>`
}

function renderStreamingBubbleHTML(
  messageID: string,
  segments: MessageSegment[] | undefined,
  planEntries?: PlanEntry[],
  activeStreamingContentSegmentID: string | null = null,
  activeStreamingReasoningSegmentID: string | null = null,
  activeStreamingToolCallSegmentID: string | null = null,
): string {
  const normalizedPlanEntries = clonePlanEntries(planEntries)
  const planHiddenAttr = normalizedPlanEntries?.length ? '' : ' hidden'
  return `
    <div class="message-plan message-plan--streaming" id="plan-${escHtml(messageID)}"${planHiddenAttr}>${normalizedPlanEntries ? renderPlanInnerHTML(normalizedPlanEntries) : ''}</div>
    <div id="segments-${escHtml(messageID)}">${renderMessageSegmentsHTML(
      messageID,
      segments,
      'streaming',
      true,
      activeStreamingContentSegmentID,
      activeStreamingReasoningSegmentID,
      activeStreamingToolCallSegmentID,
      '',
    )}</div>`
}

function updateStreamingBubbleSegments(
  messageID: string,
  segments: MessageSegment[] | undefined,
  activeStreamingContentSegmentID: string | null,
  activeStreamingReasoningSegmentID: string | null,
  activeStreamingToolCallSegmentID: string | null,
): void {
  const segmentsEl = document.getElementById(`segments-${messageID}`)
  if (!segmentsEl) return
  segmentsEl.innerHTML = renderMessageSegmentsHTML(
    messageID,
    segments,
    'streaming',
    true,
    activeStreamingContentSegmentID,
    activeStreamingReasoningSegmentID,
    activeStreamingToolCallSegmentID,
    '',
  )
  bindMarkdownControls(segmentsEl)
  bindReasoningPanels(segmentsEl)
  bindToolCallPanels(segmentsEl)
}

function setReasoningPanelExpanded(panelEl: HTMLElement, expanded: boolean): void {
  const state = reasoningPanelState(expanded)
  panelEl.dataset.state = state

  const toggleEl = panelEl.querySelector<HTMLButtonElement>('.message-reasoning__toggle')
  if (toggleEl) {
    toggleEl.dataset.state = state
    toggleEl.setAttribute('aria-expanded', expanded ? 'true' : 'false')
  }

  const contentEl = panelEl.querySelector<HTMLElement>('.message-reasoning__content')
  if (contentEl) {
    contentEl.dataset.state = state
    contentEl.hidden = !expanded
  }
}

function bindReasoningPanels(listEl: HTMLElement): void {
  listEl.querySelectorAll<HTMLButtonElement>('.message-reasoning__toggle[data-segment-id]').forEach(toggleEl => {
    toggleEl.addEventListener('click', () => {
      const segmentID = toggleEl.dataset.segmentId?.trim() ?? ''
      const panelEl = toggleEl.closest<HTMLElement>('.message-reasoning')
      if (!segmentID || !panelEl || panelEl.classList.contains('message-reasoning--streaming')) return

      const nextExpanded = toggleEl.getAttribute('aria-expanded') !== 'true'
      if (nextExpanded) {
        expandedReasoningSegmentIds.add(segmentID)
      } else {
        expandedReasoningSegmentIds.delete(segmentID)
      }
      setReasoningPanelExpanded(panelEl, nextExpanded)
    })
  })
}

function setToolCallPanelExpanded(panelEl: HTMLElement, expanded: boolean): void {
  const state = reasoningPanelState(expanded)
  panelEl.dataset.state = state

  const toggleEl = panelEl.querySelector<HTMLButtonElement>('.message-tool-call__toggle')
  if (toggleEl) {
    toggleEl.dataset.state = state
    toggleEl.setAttribute('aria-expanded', expanded ? 'true' : 'false')
  }

  const contentEl = panelEl.querySelector<HTMLElement>('.message-tool-call__panel')
  if (contentEl) {
    contentEl.dataset.state = state
    contentEl.hidden = !expanded
    if (expanded) bindMarkdownControls(contentEl)
  }
}

function bindToolCallPanels(listEl: HTMLElement): void {
  listEl.querySelectorAll<HTMLButtonElement>('.message-tool-call__toggle[data-segment-id]').forEach(toggleEl => {
    toggleEl.addEventListener('click', () => {
      const segmentID = toggleEl.dataset.segmentId?.trim() ?? ''
      const panelEl = toggleEl.closest<HTMLElement>('.message-tool-call')
      if (!segmentID || !panelEl || panelEl.classList.contains('message-tool-call--streaming')) return

      const nextExpanded = toggleEl.getAttribute('aria-expanded') !== 'true'
      if (nextExpanded) {
        expandedToolCallSegmentIds.add(segmentID)
      } else {
        expandedToolCallSegmentIds.delete(segmentID)
      }
      setToolCallPanelExpanded(panelEl, nextExpanded)
    })
  })
}

function updateStreamingBubblePlan(messageID: string, entries: PlanEntry[] | undefined): void {
  const planEl = document.getElementById(`plan-${messageID}`)
  if (!planEl) return
  const normalized = clonePlanEntries(entries)
  planEl.hidden = !normalized?.length
  if (!normalized?.length) {
    planEl.innerHTML = ''
    return
  }
  planEl.innerHTML = renderPlanInnerHTML(normalized)
}

function updateMessageList(): void {
  const listEl = document.getElementById('message-list')
  if (!listEl) return

  const { activeThreadId, threads, messages } = store.get()
  if (!activeThreadId) return

  const thread   = threads.find(t => t.threadId === activeThreadId)
  const scopeKey = threadChatScopeKey(thread)
  const msgs     = messages[scopeKey] ?? []

  if (!msgs.length) {
    listEl.innerHTML = `
      <div class="empty-state">
        <div class="empty-state-icon" style="font-size:28px">💬</div>
        <h3 class="empty-state-title" style="font-size:var(--font-size-lg)">Start the conversation</h3>
        <p class="empty-state-desc">Send your first message to begin working with ${escHtml(thread?.agent ?? 'the agent')}.</p>
      </div>`
    return
  }

  listEl.innerHTML = msgs.map(m => renderMessage(m)).join('')
  bindMarkdownControls(listEl)
  bindReasoningPanels(listEl)
  bindToolCallPanels(listEl)
  listEl.scrollTop = listEl.scrollHeight
  // Sync scroll button (we just moved to the bottom)
  const scrollBtn = document.getElementById('scroll-bottom-btn')
  if (scrollBtn) scrollBtn.style.display = 'none'
}

// ── Input state ───────────────────────────────────────────────────────────

function updateInputState(): void {
  const { activeThreadId } = store.get()
  const streamState = getActiveChatStreamState()
  const isStreaming   = !!streamState
  const isCancelling  = streamState?.status === 'cancelling'

  const sendBtn  = document.getElementById('send-btn')   as HTMLButtonElement   | null
  const cancelBtn = document.getElementById('cancel-btn') as HTMLButtonElement   | null
  const inputEl  = document.getElementById('message-input') as HTMLTextAreaElement | null
  const isSwitchingConfig = !!activeThreadId && threadConfigSwitching.has(activeThreadId)
  const isSwitchingSession = !!activeThreadId && sessionSwitchingThreads.has(activeThreadId)
  const hasThreadStreaming = hasThreadStream(activeThreadId)

  if (sendBtn)  sendBtn.disabled  = isStreaming || isSwitchingConfig || isSwitchingSession
  if (inputEl)  inputEl.disabled  = isStreaming || isSwitchingConfig || isSwitchingSession
  document.querySelectorAll<HTMLButtonElement>('.thread-model-trigger').forEach(triggerEl => {
    const pickerState = triggerEl.dataset.state ?? 'empty'
    const configID = triggerEl.dataset.configId?.trim() ?? ''
    const noSelectableValue = pickerState !== 'ready' || !configID
    const disabled = hasThreadStreaming || isSwitchingConfig || isSwitchingSession || noSelectableValue
    triggerEl.disabled = disabled
    if (disabled) {
      triggerEl.setAttribute('aria-expanded', 'false')
      const menu = triggerEl.parentElement?.querySelector<HTMLElement>('.thread-model-menu')
      menu?.setAttribute('hidden', 'true')
    }
  })
  if (cancelBtn) {
    cancelBtn.style.display = isStreaming ? '' : 'none'
    cancelBtn.disabled      = isCancelling
    cancelBtn.textContent   = isCancelling ? 'Cancelling…' : 'Cancel'
  }
  updateSlashCommandMenu()
}

function closeSlashCommandMenu(): void {
  const menuEl = document.getElementById('slash-command-menu') as HTMLDivElement | null
  if (!menuEl) return
  slashCommandSelectedIndex = 0
  menuEl.hidden = true
  menuEl.innerHTML = ''
}

function resetSlashCommandLookup(): void {
  slashCommandLookupThreadId = null
}

function getFilteredSlashCommands(commands: SlashCommand[], query: string): SlashCommand[] {
  const normalizedQuery = query.trim().toLowerCase()
  if (!normalizedQuery) return cloneSlashCommands(commands)

  return cloneSlashCommands(commands).filter(command => {
    const name = command.name.toLowerCase()
    const description = command.description?.toLowerCase() ?? ''
    return name.includes(normalizedQuery) || description.includes(normalizedQuery)
  })
}

function updateSlashCommandMenu(): void {
  const menuEl = document.getElementById('slash-command-menu') as HTMLDivElement | null
  const inputEl = document.getElementById('message-input') as HTMLTextAreaElement | null
  if (!menuEl || !inputEl) return

  const { activeThreadId, threads } = store.get()
  if (!activeThreadId || inputEl.disabled) {
    resetSlashCommandLookup()
    closeSlashCommandMenu()
    return
  }

  const thread = threads.find(item => item.threadId === activeThreadId)
  if (!thread) {
    resetSlashCommandLookup()
    closeSlashCommandMenu()
    return
  }

  const rawValue = inputEl.value
  if (!rawValue.startsWith('/')) {
    resetSlashCommandLookup()
    closeSlashCommandMenu()
    return
  }

  const agentKey = normalizeAgentKey(thread.agent ?? '')
  const query = rawValue.slice(1)
  const hasCachedCommands = hasAgentSlashCommandsCache(thread.agent ?? '')
  const loading = !!agentKey && agentSlashCommandsInFlight.has(agentKey)
  const shouldRefreshForSlashEntry = rawValue === '/' && slashCommandLookupThreadId !== thread.threadId

  if (shouldRefreshForSlashEntry && !loading) {
    slashCommandLookupThreadId = thread.threadId
    void loadThreadSlashCommands(thread.threadId, true).then(() => {
      const activeInputEl = document.getElementById('message-input') as HTMLTextAreaElement | null
      if (store.get().activeThreadId === thread.threadId && activeInputEl?.value.startsWith('/')) {
        updateSlashCommandMenu()
      }
    })
    closeSlashCommandMenu()
    return
  }

  if (!hasCachedCommands && !loading) {
    slashCommandLookupThreadId = thread.threadId
    void loadThreadSlashCommands(thread.threadId).then(() => {
      const activeInputEl = document.getElementById('message-input') as HTMLTextAreaElement | null
      if (store.get().activeThreadId === thread.threadId && activeInputEl?.value.startsWith('/')) {
        updateSlashCommandMenu()
      }
    })
    closeSlashCommandMenu()
    return
  }

  if (!hasCachedCommands || loading) {
    closeSlashCommandMenu()
    return
  }

  const cachedCommands = getAgentSlashCommands(thread.agent ?? '')
  if (!cachedCommands.length) {
    closeSlashCommandMenu()
    return
  }

  const commands = getFilteredSlashCommands(cachedCommands, query)
  if (!loading && !commands.length) {
    slashCommandSelectedIndex = 0
    menuEl.hidden = false
    menuEl.innerHTML = `<div class="slash-command-empty">No matching slash commands.</div>`
    return
  }

  slashCommandSelectedIndex = Math.max(0, Math.min(slashCommandSelectedIndex, commands.length - 1))
  menuEl.hidden = false
  menuEl.innerHTML = `
    <div class="slash-command-header">Slash Commands</div>
    <div class="slash-command-list">
      ${commands.map((command, index) => renderSlashCommandMenuItem(command, index === slashCommandSelectedIndex)).join('')}
    </div>`
}

function selectSlashCommand(commandName: string): void {
  const inputEl = document.getElementById('message-input') as HTMLTextAreaElement | null
  const { activeThreadId, threads } = store.get()
  if (!inputEl || !activeThreadId) return

  const thread = threads.find(item => item.threadId === activeThreadId)
  if (!thread) return

  const commands = getFilteredSlashCommands(getAgentSlashCommands(thread.agent ?? ''), inputEl.value.slice(1))
  const command = commands.find(item => item.name === commandName)
  if (!command) return

  inputEl.value = `/${command.name}${command.inputHint ? ' ' : ''}`
  inputEl.focus()
  inputEl.setSelectionRange(inputEl.value.length, inputEl.value.length)
  inputEl.style.height = 'auto'
  inputEl.style.height = Math.min(inputEl.scrollHeight, 220) + 'px'
  resetSlashCommandLookup()
  closeSlashCommandMenu()
}

// ── Chat area rendering ───────────────────────────────────────────────────

function renderChatEmpty(): string {
  return `
    <div class="empty-state">
      <div class="empty-state-icon">◈</div>
      <h3 class="empty-state-title">No agent selected</h3>
      <p class="empty-state-desc">
        Select an agent from the sidebar, or create a new one to start chatting.
      </p>
      <button class="btn btn-primary" id="new-thread-empty-btn">
        ${iconPlus} New Agent
      </button>
    </div>`
}

function renderSessionInfoField(label: string, value: string, copyLabel: string): string {
  return `
    <div class="session-info-field">
      <div class="session-info-label">${label}</div>
      <div class="session-info-row">
        <div class="session-info-value" title="${escHtml(value)}">${escHtml(value)}</div>
        <button
          class="btn btn-icon session-info-copy-btn"
          type="button"
          data-copy-value="${escHtml(encodeURIComponent(value))}"
          aria-label="${copyLabel}"
          title="${copyLabel}"
        >
          ${iconCopy}
        </button>
      </div>
    </div>`
}

function renderSessionInfoPopover(thread: Thread): string {
  const sessionID = threadSessionID(thread)
  if (!sessionID) return ''

  return `
    <div class="session-info" id="session-info">
      <button
        class="btn btn-icon session-info-trigger"
        id="session-info-trigger"
        type="button"
        aria-label="Session info"
        aria-expanded="false"
        aria-controls="session-info-panel"
        title="Session info"
      >
        ${iconInfo}
      </button>
      <div class="session-info-popover" id="session-info-panel" role="dialog" aria-label="Session Info" hidden>
        <div class="session-info-heading">Session Info</div>
        ${renderSessionInfoField('Session ID', sessionID, 'Copy session ID')}
        ${renderSessionInfoField('Working Directory', thread.cwd, 'Copy working directory')}
      </div>
    </div>`
}

function renderSlashCommandMenuItem(command: SlashCommand, active: boolean): string {
  const inputHint = command.inputHint?.trim() ?? ''
  return `
    <button
      class="slash-command-item ${active ? 'slash-command-item--active' : ''}"
      type="button"
      data-command-name="${escHtml(command.name)}"
      aria-pressed="${active ? 'true' : 'false'}"
    >
      <span class="slash-command-item-icon" aria-hidden="true">${iconSlashCommand}</span>
      <div class="slash-command-item-copy">
        <div class="slash-command-item-main">
          <span class="slash-command-item-name">/${escHtml(command.name)}</span>
          ${inputHint ? `<span class="slash-command-item-hint">(${escHtml(inputHint)})</span>` : ''}
          ${command.description?.trim()
            ? `<span class="slash-command-item-desc">${escHtml(command.description)}</span>`
            : ''}
        </div>
      </div>
    </button>`
}

function renderChatThread(t: Thread): string {
  const titleLabel   = threadTitle(t)
  const createdLabel = t.createdAt ? `Created ${formatTimestamp(t.createdAt)}` : ''
  const selectedModelID = fallbackThreadModelID(t)
  const catalogKey = normalizeAgentConfigCatalogKey(t.agent ?? '', selectedModelID)
  const hasConfigCache = threadConfigCache.has(t.threadId) || hasAgentConfigCatalog(t.agent ?? '', selectedModelID)
  const loadingConfig = !hasConfigCache || (!!catalogKey && agentConfigCatalogInFlight.has(catalogKey))
  const configOptions = getThreadConfigOptionsForRender(t)
  const modelOption = findModelOption(configOptions)
  const reasoningOption = findReasoningOption(configOptions)
  const modelPickerData = resolveConfigPickerData(
    modelOption,
    fallbackThreadConfigValue(t, 'model'),
    loadingConfig,
    modelPickerLabels,
  )
  const reasoningPickerData = resolveConfigPickerData(
    reasoningOption,
    reasoningOption ? fallbackThreadConfigValue(t, reasoningOption.id) : '',
    loadingConfig,
    reasoningPickerLabels,
  )
  const showReasoningSwitch = shouldShowReasoningSwitch(reasoningOption)
  const isSwitching = threadConfigSwitching.has(t.threadId)

  return `
    <div class="chat-header">
      <div class="chat-header-left">
        <button class="btn btn-icon mobile-menu-btn" aria-label="Open menu">${iconMenu}</button>
        <div class="chat-header-main">
          <div class="chat-header-title-row">
            <h2 class="chat-title" title="${escHtml(titleLabel)}">${escHtml(titleLabel)}</h2>
            <span class="badge badge--agent">${escHtml(t.agent ?? '')}</span>
          </div>
        </div>
      </div>
      <div class="chat-header-right">
        <button class="btn btn-sm btn-danger" id="cancel-btn" style="display:none" aria-label="Cancel turn">Cancel</button>
        <span class="chat-header-meta">${escHtml(createdLabel)}</span>
        ${renderSessionInfoPopover(t)}
      </div>
    </div>

    <div class="message-list-wrap">
      <div class="message-list" id="message-list"></div>
      <button class="scroll-bottom-btn" id="scroll-bottom-btn"
              aria-label="Scroll to bottom" style="display:none">↓</button>
    </div>

    <div class="input-area">
      <div class="slash-command-menu" id="slash-command-menu" hidden></div>
      <div class="input-wrapper">
        <textarea
          id="message-input"
          class="message-input"
          placeholder="Type a message…"
          rows="1"
          aria-label="Message input"
        ></textarea>
        <div class="input-compose-bar">
          <div class="thread-config-switches">
            ${renderComposerConfigSwitch('model', 'Model', modelPickerData, modelPickerLabels, isSwitching)}
            ${showReasoningSwitch
              ? renderComposerConfigSwitch('reasoning', 'Reasoning', reasoningPickerData, reasoningPickerLabels, isSwitching)
              : ''}
          </div>
          <button class="btn btn-primary btn-send" id="send-btn" aria-label="Send message">
            ${iconSend}
          </button>
        </div>
      </div>
      <div class="input-hint">Press <kbd>⌘ Enter</kbd> to send · <kbd>Esc</kbd> to cancel · Type <kbd>/</kbd> for slash commands</div>
    </div>`
}

function updateChatArea(): void {
  const chat = document.getElementById('chat')
  if (!chat) return

  const { threads, activeThreadId } = store.get()
  const thread = activeThreadId ? threads.find(t => t.threadId === activeThreadId) : null

  // The streaming bubble is tied to the current chat DOM; reset sentinel on chat-scope switch.
  activeStreamMsgId = null
  activeStreamScopeKey = ''

  if (!thread) {
    chat.innerHTML = renderChatEmpty()
    document.getElementById('new-thread-empty-btn')?.addEventListener('click', openNewThread)
    document.querySelector('.mobile-menu-btn')?.addEventListener('click', () => {
      document.getElementById('sidebar')?.classList.toggle('sidebar--open')
    })
    return
  }

  chat.innerHTML = renderChatThread(thread)
  document.querySelector('.mobile-menu-btn')?.addEventListener('click', () => {
    document.getElementById('sidebar')?.classList.toggle('sidebar--open')
  })

  // Show locally loaded messages immediately (including empty threads).
  // Show the loading state when the cache belongs to a different selected session.
  const scopeKey = threadChatScopeKey(thread)
  const hasLocalHistory = Object.prototype.hasOwnProperty.call(store.get().messages, scopeKey)
  const hasMatchingLocalHistory = hasLocalHistory && loadedHistoryScopeKeys.has(scopeKey)
  if (hasMatchingLocalHistory) {
    updateMessageList()
  } else {
    const listEl = document.getElementById('message-list')
    if (listEl) {
      listEl.innerHTML = `<div class="message-list-loading"><div class="loading-spinner"></div></div>`
    }
  }

  appendOrRestoreStreamingBubble(thread)
  renderPendingPermissionCards(scopeKey)

  updateInputState()
  bindSessionInfoPopover()
  bindInputResize()
  bindSendHandler()
  bindCancelHandler()
  bindThreadConfigSwitches(thread)
  bindScrollBottom()

  // Always reload history from server (keeps view fresh; guards against overwrites during streaming)
  void loadHistory(thread.threadId)
}

function bindThreadConfigSwitches(thread: Thread): void {
  const switchEls = Array.from(document.querySelectorAll<HTMLElement>('.thread-model-switch[data-picker-key]'))
  if (!switchEls.length) return

  const closeMenu = (switchEl: HTMLElement): void => {
    const triggerEl = switchEl.querySelector<HTMLButtonElement>('.thread-model-trigger')
    const menuEl = switchEl.querySelector<HTMLElement>('.thread-model-menu')
    triggerEl?.setAttribute('aria-expanded', 'false')
    menuEl?.setAttribute('hidden', 'true')
  }

  const closeAllMenus = (): void => {
    switchEls.forEach(closeMenu)
  }

  const renderConfigUI = (): void => {
    const latest = store.get().threads.find(item => item.threadId === thread.threadId)
    if (!latest) return

    const selectedModelID = fallbackThreadModelID(latest)
    const catalogKey = normalizeAgentConfigCatalogKey(latest.agent ?? '', selectedModelID)
    const loading = (!threadConfigCache.has(thread.threadId) && !hasAgentConfigCatalog(latest.agent ?? '', selectedModelID))
      || (!!catalogKey && agentConfigCatalogInFlight.has(catalogKey))
    const options = getThreadConfigOptionsForRender(latest)
    const modelOption = findModelOption(options)
    const reasoningOption = findReasoningOption(options)
    const pickerDataByKey = {
      model: resolveConfigPickerData(modelOption, fallbackThreadConfigValue(latest, 'model'), loading, modelPickerLabels),
      reasoning: resolveConfigPickerData(
        reasoningOption,
        reasoningOption ? fallbackThreadConfigValue(latest, reasoningOption.id) : '',
        loading,
        reasoningPickerLabels,
      ),
    } as const
    const labelsByKey = {
      model: modelPickerLabels,
      reasoning: reasoningPickerLabels,
    } as const
    const visibleByKey = {
      model: true,
      reasoning: shouldShowReasoningSwitch(reasoningOption),
    } as const

    switchEls.forEach(switchEl => {
      const key = (switchEl.dataset.pickerKey === 'reasoning' ? 'reasoning' : 'model')
      const triggerEl = switchEl.querySelector<HTMLButtonElement>('.thread-model-trigger')
      const menuEl = switchEl.querySelector<HTMLDivElement>('.thread-model-menu')
      if (!triggerEl || !menuEl) return

      switchEl.hidden = !visibleByKey[key]
      if (switchEl.hidden) {
        closeMenu(switchEl)
        return
      }

      const pickerData = pickerDataByKey[key]
      const labels = labelsByKey[key]
      const isReady = pickerData.state === 'ready'
      const disabled = loading || threadConfigSwitching.has(thread.threadId) || !isReady || !pickerData.configId

      triggerEl.dataset.state = pickerData.state
      triggerEl.dataset.selectedValue = pickerData.selectedValue
      triggerEl.dataset.configId = pickerData.configId
      triggerEl.disabled = disabled
      const valueEl = triggerEl.querySelector<HTMLElement>('.thread-model-trigger-value')
      if (valueEl) valueEl.textContent = pickerData.selectedLabel
      menuEl.innerHTML = renderConfigMenuOptions(pickerData.options, pickerData.selectedValue, pickerData.state, labels)
      if (!isReady || disabled) {
        closeMenu(switchEl)
      }
    })
  }

  const setSwitching = (switching: boolean): void => {
    if (switching) {
      threadConfigSwitching.add(thread.threadId)
      closeAllMenus()
    } else {
      threadConfigSwitching.delete(thread.threadId)
    }
    if (store.get().activeThreadId === thread.threadId) {
      updateInputState()
    }
  }

  const switchConfig = async (configId: string, nextValue: string): Promise<void> => {
    const activeThreadID = store.get().activeThreadId
    if (!activeThreadID || activeThreadID !== thread.threadId) return
    if (hasThreadStream(activeThreadID)) return

    const latest = store.get().threads.find(item => item.threadId === activeThreadID)
    if (!latest) return

    configId = configId.trim()
    nextValue = nextValue.trim()
    if (!configId || !nextValue) return

    const currentOption = getThreadConfigOptionsForRender(latest).find(option => option.id === configId)
    const currentValue = currentOption?.currentValue?.trim()
      || fallbackThreadConfigValue(latest, configId)
    if (nextValue === currentValue) return

    setSwitching(true)
    try {
      const updatedOptions = await api.setThreadConfigOption(activeThreadID, {
        configId,
        value: nextValue,
      })
      const nextModelID = findModelOption(updatedOptions)?.currentValue?.trim() ?? fallbackThreadModelID(latest)
      const normalized = cacheThreadConfigOptions(latest, updatedOptions, nextModelID)
      const { threads } = store.get()
      store.set({
        threads: threads.map(item => (
          item.threadId === activeThreadID
            ? { ...item, agentOptions: buildThreadAgentOptions(item.agentOptions, normalized) }
            : item
        )),
      })
      renderConfigUI()
    } catch (err) {
      renderConfigUI()
      const message = err instanceof Error ? err.message : String(err)
      const targetLabel = configId.toLowerCase() === 'model' ? 'model' : 'config option'
      window.alert(`Failed to update ${targetLabel}: ${message}`)
    } finally {
      setSwitching(false)
      renderConfigUI()
    }
  }

  renderConfigUI()
  if (!threadConfigCache.has(thread.threadId) && !hasAgentConfigCatalog(thread.agent ?? '', fallbackThreadModelID(thread))) {
    void loadThreadConfigOptions(thread.threadId)
      .then(() => {
        if (store.get().activeThreadId !== thread.threadId) return
        renderConfigUI()
        updateInputState()
      })
      .catch(err => {
        if (store.get().activeThreadId !== thread.threadId) return
        renderConfigUI()
        const message = err instanceof Error ? err.message : String(err)
        window.alert(`Failed to load agent config options: ${message}`)
      })
  }

  switchEls.forEach(switchEl => {
    const triggerEl = switchEl.querySelector<HTMLButtonElement>('.thread-model-trigger')
    const menuEl = switchEl.querySelector<HTMLDivElement>('.thread-model-menu')
    if (!triggerEl || !menuEl) return

    const toggleMenu = (): void => {
      const expanded = triggerEl.getAttribute('aria-expanded') === 'true'
      if (expanded) {
        closeMenu(switchEl)
        return
      }
      if (triggerEl.disabled) return
      closeAllMenus()
      triggerEl.setAttribute('aria-expanded', 'true')
      menuEl.removeAttribute('hidden')
    }

    triggerEl.addEventListener('click', e => {
      e.preventDefault()
      toggleMenu()
    })

    menuEl.addEventListener('click', e => {
      const target = e.target as HTMLElement | null
      const optionBtn = target?.closest('.thread-model-option-item[data-value]') as HTMLButtonElement | null
      if (!optionBtn || optionBtn.disabled) return
      const configId = triggerEl.dataset.configId?.trim() ?? ''
      const nextValue = optionBtn.dataset.value?.trim() ?? ''
      closeMenu(switchEl)
      void switchConfig(configId, nextValue)
    })

    switchEl.addEventListener('focusout', e => {
      const related = e.relatedTarget as Node | null
      if (!related || !switchEl.contains(related)) {
        closeMenu(switchEl)
      }
    })

    const onEsc = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.preventDefault()
        closeMenu(switchEl)
        triggerEl.focus()
      }
    }
    triggerEl.addEventListener('keydown', onEsc)
    menuEl.addEventListener('keydown', onEsc)
  })
}

function closeSessionInfoPopover(): void {
  const root = document.getElementById('session-info')
  const trigger = document.getElementById('session-info-trigger') as HTMLButtonElement | null
  const panel = document.getElementById('session-info-panel') as HTMLDivElement | null
  if (!root || !trigger || !panel) return

  root.classList.remove('session-info--open')
  trigger.setAttribute('aria-expanded', 'false')
  panel.hidden = true
}

function bindSessionInfoPopover(): void {
  const root = document.getElementById('session-info')
  const trigger = document.getElementById('session-info-trigger') as HTMLButtonElement | null
  const panel = document.getElementById('session-info-panel') as HTMLDivElement | null
  if (!root || !trigger || !panel) return

  const setOpen = (open: boolean): void => {
    root.classList.toggle('session-info--open', open)
    trigger.setAttribute('aria-expanded', open ? 'true' : 'false')
    panel.hidden = !open
  }

  trigger.addEventListener('click', e => {
    e.preventDefault()
    e.stopPropagation()
    setOpen(panel.hidden)
  })

  panel.addEventListener('click', e => e.stopPropagation())

  root.querySelectorAll<HTMLButtonElement>('.session-info-copy-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.preventDefault()
      e.stopPropagation()

      const encoded = btn.dataset.copyValue ?? ''
      const value = encoded ? decodeURIComponent(encoded) : ''
      if (!value) return

      void copyText(value).then(copied => {
        if (!copied) return
        btn.innerHTML = iconCheck
        btn.classList.add('session-info-copy-btn--copied')
        setTimeout(() => {
          btn.innerHTML = iconCopy
          btn.classList.remove('session-info-copy-btn--copied')
        }, 1_500)
      })
    })
  })
}

// ── Scroll-to-bottom button ───────────────────────────────────────────────

function bindScrollBottom(): void {
  const listEl = document.getElementById('message-list')
  const btnEl  = document.getElementById('scroll-bottom-btn') as HTMLButtonElement | null
  if (!listEl || !btnEl) return

  const syncBtn = () => {
    btnEl.style.display = isNearBottom(listEl) ? 'none' : ''
  }

  listEl.addEventListener('scroll', syncBtn, { passive: true })
  btnEl.addEventListener('click', () => {
    listEl.scrollTo({ top: listEl.scrollHeight, behavior: 'smooth' })
  })
}

// ── Input resize ──────────────────────────────────────────────────────────

function bindInputResize(): void {
  const input = document.getElementById('message-input') as HTMLTextAreaElement | null
  const menuEl = document.getElementById('slash-command-menu') as HTMLDivElement | null
  if (!input) return
  const maxHeight = 220
  input.addEventListener('input', () => {
    input.style.height = 'auto'
    input.style.height = Math.min(input.scrollHeight, maxHeight) + 'px'
    updateSlashCommandMenu()
  })
  input.addEventListener('keydown', e => {
    const menuVisible = !!menuEl && !menuEl.hidden
    if (menuVisible) {
      const { activeThreadId, threads } = store.get()
      const thread = activeThreadId ? threads.find(item => item.threadId === activeThreadId) : null
      const commands = thread ? getFilteredSlashCommands(getAgentSlashCommands(thread.agent ?? ''), input.value.slice(1)) : []
      if (e.key === 'ArrowDown' && commands.length) {
        e.preventDefault()
        slashCommandSelectedIndex = (slashCommandSelectedIndex + 1) % commands.length
        updateSlashCommandMenu()
        return
      }
      if (e.key === 'ArrowUp' && commands.length) {
        e.preventDefault()
        slashCommandSelectedIndex = (slashCommandSelectedIndex - 1 + commands.length) % commands.length
        updateSlashCommandMenu()
        return
      }
      if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey && commands.length) {
        e.preventDefault()
        selectSlashCommand(commands[slashCommandSelectedIndex]?.name ?? '')
        return
      }
      if (e.key === 'Escape') {
        e.preventDefault()
        resetSlashCommandLookup()
        closeSlashCommandMenu()
        return
      }
    }
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      document.getElementById('send-btn')?.click()
    }
  })

  menuEl?.addEventListener('mousedown', e => e.preventDefault())
  menuEl?.addEventListener('click', e => {
    const target = e.target as HTMLElement | null
    const item = target?.closest('.slash-command-item[data-command-name]') as HTMLButtonElement | null
    const commandName = item?.dataset.commandName?.trim() ?? ''
    if (!commandName) return
    selectSlashCommand(commandName)
  })
  menuEl?.addEventListener('mousemove', e => {
    const target = e.target as HTMLElement | null
    const item = target?.closest('.slash-command-item[data-command-name]') as HTMLButtonElement | null
    if (!item) return
    const all = Array.from(menuEl.querySelectorAll<HTMLButtonElement>('.slash-command-item[data-command-name]'))
    const index = all.indexOf(item)
    if (index < 0 || index === slashCommandSelectedIndex) return
    slashCommandSelectedIndex = index
    updateSlashCommandMenu()
  })
}

// ── Send ──────────────────────────────────────────────────────────────────

function bindSendHandler(): void {
  document.getElementById('send-btn')?.addEventListener('click', handleSend)
}

function handleSend(): void {
  const inputEl = document.getElementById('message-input') as HTMLTextAreaElement | null
  if (!inputEl) return

  const text = inputEl.value.trim()
  if (!text) return

  const { activeThreadId, threads } = store.get()
  if (!activeThreadId) return

  const thread = threads.find(t => t.threadId === activeThreadId)
  if (!thread || sessionSwitchingThreads.has(thread.threadId)) return
  const capturedThreadID = activeThreadId
  let capturedSessionID = threadSessionID(thread)
  let capturedScopeKey = threadChatScopeKey(thread)
  if (getScopeStreamState(capturedScopeKey)) return

  // Clear input immediately
  inputEl.value = ''
  inputEl.style.height = 'auto'
  resetSlashCommandLookup()
  closeSlashCommandMenu()

  const now = new Date().toISOString()

  // ── 1. Add user message (fires subscribe → updateMessageList renders it) ──
  const userMsg: Message = {
    id:        generateUUID(),
    role:      'user',
    content:   text,
    timestamp: now,
    status:    'done',
  }
  addMessageToStore(capturedScopeKey, userMsg)

  // ── 2. Reserve streaming message ID before touching stream state ───────────
  //    This prevents subscribe → updateMessageList from wiping the bubble.
  const agentMsgID = generateUUID()
  activeStreamMsgId = agentMsgID
  activeStreamScopeKey = capturedScopeKey
  streamBufferByScope.set(capturedScopeKey, '')
  streamPlanByScope.delete(capturedScopeKey)
  streamSegmentsByScope.set(capturedScopeKey, [])
  activeContentSegmentIdByScope.delete(capturedScopeKey)
  activeReasoningSegmentIdByScope.delete(capturedScopeKey)
  activeToolCallSegmentIdByScope.delete(capturedScopeKey)
  streamStartedAtByScope.set(capturedScopeKey, now)
  setScopeStreamState(capturedScopeKey, {
    turnId: '',
    threadId: capturedThreadID,
    sessionId: capturedSessionID,
    messageId: agentMsgID,
    status: 'streaming',
  })

  // ── 4. Append streaming bubble directly to DOM ─────────────────────────────
  const listEl = document.getElementById('message-list')
  if (listEl) {
    listEl.querySelector('.empty-state')?.remove()
    const div = document.createElement('div')
    div.className        = 'message message--agent'
    div.dataset.msgId    = agentMsgID
    div.innerHTML = `
      <div class="message-group">
        ${renderStreamingBubbleHTML(
          agentMsgID,
          streamSegmentsByScope.get(capturedScopeKey),
          undefined,
          activeContentSegmentID(capturedScopeKey),
          activeReasoningSegmentID(capturedScopeKey),
          activeToolCallSegmentID(capturedScopeKey),
        )}
        <div class="message-meta">
          <span class="message-time">${formatTimestamp(now)}</span>
        </div>
      </div>`
    listEl.appendChild(div)
    listEl.scrollTop = listEl.scrollHeight
  }

  // ── 5. Start SSE stream ────────────────────────────────────────────────────
  const stream = api.startTurn(capturedThreadID, text, {

    onTurnStarted({ turnId }) {
      const state = getScopeStreamState(capturedScopeKey)
      if (!state) return
      setScopeStreamState(capturedScopeKey, { ...state, turnId })
    },

    onDelta({ delta }) {
      setActiveContentSegmentID(capturedScopeKey, null)
      setActiveReasoningSegmentID(capturedScopeKey, null)
      setActiveToolCallSegmentID(capturedScopeKey, null)
      const previous = streamBufferByScope.get(capturedScopeKey) ?? ''
      const next = previous + delta
      streamBufferByScope.set(capturedScopeKey, next)
      const nextSegments = appendTextSegment(streamSegmentsByScope.get(capturedScopeKey), 'content', delta, agentMsgID)
      streamSegmentsByScope.set(capturedScopeKey, nextSegments)
      const activeSegmentID = nextSegments[nextSegments.length - 1]?.kind === 'content'
        ? nextSegments[nextSegments.length - 1].id
        : null
      setActiveContentSegmentID(capturedScopeKey, activeSegmentID)

      if (activeChatScopeKey() !== capturedScopeKey) return
      const list      = document.getElementById('message-list')
      const atBottom  = !list || isNearBottom(list)
      updateStreamingBubbleSegments(
        agentMsgID,
        nextSegments,
        activeContentSegmentID(capturedScopeKey),
        activeReasoningSegmentID(capturedScopeKey),
        activeToolCallSegmentID(capturedScopeKey),
      )
      if (atBottom && list) list.scrollTop = list.scrollHeight
    },

    onReasoningDelta({ delta }: ReasoningDeltaPayload) {
      setActiveContentSegmentID(capturedScopeKey, null)
      setActiveToolCallSegmentID(capturedScopeKey, null)
      const nextSegments = appendTextSegment(streamSegmentsByScope.get(capturedScopeKey), 'reasoning', delta, agentMsgID)
      streamSegmentsByScope.set(capturedScopeKey, nextSegments)
      const activeSegmentID = nextSegments[nextSegments.length - 1]?.kind === 'reasoning'
        ? nextSegments[nextSegments.length - 1].id
        : null
      setActiveReasoningSegmentID(capturedScopeKey, activeSegmentID)

      if (activeChatScopeKey() !== capturedScopeKey) return
      const list = document.getElementById('message-list')
      const atBottom = !list || isNearBottom(list)
      updateStreamingBubbleSegments(
        agentMsgID,
        nextSegments,
        activeContentSegmentID(capturedScopeKey),
        activeReasoningSegmentID(capturedScopeKey),
        activeToolCallSegmentID(capturedScopeKey),
      )
      if (atBottom && list) list.scrollTop = list.scrollHeight
    },

    onPlanUpdate({ entries }: PlanUpdatePayload) {
      setActiveContentSegmentID(capturedScopeKey, null)
      setActiveReasoningSegmentID(capturedScopeKey, null)
      setActiveToolCallSegmentID(capturedScopeKey, null)
      const nextPlanEntries = clonePlanEntries(entries) ?? []
      streamPlanByScope.set(capturedScopeKey, nextPlanEntries)

      if (activeChatScopeKey() !== capturedScopeKey) return
      const list = document.getElementById('message-list')
      const atBottom = !list || isNearBottom(list)
      updateStreamingBubblePlan(agentMsgID, nextPlanEntries)
      updateStreamingBubbleSegments(
        agentMsgID,
        streamSegmentsByScope.get(capturedScopeKey),
        activeContentSegmentID(capturedScopeKey),
        activeReasoningSegmentID(capturedScopeKey),
        activeToolCallSegmentID(capturedScopeKey),
      )
      if (atBottom && list) list.scrollTop = list.scrollHeight
    },

    onToolCall(event: ToolCallPayload) {
      setActiveContentSegmentID(capturedScopeKey, null)
      setActiveReasoningSegmentID(capturedScopeKey, null)
      const nextSegments = applyToolCallSegmentEvent(
        streamSegmentsByScope.get(capturedScopeKey),
        event as unknown as Record<string, unknown>,
        agentMsgID,
      )
      streamSegmentsByScope.set(capturedScopeKey, nextSegments)
      setActiveToolCallSegmentID(
        capturedScopeKey,
        findToolCallSegmentID(nextSegments, event.toolCallId),
      )

      if (activeChatScopeKey() !== capturedScopeKey) return
      const list = document.getElementById('message-list')
      const atBottom = !list || isNearBottom(list)
      updateStreamingBubbleSegments(
        agentMsgID,
        nextSegments,
        activeContentSegmentID(capturedScopeKey),
        activeReasoningSegmentID(capturedScopeKey),
        activeToolCallSegmentID(capturedScopeKey),
      )
      if (atBottom && list) list.scrollTop = list.scrollHeight
    },

    onToolCallUpdate(event: ToolCallPayload) {
      setActiveContentSegmentID(capturedScopeKey, null)
      setActiveReasoningSegmentID(capturedScopeKey, null)
      const nextSegments = applyToolCallSegmentEvent(
        streamSegmentsByScope.get(capturedScopeKey),
        event as unknown as Record<string, unknown>,
        agentMsgID,
      )
      streamSegmentsByScope.set(capturedScopeKey, nextSegments)
      setActiveToolCallSegmentID(
        capturedScopeKey,
        findToolCallSegmentID(nextSegments, event.toolCallId),
      )

      if (activeChatScopeKey() !== capturedScopeKey) return
      const list = document.getElementById('message-list')
      const atBottom = !list || isNearBottom(list)
      updateStreamingBubbleSegments(
        agentMsgID,
        nextSegments,
        activeContentSegmentID(capturedScopeKey),
        activeReasoningSegmentID(capturedScopeKey),
        activeToolCallSegmentID(capturedScopeKey),
      )
      if (atBottom && list) list.scrollTop = list.scrollHeight
    },

    onSessionBound({ sessionId }: SessionBoundPayload) {
      const nextSessionID = sessionId.trim()
      if (!nextSessionID || nextSessionID === capturedSessionID) return
      const previousScopeKey = capturedScopeKey
      capturedSessionID = nextSessionID
      capturedScopeKey = threadSessionScopeKey(capturedThreadID, capturedSessionID)
      rebindScopeRuntime(previousScopeKey, capturedScopeKey, capturedSessionID)
      updateThreadSessionID(capturedThreadID, sessionId)
    },

    onPermissionRequired(event) {
      const pending = upsertPendingPermission(capturedScopeKey, event)
      mountPendingPermissionCard(capturedScopeKey, pending)
    },

    onCompleted({ stopReason }) {
      // Clear stream tracking BEFORE addMessageToStore (so subscribe calls updateMessageList)
      const finalPlanEntries = clonePlanEntries(streamPlanByScope.get(capturedScopeKey))
      const finalSegments = cloneMessageSegments(streamSegmentsByScope.get(capturedScopeKey))
      const finalContent = messageSegmentsContent(finalSegments) || (streamBufferByScope.get(capturedScopeKey) ?? '')
      const finalToolCalls = messageSegmentsToolCalls(finalSegments)
      const finalReasoning = messageSegmentsReasoning(finalSegments)
      clearScopeStreamRuntime(capturedScopeKey)
      clearPendingPermissions(capturedScopeKey)
      markThreadCompletionBadge(capturedThreadID)
      void loadThreadSessions(capturedThreadID)
      void loadThreadSlashCommands(capturedThreadID, true)

      addMessageToStore(capturedScopeKey, {
        id:         agentMsgID,
        role:       'agent',
        content:    finalContent,
        timestamp:  now,
        status:     stopReason === 'cancelled' ? 'cancelled' : 'done',
        stopReason,
        segments: finalSegments,
        planEntries: finalPlanEntries,
        toolCalls: finalToolCalls,
        reasoning: hasReasoningText(finalReasoning) ? finalReasoning : undefined,
      })
    },

    onError({ code, message: msg }) {
      const finalPlanEntries = clonePlanEntries(streamPlanByScope.get(capturedScopeKey))
      const finalSegments = cloneMessageSegments(streamSegmentsByScope.get(capturedScopeKey))
      const partialContent = messageSegmentsContent(finalSegments) || (streamBufferByScope.get(capturedScopeKey) ?? '')
      const finalToolCalls = messageSegmentsToolCalls(finalSegments)
      const finalReasoning = messageSegmentsReasoning(finalSegments)
      clearScopeStreamRuntime(capturedScopeKey)
      clearPendingPermissions(capturedScopeKey)
      void loadThreadSessions(capturedThreadID)
      void loadThreadSlashCommands(capturedThreadID, true)

      addMessageToStore(capturedScopeKey, {
        id:           agentMsgID,
        role:         'agent',
        content:      partialContent,
        timestamp:    now,
        status:       'error',
        errorCode:    code,
        errorMessage: msg,
        segments:     finalSegments,
        planEntries:  finalPlanEntries,
        toolCalls:    finalToolCalls,
        reasoning:    hasReasoningText(finalReasoning) ? finalReasoning : undefined,
      })
    },

    onDisconnect() {
      const finalPlanEntries = clonePlanEntries(streamPlanByScope.get(capturedScopeKey))
      const finalSegments = cloneMessageSegments(streamSegmentsByScope.get(capturedScopeKey))
      const partialContent = messageSegmentsContent(finalSegments) || (streamBufferByScope.get(capturedScopeKey) ?? '')
      const finalToolCalls = messageSegmentsToolCalls(finalSegments)
      const finalReasoning = messageSegmentsReasoning(finalSegments)
      clearScopeStreamRuntime(capturedScopeKey)
      clearPendingPermissions(capturedScopeKey)
      void loadThreadSessions(capturedThreadID)
      void loadThreadSlashCommands(capturedThreadID, true)

      addMessageToStore(capturedScopeKey, {
        id:           agentMsgID,
        role:         'agent',
        content:      partialContent,
        timestamp:    now,
        status:       'error',
        errorMessage: 'Connection lost',
        segments:     finalSegments,
        planEntries:  finalPlanEntries,
        toolCalls:    finalToolCalls,
        reasoning:    hasReasoningText(finalReasoning) ? finalReasoning : undefined,
      })
    },
  })

  streamsByScope.set(capturedScopeKey, stream)
}

// ── Cancel ────────────────────────────────────────────────────────────────

function bindCancelHandler(): void {
  document.getElementById('cancel-btn')?.addEventListener('click', () => void handleCancel())
}

async function handleCancel(): Promise<void> {
  const scopeKey = activeChatScopeKey()
  const streamState = getActiveChatStreamState()
  if (!scopeKey || !streamState?.turnId) return

  setScopeStreamState(scopeKey, { ...streamState, status: 'cancelling' })
  try {
    await api.cancelTurn(streamState.turnId)
  } catch {
    // Ignore — stream will eventually deliver turn_completed with stopReason=cancelled
  }
}

// ── New thread ────────────────────────────────────────────────────────────

function openNewThread(): void {
  newThreadModal.open()
}

// ── Static layout shell ───────────────────────────────────────────────────

function renderShell(): void {
  const root = document.getElementById('app')
  if (!root) return
  const { activeThreadId, threads } = store.get()
  const activeThread = activeThreadId ? threads.find(thread => thread.threadId === activeThreadId) ?? null : null

  root.innerHTML = `
    <div class="layout">
      <aside class="sidebar" id="sidebar">
        <div class="sidebar-header">
          <div class="sidebar-brand">
            <div class="sidebar-brand-icon">N</div>
            <span class="sidebar-brand-text">Ngent</span>
          </div>
        </div>

        <div class="thread-list" id="thread-list">
          ${skeletonItems()}
        </div>

        <div class="sidebar-primary-action">
          <button class="btn btn-primary sidebar-new-btn" id="new-thread-btn" title="New agent" aria-label="New agent">
            ${iconPlus}
            <span class="btn-label">New agent</span>
          </button>
        </div>

        <div class="sidebar-footer">
          <button class="btn btn-ghost sidebar-settings-btn" id="settings-btn">
            ${iconSettings}
            <span class="btn-label">Settings</span>
          </button>
        </div>

        <div class="thread-action-layer" id="thread-action-layer" hidden></div>
      </aside>

      <aside class="session-sidebar" id="session-sidebar" ${activeThread ? '' : 'hidden'}>
        ${activeThread ? renderSessionPanel() : ''}
      </aside>

      <main class="chat" id="chat">
        ${renderChatEmpty()}
      </main>
    </div>`

  document.getElementById('settings-btn')?.addEventListener('click', () => settingsPanel.open())
  document.getElementById('new-thread-btn')?.addEventListener('click', openNewThread)
  document.getElementById('new-thread-empty-btn')?.addEventListener('click', openNewThread)

  syncSidebarChrome()
}

// ── Global keyboard shortcuts ─────────────────────────────────────────────

function bindGlobalShortcuts(): void {
  document.addEventListener('keydown', e => {
    // Cmd+N / Ctrl+N — open new thread modal
    if (e.key === 'n' && (e.metaKey || e.ctrlKey) && !e.shiftKey) {
      e.preventDefault()
      openNewThread()
      return
    }

    // Escape — contextual (most-specific first)
    if (e.key === 'Escape') {
      // (1) close mobile sidebar if open
      const sidebar = document.getElementById('sidebar')
      if (sidebar?.classList.contains('sidebar--open')) {
        sidebar.classList.remove('sidebar--open')
        return
      }
      // (2) close thread action menu if open
      if (openThreadActionMenuId) {
        e.preventDefault()
        resetThreadActionMenuState()
        updateThreadList()
        return
      }
      // (3) close slash command menu if open
      const slashCommandMenu = document.getElementById('slash-command-menu') as HTMLDivElement | null
      if (slashCommandMenu && !slashCommandMenu.hidden) {
        e.preventDefault()
        closeSlashCommandMenu()
        return
      }
      // (4) close session info popover if open
      const sessionInfoPanel = document.getElementById('session-info-panel')
      if (sessionInfoPanel && !sessionInfoPanel.hidden) {
        e.preventDefault()
        closeSessionInfoPopover()
        return
      }
      // (5) cancel active stream
      const streamState = getActiveChatStreamState()
      if (streamState?.turnId) {
        void handleCancel()
      }
    }
  })
}

// ── Bootstrap ─────────────────────────────────────────────────────────────

async function init(): Promise<void> {
  renderShell()
  bindGlobalShortcuts()
  const repositionThreadActionLayer = (): void => {
    if (!openThreadActionMenuId) return
    renderThreadActionLayer()
  }
  document.getElementById('thread-list')?.addEventListener('scroll', repositionThreadActionLayer, { passive: true })
  window.addEventListener('resize', repositionThreadActionLayer)
  document.addEventListener('click', e => {
    const target = e.target as HTMLElement | null
    if (!target?.closest('.input-area')) {
      resetSlashCommandLookup()
      closeSlashCommandMenu()
    }
    if (!target?.closest('.session-info')) {
      closeSessionInfoPopover()
    }

    if (!openThreadActionMenuId) return
    if (target?.closest('.thread-item-menu-trigger') || target?.closest('.thread-action-popover')) return
    resetThreadActionMenuState()
    updateThreadList()
  })

  store.subscribe(() => {
    const { activeThreadId, threads } = store.get()
    const activeThread = activeThreadId ? threads.find(thread => thread.threadId === activeThreadId) ?? null : null
    const threadChanged = activeThreadId !== lastRenderThreadId
    const chatScopeKey = threadChatScopeKey(activeThread)
    const chatScopeChanged = chatScopeKey !== lastRenderChatScopeKey
    const chatScopeStreamState = getScopeStreamState(chatScopeKey)
    const shouldRefreshForScopeChange = chatScopeChanged && (!chatScopeStreamState || !hasMountedActiveStream(chatScopeKey))

    updateThreadList()
    updateSessionPanel()

    if (threadChanged || shouldRefreshForScopeChange) {
      lastRenderThreadId = activeThreadId
      lastRenderChatScopeKey = chatScopeKey
      updateChatArea()
    } else {
      if (chatScopeChanged && hasMountedActiveStream(chatScopeKey)) {
        // A fresh session can become bound to its stable session id right before
        // the turn completes. Keep tracking the new scope even though we reuse
        // the existing streaming DOM, otherwise completion will look like a
        // later scope switch and trigger an unnecessary history reload that can
        // overwrite the finalized reasoning.
        lastRenderChatScopeKey = chatScopeKey
      }
      // activeStreamMsgId is non-null while the streaming bubble is in the DOM.
      // Re-rendering the message list would destroy that bubble, so we skip it.
      if (!activeStreamMsgId) updateMessageList()
      updateInputState()
    }
  })

  try {
    const [agents, threads] = await Promise.all([
      api.getAgents(),
      api.getThreads(),
    ])
    store.set({ agents, threads })
  } catch {
    const el = document.getElementById('thread-list')
    if (el) {
      el.innerHTML = `<div class="thread-list-empty" style="color:var(--error)">
        Failed to load agents.<br>Check the server connection in Settings.
      </div>`
    }
  }
}

void init()
