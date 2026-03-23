import { store } from './store.ts'
import type { AgentInfo, ConfigOption, ModelOption, SessionInfo, SessionTranscriptMessage, SlashCommand, Thread, Turn } from './types.ts'
import { TurnStream } from './sse.ts'
import type { TurnStreamCallbacks } from './sse.ts'

// ── Error type ─────────────────────────────────────────────────────────────

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly code: string,
    public readonly statusCode: number,
    public readonly details?: Record<string, unknown>,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

// ── Request params ─────────────────────────────────────────────────────────

export interface CreateThreadParams {
  agent: string
  cwd: string
  title?: string
  agentOptions?: Record<string, unknown>
}

export interface UpdateThreadParams {
  title?: string
  agentOptions?: Record<string, unknown>
}

export interface SetThreadConfigOptionParams {
  configId: string
  value: string
}

// ── Response shapes ────────────────────────────────────────────────────────

interface AgentsResponse        { agents: AgentInfo[] }
interface AgentModelsResponse   { agentId: string; models: ModelOption[] }
interface ThreadsResponse       { threads: Thread[] }
interface HistoryResponse       { turns: Turn[] }
interface CreateThreadResponse  { threadId: string }
interface UpdateThreadResponse  { thread: Thread }
interface ThreadConfigOptionsResponse { threadId: string; configOptions: ConfigOption[] }
interface ThreadSessionsResponse { threadId: string; supported: boolean; sessions: SessionInfo[]; nextCursor?: string }
interface ThreadSessionHistoryResponse {
  threadId: string
  sessionId: string
  supported: boolean
  messages: SessionTranscriptMessage[]
}
interface ThreadSlashCommandsResponse {
  threadId: string
  agentId: string
  commands: SlashCommand[]
}
interface CancelTurnResponse    { turnId: string; threadId: string; status: string }
interface DeleteThreadResponse  { threadId: string; status: string }
interface PathSearchResponse    { query: string; results: string[] }
interface RecentDirectoriesResponse { directories: string[] }

// ── Client ─────────────────────────────────────────────────────────────────

class ApiClient {
  private url(path: string): string {
    return `${store.get().serverUrl}${path}`
  }

  private headers(contentType: string | null = 'application/json'): Record<string, string> {
    const { clientId, authToken } = store.get()
    const h: Record<string, string> = { 'X-Client-ID': clientId }
    if (contentType) h['Content-Type'] = contentType
    if (authToken) h['Authorization'] = `Bearer ${authToken}`
    return h
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    let res: Response
    try {
      res = await fetch(this.url(path), {
        method,
        headers: this.headers(),
        body: body !== undefined ? JSON.stringify(body) : undefined,
      })
    } catch (err) {
      throw new ApiError(`Network error: ${String(err)}`, 'NETWORK_ERROR', 0)
    }

    if (!res.ok) {
      let code = 'INTERNAL'
      let message = `HTTP ${res.status}`
      let details: Record<string, unknown> | undefined
      try {
        const payload = (await res.json()) as {
          error?: { code?: string; message?: string; details?: Record<string, unknown> }
        }
        if (payload.error) {
          code    = payload.error.code    ?? code
          message = payload.error.message ?? message
          details = payload.error.details
        }
      } catch { /* ignore JSON parse failures */ }
      throw new ApiError(message, code, res.status, details)
    }

    return res.json() as Promise<T>
  }

  /** GET /v1/agents */
  async getAgents(): Promise<AgentInfo[]> {
    const data = await this.request<AgentsResponse>('GET', '/v1/agents')
    return data.agents
  }

  /** GET /v1/agents/{agentId}/models */
  async getAgentModels(agentId: string): Promise<ModelOption[]> {
    const data = await this.request<AgentModelsResponse>(
      'GET',
      `/v1/agents/${encodeURIComponent(agentId)}/models`,
    )
    return data.models
  }

  /** GET /v1/threads */
  async getThreads(): Promise<Thread[]> {
    const data = await this.request<ThreadsResponse>('GET', '/v1/threads')
    return data.threads
  }

  /** GET /v1/threads/{threadId}/history */
  async getHistory(threadId: string): Promise<Turn[]> {
    const data = await this.request<HistoryResponse>(
      'GET',
      `/v1/threads/${encodeURIComponent(threadId)}/history?includeEvents=1`,
    )
    return data.turns
  }

  /** GET /v1/threads/{threadId}/config-options */
  async getThreadConfigOptions(threadId: string): Promise<ConfigOption[]> {
    const data = await this.request<ThreadConfigOptionsResponse>(
      'GET',
      `/v1/threads/${encodeURIComponent(threadId)}/config-options`,
    )
    return data.configOptions
  }

