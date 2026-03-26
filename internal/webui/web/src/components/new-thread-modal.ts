import { store } from '../store.ts'
import { api, ApiError } from '../api.ts'
import type { AgentInfo } from '../types.ts'
import { isAbsolutePath, escHtml, debounce } from '../utils.ts'

// ── Icons ──────────────────────────────────────────────────────────────────

const iconClose = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
</svg>`

const iconChevron = `<svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
  <path d="M2 4l4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

// ── Agent Icons ─────────────────────────────────────────────────────────────

const agentIcons: Record<string, string> = {
  codex: `<img src="/codex-icon.png" width="32" height="32" alt="Codex" style="border-radius:8px;display:block;">`,
  gemini: `<img src="/gemini-icon.png" width="32" height="32" alt="Gemini CLI" style="border-radius:8px;display:block;">`,
  claude: `<img src="/claude-icon.png" width="32" height="32" alt="Claude Code" style="border-radius:8px;display:block;">`,
  cursor: `<span class="agent-card-cursor-icon" role="img" aria-label="Cursor CLI"></span>`,
  kimi: `<span class="agent-card-kimi-icon" role="img" aria-label="Kimi CLI"></span>`,
  opencode: `<span class="agent-card-opencode-icon" role="img" aria-label="OpenCode"></span>`,
  qwen: `<img src="/qwen-icon.png" width="32" height="32" alt="Qwen Code" style="border-radius:8px;display:block;">`,
  blackbox: `<img src="/blackbox.png" width="32" height="32" alt="BLACKBOX AI" style="border-radius:8px;display:block;">`,
}

const iconAgentDefault = `<svg width="32" height="32" viewBox="0 0 32 32" fill="none" aria-hidden="true">
  <rect width="32" height="32" rx="8" fill="#6B7280"/>
  <g transform="translate(4,4)" stroke="white" stroke-width="1.5" stroke-linecap="round">
    <circle cx="12" cy="12" r="9"/>
    <path d="M12 8v4l3 3"/>
  </g>
</svg>`

function agentIcon(agentId: string): string {
  return agentIcons[agentId] ?? iconAgentDefault
}

// ── State ──────────────────────────────────────────────────────────────────

interface ModalState {
  selectedAgent: string
  cwd: string
  title: string
  agentOptionsRaw: string
  advancedOpen: boolean
  submitting: boolean
  error: string
  cwdError: string
  pathSearchQuery: string
  pathSearchResults: string[]
  pathSearchLoading: boolean
  pathSearchSelectedIndex: number
  recentDirectories: string[]
  showRecentDirectories: boolean
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
      <div class="agent-card-icon">${agentIcon(agent.id)}</div>
      <span class="agent-card-name">${escHtml(agent.name)}</span>
      ${disabled ? '<span class="agent-card-status">Unavailable</span>' : ''}
    </label>`
}

function renderModal(s: ModalState, agents: AgentInfo[]): string {
  const cwdInvalid = s.cwd.length > 0 && !isAbsolutePath(s.cwd)
  const canSubmit = !!s.selectedAgent && isAbsolutePath(s.cwd) && !s.submitting

  return `
    <div class="modal-overlay" id="new-thread-overlay" role="dialog" aria-modal="true" aria-label="New agent">
      <div class="modal" id="new-thread-modal">

        <div class="modal-header">
          <div class="modal-header-copy">
            <h2 class="modal-title">New Agent</h2>
          </div>
          <button class="btn btn-icon" id="new-thread-close" aria-label="Close">${iconClose}</button>
        </div>

