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

// ── Path validation ────────────────────────────────────────────────────────

/** Returns true if the path is an absolute Unix path (starts with /). */
export function isAbsolutePath(p: string): boolean {
  return p.startsWith('/')
}

// ── HTML escaping ──────────────────────────────────────────────────────────

export function escHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
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
