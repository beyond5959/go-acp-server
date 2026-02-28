import { store } from './store.ts'
import type { AgentInfo, Thread, Turn } from './types.ts'
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

// ── Response shapes ────────────────────────────────────────────────────────

interface AgentsResponse        { agents: AgentInfo[] }
interface ThreadsResponse       { threads: Thread[] }
interface HistoryResponse       { turns: Turn[] }
interface CreateThreadResponse  { threadId: string }
interface CancelTurnResponse    { turnId: string; threadId: string; status: string }

// ── Client ─────────────────────────────────────────────────────────────────

class ApiClient {
  private url(path: string): string {
    return `${store.get().serverUrl}${path}`
  }

  private headers(): Record<string, string> {
    const { clientId, authToken } = store.get()
    const h: Record<string, string> = {
      'Content-Type': 'application/json',
      'X-Client-ID': clientId,
    }
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

  /** GET /v1/threads */
  async getThreads(): Promise<Thread[]> {
    const data = await this.request<ThreadsResponse>('GET', '/v1/threads')
    return data.threads
  }

  /** GET /v1/threads/{threadId}/history */
  async getHistory(threadId: string): Promise<Turn[]> {
    const data = await this.request<HistoryResponse>(
      'GET',
      `/v1/threads/${encodeURIComponent(threadId)}/history`,
    )
    return data.turns
  }

  /** POST /v1/threads */
  async createThread(params: CreateThreadParams): Promise<string> {
    const data = await this.request<CreateThreadResponse>('POST', '/v1/threads', params)
    return data.threadId
  }

  /**
   * POST /v1/threads/{threadId}/turns — opens an SSE stream.
   * Starts the stream immediately and returns the TurnStream handle.
   */
  startTurn(threadId: string, input: string, callbacks: TurnStreamCallbacks): TurnStream {
    const url = this.url(`/v1/threads/${encodeURIComponent(threadId)}/turns`)
    const stream = new TurnStream(url, this.headers(), { input, stream: true }, callbacks)
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
    outcome: 'approved' | 'declined' | 'cancelled',
  ): Promise<void> {
    await this.request('POST', `/v1/permissions/${encodeURIComponent(permissionId)}`, { outcome })
  }
}

export const api = new ApiClient()
