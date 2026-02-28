// ── Theme & Settings ───────────────────────────────────────────────────────

export type Theme = 'light' | 'dark' | 'system'

// ── API models (mirrors server JSON contracts) ─────────────────────────────

export interface AgentInfo {
  id: string
  name: string
  status: 'available' | 'unavailable'
}

export interface Thread {
  threadId: string
  agent: string
  cwd: string
  title: string
  agentOptions: Record<string, unknown>
  summary: string
  createdAt: string
  updatedAt: string
}

export interface TurnEvent {
  eventId: number
  seq: number
  type: string
  data: Record<string, unknown>
  createdAt: string
}

export interface Turn {
  turnId: string
  requestText: string
  responseText: string
  status: 'running' | 'completed' | 'cancelled' | 'error'
  stopReason: string
  errorMessage: string
  createdAt: string
  completedAt: string
  isInternal: boolean
  events?: TurnEvent[]
}

// ── Frontend message model ─────────────────────────────────────────────────

export type MessageRole = 'user' | 'agent' | 'system-error'

export type MessageStatus = 'done' | 'streaming' | 'cancelled' | 'error'

export interface Message {
  /** Client-side generated ID for DOM keying */
  id: string
  role: MessageRole
  content: string
  /** ISO-8601 string */
  timestamp: string
  status: MessageStatus
  stopReason?: string
  errorCode?: string
  errorMessage?: string
  /** Back-reference to the server turn */
  turnId?: string
  /** Populated when the agent emits permission_required */
  permissionRequest?: PermissionRequest
}

// ── Permission ─────────────────────────────────────────────────────────────

export type PermissionApproval = 'command' | 'file' | 'network' | 'mcp'

export type PermissionStatus = 'pending' | 'approved' | 'declined' | 'timeout'

export interface PermissionRequest {
  permissionId: string
  turnId: string
  approval: PermissionApproval
  command: string
  requestId: string
  status: PermissionStatus
  /** Unix ms — client-side deadline for countdown display */
  deadlineMs: number
}

// ── Stream state ───────────────────────────────────────────────────────────

export interface StreamState {
  turnId: string
  threadId: string
  /** ID of the Message placeholder being streamed into */
  messageId: string
  status: 'streaming' | 'cancelling'
}

// ── Application state ──────────────────────────────────────────────────────

export interface AppState {
  // — persisted settings —
  clientId: string
  authToken: string
  serverUrl: string
  theme: Theme

  // — runtime data (not persisted) —
  agents: AgentInfo[]
  threads: Thread[]
  activeThreadId: string | null
  /** Keyed by threadId */
  messages: Record<string, Message[]>
  streamState: StreamState | null

  // — UI flags —
  settingsOpen: boolean
  newThreadOpen: boolean
  searchQuery: string
}
