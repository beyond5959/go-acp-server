import type { AppState, Theme } from './types.ts'
import { generateUUID } from './utils.ts'

// ── Storage keys ───────────────────────────────────────────────────────────

const LS = {
  clientId:  'agent-hub:clientId',
  authToken: 'agent-hub:authToken',
  serverUrl: 'agent-hub:serverUrl',
  theme:     'agent-hub:theme',
} as const

// ── Store ──────────────────────────────────────────────────────────────────

type Listener = () => void

class AppStore {
  private state: AppState
  private listeners = new Set<Listener>()

  constructor() {
    this.state = this.buildInitialState()
  }

  // ── Read ─────────────────────────────────────────────────────────────────

  get(): Readonly<AppState> {
    return this.state
  }

  // ── Write ────────────────────────────────────────────────────────────────

  set(patch: Partial<AppState>): void {
    this.state = { ...this.state, ...patch }
    this.persist(patch)
    this.notify()
  }

  // ── Subscribe ────────────────────────────────────────────────────────────

  /** Registers a listener. Returns an unsubscribe function. */
  subscribe(fn: Listener): () => void {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }

  // ── Helpers ──────────────────────────────────────────────────────────────

  /** Resets the client ID to a fresh UUID and persists it. */
  resetClientId(): void {
    const clientId = generateUUID()
    localStorage.setItem(LS.clientId, clientId)
    this.set({ clientId })
  }

  // ── Internals ────────────────────────────────────────────────────────────

  private buildInitialState(): AppState {
    // Ensure client ID always exists
    let clientId = localStorage.getItem(LS.clientId) ?? ''
    if (!clientId) {
      clientId = generateUUID()
      localStorage.setItem(LS.clientId, clientId)
    }

    return {
      // Persisted
      clientId,
      authToken: localStorage.getItem(LS.authToken) ?? '',
      serverUrl: localStorage.getItem(LS.serverUrl) ?? window.location.origin,
      theme: (localStorage.getItem(LS.theme) as Theme | null) ?? 'system',

      // Runtime (empty until F3+)
      agents: [],
      threads: [],
      activeThreadId: null,
      messages: {},
      streamState: null,

      // UI flags
      settingsOpen: false,
      newThreadOpen: false,
      searchQuery: '',
    }
  }

  private persist(patch: Partial<AppState>): void {
    if (patch.authToken !== undefined) {
      localStorage.setItem(LS.authToken, patch.authToken)
    }
    if (patch.serverUrl !== undefined) {
      localStorage.setItem(LS.serverUrl, patch.serverUrl)
    }
    if (patch.theme !== undefined) {
      localStorage.setItem(LS.theme, patch.theme)
    }
    // clientId is only written via resetClientId() to avoid accidental overwrites
  }

  private notify(): void {
    this.listeners.forEach(fn => fn())
  }
}

export const store = new AppStore()