        <div class="modal-body">
          <p class="modal-lead">Choose an agent, point it at a project directory, and optionally preconfigure advanced JSON options.</p>

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
            <div class="path-search-container">
              <input
                id="cwd-input"
                class="settings-input ${cwdInvalid || s.cwdError ? 'settings-input--error' : ''}"
                type="text"
                placeholder="Type to search directories in home folder..."
                value="${escHtml(s.cwd)}"
                autocomplete="off"
                spellcheck="false"
              />
              ${s.showRecentDirectories && s.recentDirectories.length > 0 && !s.pathSearchQuery ? `
                <div class="path-search-dropdown" id="path-search-dropdown">
                  <div class="path-search-header">Recent directories</div>
                  ${s.recentDirectories.map((path, idx) => `
                    <div class="path-search-item ${idx === s.pathSearchSelectedIndex ? 'path-search-item--selected' : ''}" data-path="${escHtml(path)}">
                      ${escHtml(path)}
                    </div>
                  `).join('')}
                </div>
              ` : ''}
            ${s.pathSearchResults.length > 0 ? `
                <div class="path-search-dropdown" id="path-search-dropdown">
                  ${s.pathSearchResults.map((path, idx) => `
                    <div class="path-search-item ${idx === s.pathSearchSelectedIndex ? 'path-search-item--selected' : ''}" data-path="${escHtml(path)}">
                      ${escHtml(path)}
                    </div>
                  `).join('')}
                </div>
              ` : ''}
              ${s.pathSearchLoading ? '<div class="path-search-loading">Searching...</div>' : ''}
            </div>
            ${s.cwdError
              ? `<p class="form-hint form-hint--error" id="cwd-hint">${escHtml(s.cwdError)}</p>`
              : cwdInvalid
                ? `<p class="form-hint form-hint--error" id="cwd-hint">Path must be absolute (start with /)</p>`
                : `<p class="form-hint" id="cwd-hint">Absolute path to the project directory.</p>`}
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
            ${s.submitting ? '<span class="btn-spinner"></span> Creating…' : 'Create Agent'}
          </button>
        </div>

