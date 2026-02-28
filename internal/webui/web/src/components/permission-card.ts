import { api } from '../api.ts'
import { escHtml } from '../utils.ts'
import type { PermissionRequiredPayload } from '../sse.ts'

// Server-side default permissionTimeout is 15 seconds
const TIMEOUT_MS = 15_000

// ── Public entry point ────────────────────────────────────────────────────

/**
 * Appends a permission card to `listEl` and starts its countdown timer.
 * The card manages its own lifecycle; no cleanup from the caller is needed.
 */
export function mountPermissionCard(
  listEl: HTMLElement,
  event: PermissionRequiredPayload,
): void {
  const wrapper = document.createElement('div')
  wrapper.className = 'message message--agent'
  wrapper.innerHTML = buildHtml(event)
  listEl.appendChild(wrapper)
  listEl.scrollTop = listEl.scrollHeight

  // Elements are now in the DOM — bind interactivity
  bindCard(event.permissionId)
}

// ── HTML template ─────────────────────────────────────────────────────────

function buildHtml(event: PermissionRequiredPayload): string {
  const { permissionId: pid, approval, command } = event
  const initialSecs = TIMEOUT_MS / 1000

  return `
    <div class="message-avatar perm-avatar">!</div>
    <div class="message-group" style="max-width:min(480px,90%)">
      <div class="permission-card" id="perm-card-${pid}">

        <div class="permission-card-header">
          <span class="permission-badge permission-badge--${escHtml(approval)}">${escHtml(approval)}</span>
          <span class="permission-card-title">Permission Required</span>
        </div>

        <div class="permission-card-body">
          <code class="permission-command">${escHtml(command)}</code>
        </div>

        <div class="permission-card-footer">
          <span class="permission-countdown" id="perm-cd-${pid}">${initialSecs}s</span>
          <div class="permission-actions" id="perm-actions-${pid}">
            <button class="btn btn-sm btn-success" id="perm-allow-${pid}">Allow</button>
            <button class="btn btn-sm btn-danger"  id="perm-deny-${pid}">Deny</button>
          </div>
        </div>

      </div>
      <div class="permission-progress">
        <div class="permission-progress-bar" id="perm-bar-${pid}"></div>
      </div>
    </div>`
}

// ── Countdown + button binding ────────────────────────────────────────────

function bindCard(pid: string): void {
  let resolved = false
  let elapsed  = 0

  const tick = setInterval(() => {
    elapsed += 100
    const remaining = Math.max(0, TIMEOUT_MS - elapsed)
    const pct        = (remaining / TIMEOUT_MS) * 100

    const cdEl  = document.getElementById(`perm-cd-${pid}`)
    const barEl = document.getElementById(`perm-bar-${pid}`)
    if (cdEl)  cdEl.textContent    = `${Math.ceil(remaining / 1000)}s`
    if (barEl) barEl.style.width   = `${pct}%`

    if (remaining === 0 && !resolved) {
      resolved = true
      clearInterval(tick)
      showResolved(pid, 'timeout')
    }
  }, 100)

  document.getElementById(`perm-allow-${pid}`)?.addEventListener('click', () => {
    if (resolved) return
    resolved = true
    clearInterval(tick)
    void doResolve(pid, 'approved')
  })

  document.getElementById(`perm-deny-${pid}`)?.addEventListener('click', () => {
    if (resolved) return
    resolved = true
    clearInterval(tick)
    void doResolve(pid, 'declined')
  })
}

// ── API call ──────────────────────────────────────────────────────────────

async function doResolve(pid: string, outcome: 'approved' | 'declined'): Promise<void> {
  // Disable buttons immediately so the user can't double-click
  document
    .getElementById(`perm-actions-${pid}`)
    ?.querySelectorAll<HTMLButtonElement>('button')
    .forEach(b => { b.disabled = true })

  try {
    await api.resolvePermission(pid, outcome)
  } catch {
    // 409 = already resolved by server — show the intended outcome anyway
  }

  showResolved(pid, outcome)
}

// ── Resolved state ────────────────────────────────────────────────────────

function showResolved(pid: string, outcome: 'approved' | 'declined' | 'timeout'): void {
  const cardEl    = document.getElementById(`perm-card-${pid}`)
  const actionsEl = document.getElementById(`perm-actions-${pid}`)
  const cdEl      = document.getElementById(`perm-cd-${pid}`)
  const barEl     = document.getElementById(`perm-bar-${pid}`)

  if (!cardEl) return  // card was removed from DOM (stream completed) — nothing to do

  // Hide countdown text
  if (cdEl) cdEl.textContent = ''

  // Snap progress bar to full (approved) or empty (denied/timeout)
  if (barEl) {
    barEl.style.transition = 'none'
    barEl.style.width      = outcome === 'approved' ? '100%' : '0%'
    barEl.style.background = outcome === 'approved'
      ? 'var(--success)'
      : 'var(--error)'
  }

  // Replace Allow/Deny buttons with a resolved label
  if (actionsEl) {
    const label =
      outcome === 'approved' ? '✓ Approved'             :
      outcome === 'declined' ? '✗ Denied'               :
                               '⏱ Timed out (auto-denied)'
    actionsEl.innerHTML = `
      <span class="permission-resolved permission-resolved--${outcome}">${label}</span>`
  }

  // Recolor card header and border to reflect outcome
  cardEl.classList.add(`permission-card--${outcome}`)
}
