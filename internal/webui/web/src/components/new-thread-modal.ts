import { store } from '../store.ts'
import { api, ApiError } from '../api.ts'
import type { AgentInfo } from '../types.ts'
import { isAbsolutePath, escHtml } from '../utils.ts'

// ── Icons ──────────────────────────────────────────────────────────────────

const iconClose = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
</svg>`

const iconChevron = `<svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
  <path d="M2 4l4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

// ── State ──────────────────────────────────────────────────────────────────

interface ModalState {
  selectedAgent: string
  cwd: string
  title: string
  agentOptionsRaw: string
  advancedOpen: boolean
  submitting: boolean
  error: string
}

// ── Render ─────────────────────────────────────────────────────────────────

function renderAgentCard(agent: AgentInfo, selected: boolean): string {
  const disabled = agent.status === 'unavailable'
  return `
    <label class="agent-card ${selected ? 'agent-card--selected' : ''} ${disabled ? 'agent-card--disabled' : ''}">
      <input
        type="radio"
        name="agent"
        value="${escHtml(agent.id)}"
        ${selected ? 'checked' : ''}
        ${disabled ? 'disabled' : ''}
        class="agent-card-radio"
      />
      <div class="agent-card-body">
        <span class="agent-card-name">${escHtml(agent.name)}</span>
        <span class="agent-card-status ${agent.status === 'available' ? 'agent-card-status--ok' : 'agent-card-status--off'}">
          ${agent.status}
        </span>
      </div>
    </label>`
}

function renderModal(s: ModalState, agents: AgentInfo[]): string {
  const cwdInvalid = s.cwd.length > 0 && !isAbsolutePath(s.cwd)
  const canSubmit   = s.selectedAgent && isAbsolutePath(s.cwd) && !s.submitting

  return `
    <div class="modal-overlay" id="new-thread-overlay" role="dialog" aria-modal="true" aria-label="New thread">
      <div class="modal" id="new-thread-modal">

        <div class="modal-header">
          <h2 class="modal-title">New Thread</h2>
          <button class="btn btn-icon" id="new-thread-close" aria-label="Close">${iconClose}</button>
        </div>

        <div class="modal-body">

          ${s.error ? `<div class="form-error-banner" id="modal-error">${escHtml(s.error)}</div>` : ''}

          <div class="form-group">
            <label class="form-label">Agent</label>
            <div class="agent-grid" id="agent-grid">
              ${agents.length
                ? agents.map(a => renderAgentCard(a, a.id === s.selectedAgent)).join('')
                : '<p class="form-hint">Loading agents…</p>'}
            </div>
          </div>

          <div class="form-group">
            <label class="form-label" for="cwd-input">
              Working Directory <span class="form-required">*</span>
            </label>
            <input
              id="cwd-input"
              class="settings-input ${cwdInvalid ? 'settings-input--error' : ''}"
              type="text"
              placeholder="/home/user/my-project"
              value="${escHtml(s.cwd)}"
              autocomplete="off"
              spellcheck="false"
            />
            ${cwdInvalid
              ? `<p class="form-hint form-hint--error">Path must be absolute (start with /)</p>`
              : `<p class="form-hint">Absolute path to the project directory.</p>`}
          </div>

          <div class="form-group">
            <label class="form-label" for="title-input">Title <span class="form-optional">(optional)</span></label>
            <input
              id="title-input"
              class="settings-input"
              type="text"
              placeholder="e.g. Refactor payment module"
              value="${escHtml(s.title)}"
              maxlength="120"
            />
          </div>

          <div class="collapsible ${s.advancedOpen ? 'collapsible--open' : ''}">
            <button class="collapsible-toggle" id="advanced-toggle" type="button">
              <span class="collapsible-chevron">${iconChevron}</span>
              Advanced options
            </button>
            <div class="collapsible-body">
              <div class="form-group">
                <label class="form-label" for="agent-options-input">
                  Agent Options <span class="form-optional">(JSON)</span>
                </label>
                <textarea
                  id="agent-options-input"
                  class="settings-input settings-input--mono"
                  placeholder='{"mode":"safe"}'
                  rows="3"
                  spellcheck="false"
                >${escHtml(s.agentOptionsRaw)}</textarea>
              </div>
            </div>
          </div>

        </div>

        <div class="modal-footer">
          <button class="btn btn-ghost" id="new-thread-cancel" type="button">Cancel</button>
          <button
            class="btn btn-primary"
            id="new-thread-submit"
            type="button"
            ${canSubmit ? '' : 'disabled'}
          >
            ${s.submitting ? '<span class="btn-spinner"></span> Creating…' : 'Create Thread'}
          </button>
        </div>

      </div>
    </div>`
}

// ── Mount / unmount ────────────────────────────────────────────────────────

let container: HTMLDivElement | null = null
let onCreated: ((threadId: string) => void) | null = null

// Mutable modal state (re-rendered on change)
let modalState: ModalState = {
  selectedAgent: '',
  cwd: '',
  title: '',
  agentOptionsRaw: '',
  advancedOpen: false,
  submitting: false,
  error: '',
}

function rerender(): void {
  if (!container) return
  const agents = store.get().agents
  container.innerHTML = renderModal(modalState, agents)
  bindEvents()
}

function unmount(): void {
  if (container) { container.remove(); container = null }
  store.set({ newThreadOpen: false })
  onCreated = null
}