      </div>
    </div>`
}

// ── Mount / unmount ────────────────────────────────────────────────────────

let container: HTMLDivElement | null = null
let onCreated: ((threadId: string) => void) | null = null

let modalState: ModalState = {
  selectedAgent: '',
  cwd: '',
  title: '',
  agentOptionsRaw: '',
  advancedOpen: false,
  submitting: false,
  error: '',
  cwdError: '',
  pathSearchQuery: '',
  pathSearchResults: [],
  pathSearchLoading: false,
  pathSearchSelectedIndex: -1,
  recentDirectories: [],
  showRecentDirectories: false,
}

function rerender(): void {
  if (!container) return
  const agents = store.get().agents
  container.innerHTML = renderModal(modalState, agents)
  bindEvents()
}

function unmount(): void {
  // Clean up any floating dropdowns
  document.querySelectorAll('#path-search-dropdown, .path-search-loading').forEach(el => el.remove())
  if (container) { container.remove(); container = null }
  store.set({ newThreadOpen: false })
  onCreated = null
}

function mount(cb: (threadId: string) => void): void {
  if (container) return
  onCreated = cb

  const agents = store.get().agents
  const firstAvailable = agents.find(a => a.status === 'available')

  modalState = {
    selectedAgent: firstAvailable?.id ?? (agents[0]?.id ?? ''),
    cwd: '',
    title: '',
    agentOptionsRaw: '',
    advancedOpen: false,
    submitting: false,
    error: '',
    cwdError: '',
    pathSearchQuery: '',
    pathSearchResults: [],
    pathSearchLoading: false,
    pathSearchSelectedIndex: -1,
    recentDirectories: [],
    showRecentDirectories: false,
  }

  // Load recent directories
  void loadRecentDirectories()

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

  container.querySelector('#new-thread-overlay')?.addEventListener('click', e => {
    if ((e.target as HTMLElement).id === 'new-thread-overlay') unmount()
  })

  container.querySelector('#new-thread-close')?.addEventListener('click', unmount)
  container.querySelector('#new-thread-cancel')?.addEventListener('click', unmount)

  const onEsc = (e: KeyboardEvent) => {
    if (e.key === 'Escape') { unmount(); document.removeEventListener('keydown', onEsc) }
  }
  document.addEventListener('keydown', onEsc)

  container.querySelector('#agent-grid')?.addEventListener('change', e => {
    const radio = e.target as HTMLInputElement
    if (radio.name === 'agent') {
      modalState = {
        ...modalState,
        selectedAgent: radio.value,
        error: '',
      }
      refreshAgentSelection()
      clearModalErrorBanner()
      refreshSubmitButton()
    }
  })

  container.querySelector<HTMLInputElement>('#cwd-input')?.addEventListener('focus', () => {
    if (!modalState.cwd && modalState.recentDirectories.length > 0) {
      modalState = { ...modalState, showRecentDirectories: true, pathSearchSelectedIndex: -1 }
      refreshPathSearchDropdown()
    }
  })

  container.querySelector<HTMLInputElement>('#cwd-input')?.addEventListener('input', e => {
    const value = (e.target as HTMLInputElement).value.trim()
    modalState = { ...modalState, cwd: value, error: '', cwdError: '', pathSearchQuery: value, pathSearchSelectedIndex: -1, showRecentDirectories: false }
    refreshCwdHint()
    refreshSubmitButton()
    // Clear results immediately if input is less than 3 chars
    if (!value || value.length < 3) {
      modalState = { ...modalState, pathSearchResults: [], pathSearchLoading: false }
      refreshPathSearchDropdown()
    }
    debouncedPathSearch(value)
  })

  container.querySelector<HTMLInputElement>('#cwd-input')?.addEventListener('keydown', e => {
    const items = modalState.showRecentDirectories ? modalState.recentDirectories : modalState.pathSearchResults
    if (items.length === 0) return

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        modalState = {
          ...modalState,
          pathSearchSelectedIndex: (modalState.pathSearchSelectedIndex + 1) % items.length
        }
        refreshPathSearchDropdown()
        break
      case 'ArrowUp':
        e.preventDefault()
        modalState = {
          ...modalState,
          pathSearchSelectedIndex: modalState.pathSearchSelectedIndex <= 0
            ? items.length - 1
            : modalState.pathSearchSelectedIndex - 1
        }
        refreshPathSearchDropdown()
        break
      case 'Enter':
        e.preventDefault()
        if (modalState.pathSearchSelectedIndex >= 0) {
          selectPathSearchResult(items[modalState.pathSearchSelectedIndex])
        }
        break
      case 'Escape':
        modalState = { ...modalState, pathSearchResults: [], pathSearchSelectedIndex: -1, showRecentDirectories: false }
        refreshPathSearchDropdown()
        break
    }
  })

  container.querySelector('#path-search-dropdown')?.addEventListener('click', e => {
    const item = (e.target as HTMLElement).closest('.path-search-item') as HTMLElement | null
    if (item?.dataset.path) {
      selectPathSearchResult(item.dataset.path)
    }
  })

  container.querySelector<HTMLInputElement>('#title-input')?.addEventListener('input', e => {
    modalState = { ...modalState, title: (e.target as HTMLInputElement).value }
  })

  container.querySelector<HTMLTextAreaElement>('#agent-options-input')?.addEventListener('input', e => {
    modalState = { ...modalState, agentOptionsRaw: (e.target as HTMLTextAreaElement).value }
  })

  container.querySelector('#advanced-toggle')?.addEventListener('click', () => {
    modalState = { ...modalState, advancedOpen: !modalState.advancedOpen }
    container?.querySelector('.collapsible')?.classList.toggle('collapsible--open', modalState.advancedOpen)
  })

  container.querySelector('#new-thread-submit')?.addEventListener('click', () => void submit())
}

// ── Targeted DOM refreshes (avoid full rerender during user input) ─────────

function refreshCwdHint(): void {
  const input = container?.querySelector<HTMLInputElement>('#cwd-input')
  const hint = container?.querySelector<HTMLElement>('#cwd-hint')
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

function refreshSubmitButtonState(): void {
  const btn = container?.querySelector<HTMLButtonElement>('#new-thread-submit')
  if (!btn) return
  const ok = !!modalState.selectedAgent && isAbsolutePath(modalState.cwd) && !modalState.submitting
  btn.disabled = !ok
  btn.innerHTML = modalState.submitting
    ? '<span class="btn-spinner"></span> Creating…'
    : 'Create Agent'
}

function refreshAgentSelection(): void {
  const grid = container?.querySelector('#agent-grid')
  if (!grid) return
  const radios = grid.querySelectorAll<HTMLInputElement>('input[name="agent"]')
  radios.forEach(radio => {
    const selected = radio.value === modalState.selectedAgent
    radio.checked = selected
    const card = radio.closest('.agent-card')
    if (card) {
      card.classList.toggle('agent-card--selected', selected)
    }
  })
}

function clearModalErrorBanner(): void {
  const banner = container?.querySelector('#modal-error')
  if (banner) {
    banner.remove()
  }
}

// ── Path Search ────────────────────────────────────────────────────────────

const debouncedPathSearch = debounce(async (query: string) => {
  if (!query || query.length < 3) {
    modalState = { ...modalState, pathSearchResults: [], pathSearchLoading: false }
    refreshPathSearchDropdown()
    return
  }

  modalState = { ...modalState, pathSearchLoading: true }
  refreshPathSearchDropdown()

  try {
    const results = await api.searchPaths(query)
    // Only update if the query hasn't changed
    if (modalState.pathSearchQuery === query) {
      modalState = { ...modalState, pathSearchResults: results, pathSearchLoading: false }
      refreshPathSearchDropdown()
    }
  } catch {
    if (modalState.pathSearchQuery === query) {
      modalState = { ...modalState, pathSearchResults: [], pathSearchLoading: false }
      refreshPathSearchDropdown()
    }
  }
}, 200)

function refreshPathSearchDropdown(): void {
  if (!container) return

  // Remove existing dropdown and loading indicator (they are attached to document.body)
  document.querySelectorAll('#path-search-dropdown, .path-search-loading').forEach(el => el.remove())

  const input = container.querySelector<HTMLInputElement>('#cwd-input')
  const pathContainer = container.querySelector('.path-search-container')
  if (!input || !pathContainer) return

  // Calculate position relative to viewport
  const rect = input.getBoundingClientRect()
  const dropdownTop = rect.bottom + 4
  const dropdownLeft = rect.left
  const dropdownWidth = rect.width

  // Add loading indicator
  if (modalState.pathSearchLoading) {
    const loadingEl = document.createElement('div')
    loadingEl.className = 'path-search-loading'
    loadingEl.textContent = 'Searching...'
    loadingEl.style.top = `${dropdownTop}px`
    loadingEl.style.left = `${dropdownLeft}px`
    loadingEl.style.width = `${dropdownWidth}px`
    document.body.appendChild(loadingEl)
    return
  }

  // Show recent directories if enabled and no search query
  if (modalState.showRecentDirectories && modalState.recentDirectories.length > 0 && !modalState.pathSearchQuery) {
    const dropdown = document.createElement('div')
    dropdown.id = 'path-search-dropdown'
    dropdown.className = 'path-search-dropdown'
    dropdown.style.top = `${dropdownTop}px`
    dropdown.style.left = `${dropdownLeft}px`
    dropdown.style.width = `${dropdownWidth}px`
    dropdown.innerHTML = `
      <div class="path-search-header">Recent directories</div>
      ${modalState.recentDirectories.map((path, idx) => `
        <div class="path-search-item ${idx === modalState.pathSearchSelectedIndex ? 'path-search-item--selected' : ''}" data-path="${escHtml(path)}">
          ${escHtml(path)}
        </div>
      `).join('')}
    `
    document.body.appendChild(dropdown)

    // Bind click events
    dropdown.querySelectorAll('.path-search-item').forEach(item => {
      item.addEventListener('click', () => {
        const path = (item as HTMLElement).dataset.path
        if (path) selectPathSearchResult(path)
      })
    })
    return
  }

  // Add dropdown if there are search results
  if (modalState.pathSearchResults.length > 0) {
    const dropdown = document.createElement('div')
    dropdown.id = 'path-search-dropdown'
    dropdown.className = 'path-search-dropdown'
    dropdown.style.top = `${dropdownTop}px`
    dropdown.style.left = `${dropdownLeft}px`
    dropdown.style.width = `${dropdownWidth}px`
    dropdown.innerHTML = modalState.pathSearchResults.map((path, idx) => `
      <div class="path-search-item ${idx === modalState.pathSearchSelectedIndex ? 'path-search-item--selected' : ''}" data-path="${escHtml(path)}">
        ${escHtml(path)}
      </div>
    `).join('')
    document.body.appendChild(dropdown)

    // Bind click events
    dropdown.querySelectorAll('.path-search-item').forEach(item => {
      item.addEventListener('click', () => {
        const path = (item as HTMLElement).dataset.path
        if (path) selectPathSearchResult(path)
      })
    })
  }
}

async function loadRecentDirectories(): Promise<void> {
  try {
    const dirs = await api.getRecentDirectories()
    modalState = { ...modalState, recentDirectories: dirs }
  } catch {
    // Ignore errors, recent directories are optional
  }
}

function selectPathSearchResult(path: string): void {
  modalState = {
    ...modalState,
    cwd: path,
    cwdError: '',
    pathSearchResults: [],
    pathSearchSelectedIndex: -1,
    pathSearchQuery: path,
    showRecentDirectories: false
  }

  const input = container?.querySelector<HTMLInputElement>('#cwd-input')
  if (input) {
    input.value = path
    input.focus()
  }

  refreshPathSearchDropdown()
  refreshCwdHint()
  refreshSubmitButton()
}

// ── Submit ─────────────────────────────────────────────────────────────────

async function submit(): Promise<void> {
  if (!container) return

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

  // Only update submitting state, don't clear errors yet (to avoid flicker if request fails quickly)
  modalState = { ...modalState, submitting: true }
  refreshSubmitButtonState()

  try {
    const threadId = await api.createThread({
      agent: modalState.selectedAgent,
      cwd: modalState.cwd,
      title: modalState.title || undefined,
      agentOptions,
    })

    const threads = await api.getThreads()
    const state = store.get()
    const initialScopeKey = `${threadId}::`
    const nextMessages = Object.prototype.hasOwnProperty.call(state.messages, initialScopeKey)
      ? state.messages
      : { ...state.messages, [initialScopeKey]: [] }
    store.set({ threads, activeThreadId: threadId, messages: nextMessages })

    unmount()
    onCreated?.(threadId)
  } catch (err) {
    const apiErr = err instanceof ApiError ? err : null
    const message = apiErr?.message ?? String(err)
    const code = apiErr?.code ?? ''
    const details = apiErr?.details

    // Check if it's a cwd-related error (path not existing or other cwd validation errors)
    const isCwdError = code === 'INVALID_ARGUMENT' && (
      message.toLowerCase().includes('cwd') ||
      message.toLowerCase().includes('path') ||
      message.toLowerCase().includes('exist') ||
      (details && typeof details === 'object' && details.field === 'cwd')
    )

    if (isCwdError) {
      modalState = { ...modalState, submitting: false, error: '', cwdError: message }
      // Update error UI without full rerender to avoid flicker
      refreshCwdErrorUI()
      refreshSubmitButtonState()
      // Focus the cwd input to help user correct the error
      const cwdInput = container?.querySelector<HTMLInputElement>('#cwd-input')
      cwdInput?.focus()
    } else {
      modalState = { ...modalState, submitting: false, error: message, cwdError: '' }
      // Update error UI without full rerender
      refreshErrorBanner()
      refreshSubmitButtonState()
    }
  }
}

function refreshCwdErrorUI(): void {
  if (!container) return
  const input = container.querySelector<HTMLInputElement>('#cwd-input')
  const hint = container.querySelector<HTMLElement>('#cwd-hint')
  if (!input || !hint) return

  const hasError = !!modalState.cwdError
  input.classList.toggle('settings-input--error', hasError)

  if (modalState.cwdError) {
    hint.className = 'form-hint form-hint--error'
    hint.textContent = modalState.cwdError
  } else {
    const invalid = input.value.length > 0 && !isAbsolutePath(input.value.trim())
    hint.className = `form-hint${invalid ? ' form-hint--error' : ''}`
    hint.textContent = invalid
      ? 'Path must be absolute (start with /)'
      : 'Absolute path to the project directory.'
  }
}

function refreshErrorBanner(): void {
  if (!container) return
  let banner = container.querySelector<HTMLElement>('#modal-error')
  if (modalState.error) {
    if (!banner) {
      banner = document.createElement('div')
      banner.id = 'modal-error'
      banner.className = 'form-error-banner'
      const modalBody = container.querySelector('.modal-body')
      if (modalBody) {
        modalBody.insertBefore(banner, modalBody.firstChild)
      }
    }
    banner.textContent = modalState.error
  } else if (banner) {
    banner.remove()
  }
}

// ── Public API ─────────────────────────────────────────────────────────────

export const newThreadModal = {
  open(onDone?: (threadId: string) => void): void {
    mount(onDone ?? (() => { /* noop */ }))
  },
  close: unmount,
}
