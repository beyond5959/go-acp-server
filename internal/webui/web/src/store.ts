import type { AppState, Theme } from './types.ts'

// ── Storage keys ───────────────────────────────────────────────────────────

const LS = {
  authToken: 'ngent:authToken',
  serverUrl: 'ngent:serverUrl',
  theme:     'ngent:theme',
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

  // ── Internals ────────────────────────────────────────────────────────────

  private buildInitialState(): AppState {
    localStorage.removeItem('ngent:clientId')

    return {
      // Persisted
      authToken: localStorage.getItem(LS.authToken) || '',
      serverUrl: localStorage.getItem(LS.serverUrl) || window.location.origin,
      theme: (localStorage.getItem(LS.theme) as Theme | null) ?? 'system',

      // Runtime (empty until F3+)
      agents: [],
      threads: [],
      activeThreadId: null,
      messages: {},
      streamStates: {},
      threadCompletionBadges: {},

      // UI flags
      settingsOpen: false,
      newThreadOpen: false,
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
  }

  private notify(): void {
    this.listeners.forEach(fn => fn())
  }
}

export const store = new AppStore()
