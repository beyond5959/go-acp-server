import { api } from '../api.ts'
import type { PermissionOption } from '../types.ts'
import { escHtml } from '../utils.ts'
import type { PermissionRequiredPayload } from '../sse.ts'

// Server-side default permissionTimeout is 2 hours
export const PERMISSION_TIMEOUT_MS = 2 * 60 * 60 * 1000
const TICK_MS = 1_000

type ResolveOutcome = 'approved' | 'declined' | 'cancelled'
type ResolveState = ResolveOutcome | 'selected' | 'timeout'

interface PermissionAction {
  label: string
  optionId?: string
  outcome?: ResolveOutcome
  tone: 'success' | 'danger' | 'default'
}

interface ResolveDecision {
  label: string
  optionId?: string
  outcome?: ResolveOutcome
}

interface ResolvedPermission {
  state: ResolveState
  label?: string
}

interface MountOptions {
  deadlineMs?: number
  onResolved?: (state: ResolveState) => void
}

// ── Public entry point ────────────────────────────────────────────────────

/**
 * Appends a permission card to `listEl` and starts its countdown timer.
 * The card manages its own lifecycle; no cleanup from the caller is needed.
 */
export function mountPermissionCard(
  listEl: HTMLElement,
  event: PermissionRequiredPayload,
  options?: MountOptions,
): void {
  const timeoutMs = options?.deadlineMs
    ? Math.max(0, options.deadlineMs - Date.now())
    : PERMISSION_TIMEOUT_MS
  const actions = permissionActions(event)

  const wrapper = document.createElement('div')
  wrapper.className = 'message message--agent'
  wrapper.innerHTML = buildHtml(event, timeoutMs, actions)
  listEl.appendChild(wrapper)
  listEl.scrollTop = listEl.scrollHeight

  // Elements are now in the DOM — bind interactivity
  bindCard(event.permissionId, timeoutMs, options?.onResolved)
}

// ── HTML template ─────────────────────────────────────────────────────────

function buildHtml(
  event: PermissionRequiredPayload,
  timeoutMs: number,
  actions: PermissionAction[],
): string {
  const { permissionId: pid, approval, command } = event

  return `
    <div class="message-avatar perm-avatar">!</div>
    <div class="message-group message-group--permission">
      <div class="permission-card" id="perm-card-${pid}">

        <div class="permission-card-header">
          <span class="permission-badge permission-badge--${escHtml(approval)}">${escHtml(approval)}</span>
          <span class="permission-card-title">Permission Required</span>
        </div>

        <div class="permission-card-body">
          <code class="permission-command">${escHtml(command)}</code>
        </div>

        <div class="permission-card-footer">
          <span class="permission-countdown" id="perm-cd-${pid}">${formatRemaining(timeoutMs)}</span>
          <div class="permission-actions" id="perm-actions-${pid}">
            ${actions.map(action => buildActionButton(action)).join('')}
          </div>
        </div>

      </div>
    </div>`
}

function buildActionButton(action: PermissionAction): string {
  return `
    <button
      type="button"
      class="btn btn-sm ${buttonClass(action.tone)}"
      data-label="${escHtml(action.label)}"
      data-option-id="${escHtml(action.optionId ?? '')}"
      data-outcome="${escHtml(action.outcome ?? '')}"
    >${escHtml(action.label)}</button>`
}

function buttonClass(tone: PermissionAction['tone']): string {
  switch (tone) {
    case 'success':
      return 'btn-success'
    case 'danger':
      return 'btn-danger'
    default:
      return 'btn-ghost'
  }
}

// ── Countdown + button binding ────────────────────────────────────────────

function bindCard(
  pid: string,
  timeoutMs: number,
  onResolved?: (state: ResolveState) => void,
): void {
  let resolved = false
  let elapsed = 0
  if (timeoutMs <= 0) {
    showResolved(pid, { state: 'timeout' }, onResolved)
    return
  }

  const tick = setInterval(() => {
    const cardEl = document.getElementById(`perm-card-${pid}`)
    if (!cardEl) {
      clearInterval(tick)
      return
    }

    elapsed += TICK_MS
    const remaining = Math.max(0, timeoutMs - elapsed)

    const cdEl = document.getElementById(`perm-cd-${pid}`)
    if (cdEl) cdEl.textContent = formatRemaining(remaining)

    if (remaining === 0 && !resolved) {
      resolved = true
      clearInterval(tick)
      showResolved(pid, { state: 'timeout' }, onResolved)
    }
  }, TICK_MS)

  document
    .getElementById(`perm-actions-${pid}`)
    ?.querySelectorAll<HTMLButtonElement>('button')
    .forEach(button => {
      button.addEventListener('click', () => {
        if (resolved) return
        resolved = true
        clearInterval(tick)
        void doResolve(pid, buttonDecision(button), onResolved)
      })
    })
}