  /** GET /v1/threads/{threadId}/sessions */
  async getThreadSessions(
    threadId: string,
    cursor = '',
  ): Promise<{ supported: boolean; sessions: SessionInfo[]; nextCursor: string }> {
    const params = new URLSearchParams()
    if (cursor.trim()) params.set('cursor', cursor.trim())
    const suffix = params.toString() ? `?${params.toString()}` : ''
    const data = await this.request<ThreadSessionsResponse>(
      'GET',
      `/v1/threads/${encodeURIComponent(threadId)}/sessions${suffix}`,
    )
    return {
      supported: !!data.supported,
      sessions: data.sessions ?? [],
      nextCursor: data.nextCursor ?? '',
    }
  }

  /** GET /v1/threads/{threadId}/session-history */
  async getThreadSessionHistory(
    threadId: string,
    sessionId: string,
  ): Promise<{ supported: boolean; messages: SessionTranscriptMessage[] }> {
    const params = new URLSearchParams({ sessionId: sessionId.trim() })
    const data = await this.request<ThreadSessionHistoryResponse>(
      'GET',
      `/v1/threads/${encodeURIComponent(threadId)}/session-history?${params.toString()}`,
    )
    return {
      supported: !!data.supported,
      messages: data.messages ?? [],
    }
  }

  /** GET /v1/threads/{threadId}/slash-commands */
  async getThreadSlashCommands(threadId: string): Promise<SlashCommand[]> {
    const data = await this.request<ThreadSlashCommandsResponse>(
      'GET',
      `/v1/threads/${encodeURIComponent(threadId)}/slash-commands`,
    )
    return data.commands ?? []
  }

  /** POST /v1/threads/{threadId}/config-options */
  async setThreadConfigOption(threadId: string, params: SetThreadConfigOptionParams): Promise<ConfigOption[]> {
    const data = await this.request<ThreadConfigOptionsResponse>(
      'POST',
      `/v1/threads/${encodeURIComponent(threadId)}/config-options`,
      params,
    )
    return data.configOptions
  }

  /** POST /v1/threads */
  async createThread(params: CreateThreadParams): Promise<string> {
    const data = await this.request<CreateThreadResponse>('POST', '/v1/threads', params)
    return data.threadId
  }

  /** PATCH /v1/threads/{threadId} */
  async updateThread(threadId: string, params: UpdateThreadParams): Promise<Thread> {
    const data = await this.request<UpdateThreadResponse>(
      'PATCH',
      `/v1/threads/${encodeURIComponent(threadId)}`,
      params,
    )
    return data.thread
  }

  /** DELETE /v1/threads/{threadId} */
  async deleteThread(threadId: string): Promise<void> {
    await this.request<DeleteThreadResponse>('DELETE', `/v1/threads/${encodeURIComponent(threadId)}`)
  }

  /**
   * POST /v1/threads/{threadId}/turns — opens an SSE stream.
   * Starts the stream immediately and returns the TurnStream handle.
   */
  startTurn(
    threadId: string,
    params: { input: string; attachments?: File[] },
    callbacks: TurnStreamCallbacks,
  ): TurnStream {
    const url = this.url(`/v1/threads/${encodeURIComponent(threadId)}/turns`)
    const attachments = params.attachments ?? []
    let body: FormData | Record<string, unknown> = {
      input: params.input,
      stream: true,
    }
    let headers = this.headers()

    if (attachments.length) {
      const formData = new FormData()
      formData.set('input', params.input)
      formData.set('stream', 'true')
      attachments.forEach(file => {
        formData.append('attachments', file, file.name)
      })
      body = formData
      headers = this.headers(null)
    }

    const stream = new TurnStream(url, headers, body, callbacks)
    void stream.start()
    return stream
  }

  /** POST /v1/turns/{turnId}/cancel */
  async cancelTurn(turnId: string): Promise<void> {
    await this.request<CancelTurnResponse>('POST', `/v1/turns/${encodeURIComponent(turnId)}/cancel`)
  }

  /** POST /v1/permissions/{permissionId} */
  async resolvePermission(
    permissionId: string,
    decision: {
      outcome?: 'approved' | 'declined' | 'cancelled'
      optionId?: string
    },
  ): Promise<void> {
    await this.request('POST', `/v1/permissions/${encodeURIComponent(permissionId)}`, decision)
  }

  /** GET /v1/path-search?q={query} */
  async searchPaths(query: string): Promise<string[]> {
    if (!query || query.length < 3) return []
    const params = new URLSearchParams({ q: query.trim() })
    const data = await this.request<PathSearchResponse>('GET', `/v1/path-search?${params.toString()}`)
    return data.results ?? []
  }

  /** GET /v1/recent-directories */
  async getRecentDirectories(): Promise<string[]> {
    const data = await this.request<RecentDirectoriesResponse>('GET', '/v1/recent-directories')
    return data.directories ?? []
  }
}

export const api = new ApiClient()
