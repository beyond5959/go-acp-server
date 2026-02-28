import { store } from '../store.ts'
import type { Theme } from '../types.ts'
import { debounce, escHtml } from '../utils.ts'

// ── Icons ──────────────────────────────────────────────────────────────────

const iconClose = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
</svg>`

const iconCopy = `<svg width="13" height="13" viewBox="0 0 15 15" fill="none" aria-hidden="true">
  <rect x="4" y="4" width="9" height="9" rx="1.5" stroke="currentColor" stroke-width="1.4"/>
  <path d="M2 11V3a1 1 0 011-1h8" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/>
</svg>`

// ── Render ─────────────────────────────────────────────────────────────────

function renderPanel(): string {
  const { clientId, authToken, serverUrl, theme } = store.get()

  const themeBtn = (value: Theme, label: string) => `
    <button
      class="theme-btn ${theme === value ? 'theme-btn--active' : ''}"
      data-theme-value="${value}"
      type="button"
    >${label}</button>`

  return `
    <div class="settings-overlay" id="settings-overlay" role="dialog" aria-modal="true" aria-label="Settings">
      <div class="settings-panel" id="settings-panel">

        <div class="settings-header">
          <h2 class="settings-title">Settings</h2>
          <button class="btn btn-icon" id="settings-close-btn" aria-label="Close settings">
            ${iconClose}
          </button>
        </div>

        <div class="settings-body">

          <section class="settings-section">
            <h3 class="settings-section-title">Identity</h3>
            <label class="settings-label">Client ID</label>
            <p class="settings-description">
              Automatically assigned. All threads and turns are scoped to this ID.
            </p>
            <div class="settings-id-row">
              <code class="settings-client-id" id="client-id-display">${escHtml(clientId)}</code>
              <button class="btn btn-icon settings-copy-btn" id="copy-client-id-btn" title="Copy client ID">
                ${iconCopy}
              </button>
            </div>
            <button class="btn btn-ghost btn-sm settings-reset-btn" id="reset-client-id-btn">
              Reset Client ID
            </button>
          </section>

          <section class="settings-section">
            <h3 class="settings-section-title">Security</h3>
            <label class="settings-label" for="auth-token-input">Bearer Token</label>
            <p class="settings-description">
              Optional. Set if the server was started with <code>--auth-token</code>.
            </p>
            <input
              id="auth-token-input"
              class="settings-input"
              type="password"
              placeholder="Leave empty if not required"
              value="${escHtml(authToken)}"
              autocomplete="off"
              spellcheck="false"
            />
          </section>

          <section class="settings-section">
            <h3 class="settings-section-title">Appearance</h3>
            <label class="settings-label">Theme</label>
            <div class="theme-btn-group">
              ${themeBtn('light', 'Light')}
              ${themeBtn('system', 'System')}
              ${themeBtn('dark', 'Dark')}
            </div>
          </section>

          <section class="settings-section">
            <h3 class="settings-section-title">Connection</h3>
            <label class="settings-label" for="server-url-input">Server URL</label>
            <p class="settings-description">
              Base URL of the Agent Hub Server API. Change if using a reverse proxy.
            </p>
            <input
              id="server-url-input"
              class="settings-input"
              type="url"
              placeholder="http://127.0.0.1:8686"
              value="${escHtml(serverUrl)}"
              autocomplete="off"
              spellcheck="false"
            />
          </section>

        </div>
      </div>
    </div>`
}

// ── Mount / Unmount ────────────────────────────────────────────────────────

let container: HTMLDivElement | null = null

function unmount(): void {
  if (container) {
    container.remove()
    container = null
  }
  store.set({ settingsOpen: false })
}

function mount(): void {
  if (container) return

  container = document.createElement('div')
  container.innerHTML = renderPanel()
  document.body.appendChild(container)

  bindEvents()

  // Focus the panel for keyboard navigation
  ;(container.querySelector('#settings-panel') as HTMLElement | null)?.focus()
}

// ── Event binding ──────────────────────────────────────────────────────────

function bindEvents(): void {
  if (!container) return

  // Close on backdrop click
  container.querySelector<HTMLElement>('#settings-overlay')?.addEventListener('click', e => {
    if ((e.target as HTMLElement).id === 'settings-overlay') unmount()
  })

  // Close button
  container.querySelector('#settings-close-btn')?.addEventListener('click', unmount)

  // Escape key
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape') { unmount(); document.removeEventListener('keydown', onKey) }
  }
  document.addEventListener('keydown', onKey)

  // Copy client ID
  container.querySelector('#copy-client-id-btn')?.addEventListener('click', () => {
    const id = store.get().clientId
    navigator.clipboard.writeText(id).then(() => {
      const btn = container?.querySelector('#copy-client-id-btn')
      if (btn) {
        btn.textContent = '✓'
        setTimeout(() => { if (btn) btn.innerHTML = iconCopy }, 1500)
      }
    }).catch(() => { /* clipboard access denied, ignore */ })
  })

  // Reset client ID (with confirmation)
  container.querySelector('#reset-client-id-btn')?.addEventListener('click', () => {
    if (!confirm('Reset your Client ID? You will lose access to existing threads in this browser.')) return
    store.resetClientId()
    const display = container?.querySelector<HTMLElement>('#client-id-display')
    if (display) display.textContent = store.get().clientId
  })

  // Auth token — save on change (debounced)
  const saveToken = debounce((v: string) => store.set({ authToken: v }), 400)
  container.querySelector<HTMLInputElement>('#auth-token-input')?.addEventListener('input', e => {
    saveToken((e.target as HTMLInputElement).value)
  })

  // Theme buttons
  container.querySelector('.theme-btn-group')?.addEventListener('click', e => {
    const btn = (e.target as HTMLElement).closest<HTMLButtonElement>('[data-theme-value]')
    if (!btn) return
    const value = btn.dataset.themeValue as Theme
    store.set({ theme: value })
    applyTheme(value)
    // Update active state
    container?.querySelectorAll('.theme-btn').forEach(b => b.classList.remove('theme-btn--active'))
    btn.classList.add('theme-btn--active')
  })

  // Server URL — save on change (debounced)
  const saveUrl = debounce((v: string) => store.set({ serverUrl: v }), 400)
  container.querySelector<HTMLInputElement>('#server-url-input')?.addEventListener('input', e => {
    saveUrl((e.target as HTMLInputElement).value)
  })
}

// ── Theme application ──────────────────────────────────────────────────────

function getSystemTheme(): 'light' | 'dark' {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme === 'system' ? getSystemTheme() : theme
}

// ── Public API ─────────────────────────────────────────────────────────────

export const settingsPanel = {
  open(): void {
    store.set({ settingsOpen: true })
    mount()
  },
  close(): void {
    unmount()
  },
}