function buttonDecision(button: HTMLButtonElement): ResolveDecision {
  return {
    label: button.dataset.label?.trim() || button.textContent?.trim() || 'Selected',
    optionId: button.dataset.optionId?.trim() || undefined,
    outcome: parseResolveOutcome(button.dataset.outcome),
  }
}

function parseResolveOutcome(raw: string | undefined): ResolveOutcome | undefined {
  switch ((raw ?? '').trim()) {
    case 'approved':
      return 'approved'
    case 'declined':
      return 'declined'
    case 'cancelled':
      return 'cancelled'
    default:
      return undefined
  }
}

function formatRemaining(remainingMs: number): string {
  const totalSeconds = Math.ceil(Math.max(0, remainingMs) / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  return `${hours}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
}

// ── API call ──────────────────────────────────────────────────────────────

async function doResolve(
  pid: string,
  decision: ResolveDecision,
  onResolved?: (state: ResolveState) => void,
): Promise<void> {
  // Disable buttons immediately so the user can't double-click
  document
    .getElementById(`perm-actions-${pid}`)
    ?.querySelectorAll<HTMLButtonElement>('button')
    .forEach(button => { button.disabled = true })

  try {
    await api.resolvePermission(pid, {
      outcome: decision.outcome,
      optionId: decision.optionId,
    })
  } catch {
    // 409 = already resolved by server — show the intended outcome anyway
  }

  showResolved(pid, {
    state: decision.outcome ?? 'selected',
    label: decision.label,
  }, onResolved)
}

// ── Permission options ────────────────────────────────────────────────────

function permissionActions(event: PermissionRequiredPayload): PermissionAction[] {
  const agentActions = (event.options ?? [])
    .map(buildPermissionAction)
    .filter((action): action is PermissionAction => action !== null)
  if (agentActions.length > 0) return agentActions

  return [
    { label: 'Allow', outcome: 'approved', tone: 'success' },
    { label: 'Deny', outcome: 'declined', tone: 'danger' },
  ]
}

function buildPermissionAction(option: PermissionOption): PermissionAction | null {
  const optionId = option.optionId?.trim() || undefined
  const outcome = permissionOutcomeForKind(option.kind)
  if (!optionId && !outcome) return null

  return {
    label: permissionOptionLabel(option),
    optionId,
    outcome,
    tone: permissionActionTone(outcome),
  }
}

function permissionOptionLabel(option: PermissionOption): string {
  const name = option.name?.trim()
  if (name) return name

  const kind = option.kind?.trim()
  if (kind) return humanizePermissionToken(kind)

  const optionId = option.optionId?.trim()
  if (optionId) return humanizePermissionToken(optionId)

  return 'Select'
}

function permissionOutcomeForKind(rawKind: string | undefined): ResolveOutcome | undefined {
  const kind = (rawKind ?? '').trim().toLowerCase()
  if (!kind) return undefined
  if (kind === 'cancel' || kind === 'cancelled' || kind === 'canceled') return 'cancelled'
  if (kind.startsWith('allow') || kind === 'approve' || kind === 'approved') return 'approved'
  if (kind.startsWith('reject') || kind.startsWith('deny') || kind === 'decline' || kind === 'declined') return 'declined'
  return undefined
}

function permissionActionTone(outcome: ResolveOutcome | undefined): PermissionAction['tone'] {
  switch (outcome) {
    case 'approved':
      return 'success'
    case 'declined':
    case 'cancelled':
      return 'danger'
    default:
      return 'default'
  }
}

function humanizePermissionToken(token: string): string {
  const normalized = token
    .trim()
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
  if (!normalized) return 'Select'
  return normalized.replace(/\b\w/g, char => char.toUpperCase())
}

// ── Resolved state ────────────────────────────────────────────────────────

function showResolved(
  pid: string,
  resolved: ResolvedPermission,
  onResolved?: (state: ResolveState) => void,
): void {
  const cardEl = document.getElementById(`perm-card-${pid}`)
  const actionsEl = document.getElementById(`perm-actions-${pid}`)
  const cdEl = document.getElementById(`perm-cd-${pid}`)
  if (!cardEl) {
    onResolved?.(resolved.state)
    return
  }

  if (cdEl) cdEl.textContent = ''

  if (actionsEl) {
    actionsEl.innerHTML = `
      <span class="permission-resolved permission-resolved--${resolved.state}">${escHtml(resolvedLabel(resolved))}</span>`
  }

  cardEl.classList.add(`permission-card--${resolved.state}`)
  onResolved?.(resolved.state)
}

function resolvedLabel(resolved: ResolvedPermission): string {
  switch (resolved.state) {
    case 'approved':
      return `✓ ${resolved.label ?? 'Approved'}`
    case 'declined':
      return `✗ ${resolved.label ?? 'Denied'}`
    case 'cancelled':
      return `✕ ${resolved.label ?? 'Cancelled'}`
    case 'selected':
      return resolved.label ? `Selected: ${resolved.label}` : 'Selected'
    case 'timeout':
    default:
      return '⏱ Timed out (auto-denied)'
  }
}
