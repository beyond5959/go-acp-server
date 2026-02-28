// ── SSE event payloads (mirror server API contract) ───────────────────────

export interface TurnStartedPayload {
  turnId: string
}

export interface MessageDeltaPayload {
  turnId: string
  delta: string
}

export interface TurnCompletedPayload {
  turnId: string
  stopReason: string
}

export interface TurnErrorPayload {
  turnId: string
  code: string
  message: string
}

export interface PermissionRequiredPayload {
  turnId: string
  permissionId: string
  approval: string
  command: string
  requestId: string
}

// ── Callbacks ─────────────────────────────────────────────────────────────

export interface TurnStreamCallbacks {
  onTurnStarted?:        (e: TurnStartedPayload) => void
  onDelta?:              (e: MessageDeltaPayload) => void
  onCompleted?:          (e: TurnCompletedPayload) => void
  onError?:              (e: TurnErrorPayload) => void
  onPermissionRequired?: (e: PermissionRequiredPayload) => void
  /** Called when the fetch connection closes unexpectedly (not via abort). */
  onDisconnect?:         () => void
}

// ── TurnStream ────────────────────────────────────────────────────────────

/**
 * Reads a POST SSE stream using the Fetch API.
 * EventSource is not used because it only supports GET without custom headers.
 */
export class TurnStream {
  private readonly aborter = new AbortController()
  /** Set to true once a terminal event (completed/error) or abort() is received. */
  private terminated = false

  constructor(
    private readonly fetchUrl: string,
    private readonly fetchHeaders: Record<string, string>,
    private readonly body: unknown,
    private readonly callbacks: TurnStreamCallbacks,
  ) {}

  /** Starts the stream. Resolves when the stream ends (for any reason). */
  async start(): Promise<void> {
    let res: Response
    try {
      res = await fetch(this.fetchUrl, {
        method: 'POST',
        headers: this.fetchHeaders,
        body: JSON.stringify(this.body),
        signal: this.aborter.signal,
      })
    } catch {
      if (!this.terminated) this.callbacks.onDisconnect?.()
      return
    }

    if (!res.ok || !res.body) {
      let code = 'INTERNAL'
      let message = `HTTP ${res.status}`
      try {
        const payload = (await res.json()) as {
          error?: { code?: string; message?: string }
        }
        if (payload.error) {
          code    = payload.error.code    ?? code
          message = payload.error.message ?? message
        }
      } catch { /* ignore */ }
      this.callbacks.onError?.({ turnId: '', code, message })
      return
    }

    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''

    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        // SSE messages are separated by blank lines (\n\n)
        const parts = buf.split('\n\n')
        buf = parts.pop() ?? ''
        for (const part of parts) {
          this.dispatch(part.trim())
        }
        if (this.terminated) break
      }
    } catch {
      if (!this.terminated) this.callbacks.onDisconnect?.()
    } finally {
      reader.releaseLock()
    }
  }

  /** Aborts the underlying fetch request. */
  abort(): void {
    this.terminated = true
    this.aborter.abort()
  }

  // ── SSE parsing ──────────────────────────────────────────────────────────

  private dispatch(raw: string): void {
    if (!raw) return

    let eventType = 'message'
    let dataStr = ''

    for (const line of raw.split('\n')) {
      if (line.startsWith('event:')) {
        eventType = line.slice(6).trim()
      } else if (line.startsWith('data:')) {
        dataStr = line.slice(5).trim()
      }
    }

    if (!dataStr) return

    let payload: Record<string, unknown>
    try {
      payload = JSON.parse(dataStr) as Record<string, unknown>
    } catch {
      return
    }

    switch (eventType) {
      case 'turn_started':
        this.callbacks.onTurnStarted?.(payload as unknown as TurnStartedPayload)
        break
      case 'message_delta':
        this.callbacks.onDelta?.(payload as unknown as MessageDeltaPayload)
        break
      case 'turn_completed':
        this.terminated = true
        this.callbacks.onCompleted?.(payload as unknown as TurnCompletedPayload)
        break
      case 'error':
        this.terminated = true
        this.callbacks.onError?.(payload as unknown as TurnErrorPayload)
        break
      case 'permission_required':
        this.callbacks.onPermissionRequired?.(payload as unknown as PermissionRequiredPayload)
        break
    }
  }
}
