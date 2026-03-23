// ── UUID ───────────────────────────────────────────────────────────────────

export function generateUUID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  // Fallback for environments without crypto.randomUUID
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}

// ── Time formatting ────────────────────────────────────────────────────────

/**
 * Returns HH:mm for today, MM-DD HH:mm for other days.
 */
export function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''

  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const time = `${hh}:${mm}`

  const today = new Date()
  if (
    d.getFullYear() === today.getFullYear() &&
    d.getMonth() === today.getMonth() &&
    d.getDate() === today.getDate()
  ) {
    return time
  }

  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${month}-${day} ${time}`
}

/**
 * Returns a human-readable relative time string.
 * e.g. "just now", "3m", "2h", "5d"
 */
export function formatRelativeTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''

  const diffMs = Date.now() - d.getTime()
  if (diffMs < 60_000) return 'just now'
  if (diffMs < 3_600_000) return `${Math.floor(diffMs / 60_000)}m`
  if (diffMs < 86_400_000) return `${Math.floor(diffMs / 3_600_000)}h`
  return `${Math.floor(diffMs / 86_400_000)}d`
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  const precision = value >= 10 || unitIndex === 0 ? 0 : 1
  return `${value.toFixed(precision)} ${units[unitIndex]}`
}

// ── Path validation ────────────────────────────────────────────────────────

/** Returns true if the path is an absolute path (starts with / or ~/). */
export function isAbsolutePath(p: string): boolean {
  return p.startsWith('/') || p.startsWith('~/')
}

// ── HTML escaping ──────────────────────────────────────────────────────────

export function escHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

// ── Clipboard ──────────────────────────────────────────────────────────────

/**
 * Best-effort clipboard copy that works on localhost and LAN HTTP pages.
 * Falls back to execCommand for browsers that block navigator.clipboard on
 * non-secure origins.
 */
export async function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Fall through to the legacy copy path.
    }
  }

  if (typeof document === 'undefined' || !document.body) return false

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.top = '0'
  textarea.style.left = '-9999px'
  textarea.style.opacity = '0'
  textarea.style.pointerEvents = 'none'

  const activeEl = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const selection = typeof window !== 'undefined' ? window.getSelection() : null
  const ranges = selection
    ? Array.from({ length: selection.rangeCount }, (_, i) => selection.getRangeAt(i).cloneRange())
    : []

  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  textarea.setSelectionRange(0, textarea.value.length)

  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    textarea.remove()
    if (selection) {
      selection.removeAllRanges()
      ranges.forEach(range => selection.addRange(range))
    }
    activeEl?.focus()
  }
}

// ── Debounce ───────────────────────────────────────────────────────────────

export function debounce<T extends (...args: Parameters<T>) => void>(
  fn: T,
  delayMs: number,
): (...args: Parameters<T>) => void {
  let timer: ReturnType<typeof setTimeout> | undefined
  return (...args: Parameters<T>) => {
    clearTimeout(timer)
    timer = setTimeout(() => fn(...args), delayMs)
  }
}