function mount(cb: (threadId: string) => void): void {
  if (container) return
  onCreated = cb

  const agents = store.get().agents
  // Pre-select first available agent
  const firstAvailable = agents.find(a => a.status === 'available')
  modalState = {
    selectedAgent: firstAvailable?.id ?? (agents[0]?.id ?? ''),
    cwd: '',
    title: '',
    agentOptionsRaw: '',
    advancedOpen: false,
    submitting: false,
    error: '',
  }

  container = document.createElement('div')
  container.innerHTML = renderModal(modalState, agents)
  document.body.appendChild(container)
  store.set({ newThreadOpen: true })

  bindEvents()
  ;(container.querySelector('#cwd-input') as HTMLInputElement | null)?.focus()
}

// ── Event binding ──────────────────────────────────────────────────────────

function bindEvents(): void {
  if (!container) return

  // Backdrop click
  container.querySelector('#new-thread-overlay')?.addEventListener('click', e => {
    if ((e.target as HTMLElement).id === 'new-thread-overlay') unmount()
  })

  // Close / cancel
  container.querySelector('#new-thread-close')?.addEventListener('click', unmount)
  container.querySelector('#new-thread-cancel')?.addEventListener('click', unmount)

  // Escape key
  const onEsc = (e: KeyboardEvent) => {
    if (e.key === 'Escape') { unmount(); document.removeEventListener('keydown', onEsc) }
  }
  document.addEventListener('keydown', onEsc)

  // Agent radio
  container.querySelector('#agent-grid')?.addEventListener('change', e => {
    const radio = e.target as HTMLInputElement
    if (radio.name === 'agent') {
      modalState = { ...modalState, selectedAgent: radio.value, error: '' }
      // Update card highlights without full re-render
      container?.querySelectorAll('.agent-card').forEach(card => {
        const r = card.querySelector<HTMLInputElement>('input[type=radio]')
        card.classList.toggle('agent-card--selected', r?.checked ?? false)
      })
      refreshSubmitButton()
    }
  })

  // CWD input
  container.querySelector<HTMLInputElement>('#cwd-input')?.addEventListener('input', e => {
    modalState = { ...modalState, cwd: (e.target as HTMLInputElement).value.trim(), error: '' }
    refreshCwdHint()
    refreshSubmitButton()
  })

  // Title input
  container.querySelector<HTMLInputElement>('#title-input')?.addEventListener('input', e => {
    modalState = { ...modalState, title: (e.target as HTMLInputElement).value }
  })

  // Agent options textarea
  container.querySelector<HTMLTextAreaElement>('#agent-options-input')?.addEventListener('input', e => {
    modalState = { ...modalState, agentOptionsRaw: (e.target as HTMLTextAreaElement).value }
  })

  // Advanced toggle
  container.querySelector('#advanced-toggle')?.addEventListener('click', () => {
    modalState = { ...modalState, advancedOpen: !modalState.advancedOpen }
    container?.querySelector('.collapsible')?.classList.toggle('collapsible--open', modalState.advancedOpen)
  })

  // Submit
  container.querySelector('#new-thread-submit')?.addEventListener('click', () => void submit())
}

// ── Targeted DOM refreshes (avoid full rerender during user input) ─────────

function refreshCwdHint(): void {
  const input = container?.querySelector<HTMLInputElement>('#cwd-input')
  const hint  = container?.querySelector<HTMLElement>('.form-hint')
  if (!input || !hint) return
  const invalid = input.value.length > 0 && !isAbsolutePath(input.value.trim())
  input.classList.toggle('settings-input--error', invalid)
  hint.className = `form-hint${invalid ? ' form-hint--error' : ''}`
  hint.textContent = invalid
    ? 'Path must be absolute (start with /)'
    : 'Absolute path to the project directory.'
}

function refreshSubmitButton(): void {
  const btn = container?.querySelector<HTMLButtonElement>('#new-thread-submit')
  if (!btn) return
  const ok = !!modalState.selectedAgent && isAbsolutePath(modalState.cwd) && !modalState.submitting
  btn.disabled = !ok
}

// ── Submit ─────────────────────────────────────────────────────────────────

async function submit(): Promise<void> {
  if (!container) return

  // Parse optional agent options
  let agentOptions: Record<string, unknown> | undefined
  if (modalState.agentOptionsRaw.trim()) {
    try {
      agentOptions = JSON.parse(modalState.agentOptionsRaw) as Record<string, unknown>
    } catch {
      modalState = { ...modalState, error: 'Agent options must be valid JSON.' }
      rerender()
      return
    }
  }

  modalState = { ...modalState, submitting: true, error: '' }
  rerender()

  try {
    const threadId = await api.createThread({
      agent: modalState.selectedAgent,
      cwd: modalState.cwd,
      title: modalState.title || undefined,
      agentOptions,
    })

    // Refresh thread list in store
    const threads = await api.getThreads()
    store.set({ threads, activeThreadId: threadId })

    unmount()
    onCreated?.(threadId)
  } catch (err) {
    const msg = err instanceof ApiError ? err.message : String(err)
    modalState = { ...modalState, submitting: false, error: msg }
    rerender()
  }
}

// ── Public API ─────────────────────────────────────────────────────────────

export const newThreadModal = {
  open(onDone?: (threadId: string) => void): void {
    mount(onDone ?? (() => { /* noop */ }))
  },
  close: unmount,
}
