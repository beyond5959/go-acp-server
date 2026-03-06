import './style.css'
import { store } from './store.ts'
import { api } from './api.ts'
import { applyTheme, settingsPanel } from './components/settings-panel.ts'
import { newThreadModal } from './components/new-thread-modal.ts'
import { mountPermissionCard, PERMISSION_TIMEOUT_MS } from './components/permission-card.ts'
import { renderMarkdown, bindMarkdownControls } from './markdown.ts'
import type { Thread, Message, ConfigOption, ConfigOptionValue, Turn, StreamState } from './types.ts'
import type { TurnStream, PermissionRequiredPayload } from './sse.ts'
import { escHtml, formatRelativeTime, formatTimestamp, generateUUID } from './utils.ts'

// ── Theme ─────────────────────────────────────────────────────────────────

applyTheme(store.get().theme)
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
  if (store.get().theme === 'system') applyTheme('system')
})

// ── Icons ─────────────────────────────────────────────────────────────────

const iconPlus = `<svg width="15" height="15" viewBox="0 0 15 15" fill="none" aria-hidden="true">
  <path d="M7.5 2v11M2 7.5h11" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>
</svg>`

const iconSend = `<svg width="14" height="14" viewBox="0 0 15 15" fill="none" aria-hidden="true">
  <path d="M1.5 7.5h12M8.5 2l5 5.5-5 5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

const iconSettings = `<svg width="14" height="14" viewBox="0 0 15 15" fill="none" aria-hidden="true">
  <circle cx="7.5" cy="7.5" r="2" stroke="currentColor" stroke-width="1.5"/>
  <path d="M7.5 1v1.5M7.5 12.5V14M1 7.5h1.5M12.5 7.5H14M3.05 3.05l1.06 1.06M10.9 10.9l1.05 1.05M3.05 11.95l1.06-1.06M10.9 4.1l1.05-1.05"
    stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
</svg>`

const iconMenu = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <path d="M2 4h12M2 8h12M2 12h12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
</svg>`

const iconCheck = `<svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
  <path d="M3.5 8.5l3 3 6-7" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

const codexIconURL = '/codex-icon.png'
const geminiIconURL = '/gemini-icon.png'
const claudeIconURL = '/claude-icon.png'
const opencodeIconURL = '/opencode-icon.png'
const qwenIconURL = '/qwen-icon.png'

const defaultConfigCatalogCacheKey = '__default__'
const threadConfigCache = new Map<string, ConfigOption[]>()
const agentConfigCatalogCache = new Map<string, ConfigOption[]>()
const agentConfigCatalogInFlight = new Map<string, Promise<ConfigOption[]>>()
const threadConfigSwitching = new Set<string>()

function cloneConfigOptions(options: ConfigOption[]): ConfigOption[] {
  return options.map(option => ({
    ...option,
    options: [...(option.options ?? [])],
  }))
}

function normalizeConfigOptions(options: ConfigOption[], includeCurrentValue = true): ConfigOption[] {
  const byId = new Set<string>()
  const normalized: ConfigOption[] = []

  for (const rawOption of options) {
    const id = rawOption.id?.trim() ?? ''
    if (!id || byId.has(id)) continue
    byId.add(id)

    const seenValue = new Set<string>()
    const values: ConfigOptionValue[] = []
    for (const rawValue of rawOption.options ?? []) {
      const value = rawValue.value?.trim() ?? ''
      if (!value || seenValue.has(value)) continue
      seenValue.add(value)
      values.push({
        value,
        name: (rawValue.name || value).trim() || value,
        description: rawValue.description?.trim() || undefined,
      })
    }

    const currentValue = rawOption.currentValue?.trim() ?? ''
    if (includeCurrentValue && currentValue && !seenValue.has(currentValue)) {
      values.unshift({ value: currentValue, name: currentValue })
      seenValue.add(currentValue)
    }

    normalized.push({
      id,
      category: rawOption.category?.trim() || undefined,
      name: rawOption.name?.trim() || id,
      description: rawOption.description?.trim() || undefined,
      type: rawOption.type?.trim() || undefined,
      currentValue,
      options: values,
    })
  }
  return normalized
}

function normalizeConfigCatalogOptions(options: ConfigOption[]): ConfigOption[] {
  return normalizeConfigOptions(options, false).map(option => ({
    ...option,
    currentValue: '',
    options: [...(option.options ?? [])],
  }))
}

function normalizeAgentConfigCatalogKey(agentId: string, modelId = ''): string {
  const normalizedAgentID = agentId.trim().toLowerCase()
  if (!normalizedAgentID) return ''
  const normalizedModelID = modelId.trim() || defaultConfigCatalogCacheKey
  return `${normalizedAgentID}::${normalizedModelID}`
}

function hasAgentConfigCatalog(agentId: string, modelId = ''): boolean {
  const key = normalizeAgentConfigCatalogKey(agentId, modelId)
  return !!key && agentConfigCatalogCache.has(key)
}

function getAgentConfigCatalog(agentId: string, modelId = ''): ConfigOption[] {
  const key = normalizeAgentConfigCatalogKey(agentId, modelId)
  if (!key) return []
  return agentConfigCatalogCache.get(key) ?? []
}

function cacheAgentConfigCatalog(agentId: string, modelId: string, options: ConfigOption[]): ConfigOption[] {
  const cacheKey = normalizeAgentConfigCatalogKey(agentId, modelId)
  const normalized = normalizeConfigCatalogOptions(options)
  if (cacheKey) {
    agentConfigCatalogCache.set(cacheKey, normalized)
  }
  return normalized
}

function cacheThreadConfigOptions(thread: Thread, options: ConfigOption[], selectedModelID?: string): ConfigOption[] {
  const normalized = normalizeConfigOptions(options)
  threadConfigCache.set(thread.threadId, normalized)
  cacheAgentConfigCatalog(thread.agent ?? '', selectedModelID ?? fallbackThreadModelID(thread), normalized)
  return normalized
}

function findModelOption(options: ConfigOption[]): ConfigOption | null {
  for (const option of options) {
    const category = option.category?.trim().toLowerCase() ?? ''
    const id = option.id.trim().toLowerCase()
    if (category === 'model' || id === 'model') {
      return option
    }
  }
  return null
}

function fallbackThreadModelID(thread: Thread): string {
  const model = thread.agentOptions?.modelId
  return typeof model === 'string' ? model.trim() : ''
}

async function loadThreadConfigOptions(threadId: string): Promise<ConfigOption[]> {
  const thread = store.get().threads.find(item => item.threadId === threadId)
  if (!thread) return []
  const selectedModelID = fallbackThreadModelID(thread)
  const catalogKey = normalizeAgentConfigCatalogKey(thread.agent ?? '', selectedModelID)

  if (threadConfigCache.has(thread.threadId) || hasAgentConfigCatalog(thread.agent ?? '', selectedModelID)) {
    return cloneConfigOptions(getThreadConfigOptionsForRender(thread))
  }

  const inFlight = catalogKey ? agentConfigCatalogInFlight.get(catalogKey) : undefined
  if (inFlight) {
    return inFlight.then(() => cloneConfigOptions(getThreadConfigOptionsForRender(thread)))
  }

  const task = api.getThreadConfigOptions(thread.threadId)
    .then(options => {
      cacheThreadConfigOptions(thread, options, selectedModelID)
      return cloneConfigOptions(getThreadConfigOptionsForRender(thread))
    })
    .finally(() => {
      if (catalogKey) agentConfigCatalogInFlight.delete(catalogKey)
    })

  if (catalogKey) agentConfigCatalogInFlight.set(catalogKey, task)
  return task
}

// ── Active stream state (DOM-managed, per thread) ──────────────────────────

/**
 * Non-null while a streaming bubble is live in the DOM.
 * We use this to prevent updateMessageList() from wiping the in-progress bubble.
 */
let activeStreamMsgId: string | null = null
const streamsByThread = new Map<string, TurnStream>()
const streamBufferByThread = new Map<string, string>()
const streamStartedAtByThread = new Map<string, string>()
type PendingPermission = PermissionRequiredPayload & { deadlineMs: number }
const pendingPermissionsByThread = new Map<string, Map<string, PendingPermission>>()

/** Last threadId that triggered a full chat-area re-render. */
let lastRenderThreadId: string | null = null
let openThreadActionMenuId: string | null = null
let renamingThreadId: string | null = null
let renamingThreadDraft = ''

// ── Scroll helpers ────────────────────────────────────────────────────────

/** True when the list is within 100px of its bottom — safe to auto-scroll. */
function isNearBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < 100
}

// ── Message store helpers ─────────────────────────────────────────────────

function addMessageToStore(threadId: string, msg: Message): void {
  const { messages } = store.get()
  store.set({ messages: { ...messages, [threadId]: [...(messages[threadId] ?? []), msg] } })
}

function omitThreadCompletionBadge(
  badges: Record<string, boolean>,
  threadId: string,
): Record<string, boolean> {
  if (!threadId || !badges[threadId]) return badges
  const next = { ...badges }
  delete next[threadId]
  return next
}

function markThreadCompletionBadge(threadId: string): void {
  if (!threadId) return
  const state = store.get()
  if (state.activeThreadId === threadId || state.threadCompletionBadges[threadId]) return
  store.set({
    threadCompletionBadges: {
      ...state.threadCompletionBadges,
      [threadId]: true,
    },
  })
}

function activateThread(threadId: string): void {
  if (!threadId) return
  const state = store.get()
  const clearedThreadActions = resetThreadActionMenuState()
  const nextThreadCompletionBadges = omitThreadCompletionBadge(state.threadCompletionBadges, threadId)
  if (threadId === state.activeThreadId) {
    if (nextThreadCompletionBadges !== state.threadCompletionBadges) {
      store.set({ threadCompletionBadges: nextThreadCompletionBadges })
    } else if (clearedThreadActions) {
      updateThreadList()
    }
    return
  }

  store.set({
    activeThreadId: threadId,
    threadCompletionBadges: nextThreadCompletionBadges,
  })
}

function resetThreadActionMenuState(): boolean {
  const changed = openThreadActionMenuId !== null || renamingThreadId !== null || renamingThreadDraft !== ''
  if (!changed) return false
  openThreadActionMenuId = null
  renamingThreadId = null
  renamingThreadDraft = ''
  return true
}

function cancelThreadRename(threadId: string): void {
  if (renamingThreadId !== threadId) return
  renamingThreadId = null
  renamingThreadDraft = ''
  updateThreadList()
}

function toggleThreadActionMenu(threadId: string): void {
  if (!threadId) return
  if (openThreadActionMenuId === threadId) {
    resetThreadActionMenuState()
    updateThreadList()
    return
  }

  openThreadActionMenuId = threadId
  renamingThreadId = null
  renamingThreadDraft = ''
  updateThreadList()
}

function beginRenameThread(threadId: string): void {
  const thread = store.get().threads.find(item => item.threadId === threadId)
  if (!thread) return

  openThreadActionMenuId = threadId
  renamingThreadId = threadId
  renamingThreadDraft = thread.title || threadTitle(thread)
  updateThreadList()
  requestAnimationFrame(() => {
    const input = document.querySelector<HTMLInputElement>('.thread-rename-input')
    input?.focus()
    input?.select()
  })
}

function getThreadMenuTrigger(threadId: string): HTMLButtonElement | null {
  return Array.from(document.querySelectorAll<HTMLButtonElement>('.thread-item-menu-trigger'))
    .find(btn => btn.dataset.threadId === threadId) ?? null
}

function renderThreadActionPopover(t: Thread): string {
  const isOpen = openThreadActionMenuId === t.threadId
  if (!isOpen) return ''

  if (renamingThreadId === t.threadId) {
    return `
      <div class="thread-action-popover thread-action-popover--rename" data-thread-id="${escHtml(t.threadId)}">
        <form class="thread-rename-form" data-thread-id="${escHtml(t.threadId)}">
          <input
            class="thread-rename-input"
            data-thread-id="${escHtml(t.threadId)}"
            type="text"
            value="${escHtml(renamingThreadDraft)}"
            placeholder="Thread name"
            maxlength="120"
            aria-label="Rename thread"
          />
          <div class="thread-rename-actions">
            <button class="btn btn-primary btn-sm" type="submit">Save</button>
            <button class="btn btn-ghost btn-sm thread-rename-cancel-btn" type="button" data-thread-id="${escHtml(t.threadId)}">
              Cancel
            </button>
          </div>
        </form>
      </div>`
  }

  return `
    <div class="thread-action-popover thread-action-menu" data-thread-id="${escHtml(t.threadId)}" role="menu" aria-label="Thread actions">
      <button class="thread-action-menu-item" type="button" data-thread-id="${escHtml(t.threadId)}" data-action="rename" role="menuitem">
        Rename
      </button>
      <button
        class="thread-action-menu-item thread-action-menu-item--danger"
        type="button"
        data-thread-id="${escHtml(t.threadId)}"
        data-action="delete"
        role="menuitem"
      >
        Delete
      </button>
    </div>`
}

function renderThreadActionLayer(): void {
  const layer = document.getElementById('thread-action-layer')
  if (!layer) return
  if (!openThreadActionMenuId) {
    layer.innerHTML = ''
    layer.hidden = true
    return
  }

  const thread = store.get().threads.find(item => item.threadId === openThreadActionMenuId)
  const trigger = getThreadMenuTrigger(openThreadActionMenuId)
  const sidebar = document.getElementById('sidebar')
  if (!thread || !trigger || !sidebar) {
    resetThreadActionMenuState()
    layer.innerHTML = ''
    layer.hidden = true
    return
  }

  layer.hidden = false
  layer.innerHTML = renderThreadActionPopover(thread)

  const popover = layer.querySelector<HTMLElement>('.thread-action-popover')
  if (!popover) return

  const margin = 8
  const offset = 8
  const triggerRect = trigger.getBoundingClientRect()
  const sidebarRect = sidebar.getBoundingClientRect()
  const popoverWidth = popover.offsetWidth
  const popoverHeight = popover.offsetHeight
  const maxLeft = Math.max(margin, sidebar.clientWidth - popoverWidth - margin)
  const maxTop = Math.max(margin, sidebar.clientHeight - popoverHeight - margin)

  let left = triggerRect.right - sidebarRect.left - popoverWidth
  left = Math.min(Math.max(left, margin), maxLeft)

  let top = triggerRect.bottom - sidebarRect.top + offset
  if (top > maxTop) {
    top = triggerRect.top - sidebarRect.top - popoverHeight - offset
  }
  top = Math.min(Math.max(top, margin), maxTop)

  popover.style.left = `${left}px`
  popover.style.top = `${top}px`

  popover.addEventListener('click', e => e.stopPropagation())
  popover.addEventListener('keydown', e => e.stopPropagation())

  layer.querySelectorAll<HTMLButtonElement>('.thread-action-menu-item').forEach(btn => {
    btn.addEventListener('click', e => {
      e.preventDefault()
      e.stopPropagation()
      const id = btn.dataset.threadId ?? ''
      if (!id) return
      if (btn.dataset.action === 'rename') {
        beginRenameThread(id)
        return
      }
      if (btn.dataset.action === 'delete') {
        void handleDeleteThread(id)
      }
    })
  })

  layer.querySelectorAll<HTMLFormElement>('.thread-rename-form').forEach(form => {
    form.addEventListener('submit', e => {
      e.preventDefault()
      e.stopPropagation()
      const threadId = form.dataset.threadId ?? ''
      const input = form.querySelector<HTMLInputElement>('.thread-rename-input')
      if (!threadId || !input) return

      const controls = Array.from(form.querySelectorAll<HTMLInputElement | HTMLButtonElement>('input, button'))
      controls.forEach(control => { control.disabled = true })
      void handleRenameThread(threadId, input.value).finally(() => {
        controls.forEach(control => {
          if (control.isConnected) control.disabled = false
        })
      })
    })
  })

  layer.querySelectorAll<HTMLInputElement>('.thread-rename-input').forEach(input => {
    input.addEventListener('input', () => {
      const threadId = input.dataset.threadId ?? ''
      if (!threadId || renamingThreadId !== threadId) return
      renamingThreadDraft = input.value
    })
    input.addEventListener('keydown', e => {
      e.stopPropagation()
      if (e.key === 'Escape') {
        e.preventDefault()
        const threadId = input.dataset.threadId ?? ''
        if (threadId) cancelThreadRename(threadId)
      }
    })
  })

  layer.querySelectorAll<HTMLButtonElement>('.thread-rename-cancel-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.preventDefault()
      e.stopPropagation()
      const id = btn.dataset.threadId ?? ''
      if (!id) return
      cancelThreadRename(id)
    })
  })
}

function getThreadStreamState(threadId: string | null): StreamState | null {
  if (!threadId) return null
  return store.get().streamStates[threadId] ?? null
}

function setThreadStreamState(threadId: string, next: StreamState | null): void {
  const { streamStates } = store.get()
  const updated = { ...streamStates }
  if (next) {
    updated[threadId] = next
  } else {
    delete updated[threadId]
  }
  store.set({ streamStates: updated })
}

function appendOrRestoreStreamingBubble(thread: Thread): void {
  const streamState = getThreadStreamState(thread.threadId)
  if (!streamState) return

  const listEl = document.getElementById('message-list')
  if (!listEl) return

  const bubbleID = `bubble-${streamState.messageId}`
  if (document.getElementById(bubbleID)) {
    activeStreamMsgId = streamState.messageId
    return
  }

  listEl.querySelector('.empty-state')?.remove()
  listEl.querySelector('.message-list-loading')?.remove()
  const startedAt = streamStartedAtByThread.get(thread.threadId) ?? new Date().toISOString()
  const avatar = renderAgentAvatar(thread.agent ?? '', 'message')
  const div = document.createElement('div')
  div.className = 'message message--agent'
  div.dataset.msgId = streamState.messageId
  div.innerHTML = `
    <div class="message-avatar">${avatar}</div>
    <div class="message-group">
      ${renderStreamingBubbleHTML(bubbleID)}
      <div class="message-meta">
        <span class="message-time">${formatTimestamp(startedAt)}</span>
      </div>
    </div>`

  listEl.appendChild(div)
  activeStreamMsgId = streamState.messageId

  const buffered = streamBufferByThread.get(thread.threadId) ?? ''
  if (buffered) {
    updateStreamingBubbleContent(streamState.messageId, buffered)
  }
  listEl.scrollTop = listEl.scrollHeight
}

function clearThreadStreamRuntime(threadId: string): void {
  streamsByThread.delete(threadId)
  streamBufferByThread.delete(threadId)
  streamStartedAtByThread.delete(threadId)
  setThreadStreamState(threadId, null)
  if (store.get().activeThreadId === threadId) {
    activeStreamMsgId = null
  }
}

function upsertPendingPermission(threadId: string, event: PermissionRequiredPayload): PendingPermission {
  let byID = pendingPermissionsByThread.get(threadId)
  if (!byID) {
    byID = new Map<string, PendingPermission>()
    pendingPermissionsByThread.set(threadId, byID)
  }
  const existing = byID.get(event.permissionId)
  if (existing) return existing

  const pending: PendingPermission = {
    ...event,
    deadlineMs: Date.now() + PERMISSION_TIMEOUT_MS,
  }
  byID.set(event.permissionId, pending)
  return pending
}

function removePendingPermission(threadId: string, permissionId: string): void {
  const byID = pendingPermissionsByThread.get(threadId)
  if (!byID) return
  byID.delete(permissionId)
  if (byID.size === 0) {
    pendingPermissionsByThread.delete(threadId)
  }
}

function clearPendingPermissions(threadId: string): void {
  pendingPermissionsByThread.delete(threadId)
}

function mountPendingPermissionCard(threadId: string, pending: PendingPermission): void {
  if (store.get().activeThreadId !== threadId) return
  if (document.getElementById(`perm-card-${pending.permissionId}`)) return

  const listEl = document.getElementById('message-list')
  if (!listEl) return

  mountPermissionCard(listEl, pending, {
    deadlineMs: pending.deadlineMs,
    onResolved: () => removePendingPermission(threadId, pending.permissionId),
  })
}

function renderPendingPermissionCards(threadId: string): void {
  const byID = pendingPermissionsByThread.get(threadId)
  if (!byID) return
  byID.forEach(pending => mountPendingPermissionCard(threadId, pending))
}

// ── Thread list rendering ─────────────────────────────────────────────────

function skeletonItems(): string {
  return Array.from({ length: 3 }, () => `
    <div class="thread-skeleton">
      <div class="skeleton thread-skeleton-avatar"></div>
      <div class="thread-skeleton-lines">
        <div class="skeleton thread-skeleton-line" style="width:70%"></div>
        <div class="skeleton thread-skeleton-line" style="width:50%"></div>
      </div>
    </div>`).join('')
}

function threadTitle(t: Thread): string {
  if (t.title) return t.title
  return t.cwd.split('/').filter(Boolean).pop() ?? t.cwd
}

type ConfigPickerState = 'loading' | 'empty' | 'ready'

interface ConfigPickerOption {
  value: string
  name: string
  description: string
}

interface ConfigPickerLabels {
  loadingLabel: string
  emptyLabel: string
}

interface ConfigPickerData {
  state: ConfigPickerState
  configId: string
  selectedValue: string
  selectedLabel: string
  options: ConfigPickerOption[]
}

function findReasoningOption(options: ConfigOption[]): ConfigOption | null {
  for (const option of options) {
    const category = option.category?.trim().toLowerCase() ?? ''
    const id = option.id.trim().toLowerCase()
    if (category === 'reasoning' || id === 'reasoning') {
      return option
    }
  }
  return null
}

function fallbackThreadConfigValue(thread: Thread, configId: string): string {
  const trimmedConfigID = configId.trim()
  if (!trimmedConfigID) return ''
  if (trimmedConfigID.toLowerCase() === 'model') {
    return fallbackThreadModelID(thread)
  }

  const rawOverrides = thread.agentOptions?.configOverrides
  if (!rawOverrides || typeof rawOverrides !== 'object') return ''
  const value = (rawOverrides as Record<string, unknown>)[trimmedConfigID]
  return typeof value === 'string' ? value.trim() : ''
}

function currentValueForConfig(options: ConfigOption[], configId: string): string {
  const trimmedConfigID = configId.trim()
  if (!trimmedConfigID) return ''
  const option = options.find(item => item.id === trimmedConfigID)
  return option?.currentValue?.trim() ?? ''
}

function getThreadConfigOptionsForRender(thread: Thread): ConfigOption[] {
  const threadOptions = threadConfigCache.get(thread.threadId) ?? []
  const agentCatalog = getAgentConfigCatalog(thread.agent ?? '', fallbackThreadModelID(thread))

  if (!agentCatalog.length) {
    return cloneConfigOptions(threadOptions)
  }

  const merged: ConfigOption[] = []
  const seen = new Set<string>()

  for (const catalogOption of agentCatalog) {
    const configId = catalogOption.id.trim()
    if (!configId) continue
    seen.add(configId)

    const currentValue = currentValueForConfig(threadOptions, configId) || fallbackThreadConfigValue(thread, configId)
    merged.push({
      ...catalogOption,
      currentValue,
      options: [...(catalogOption.options ?? [])],
    })
  }

  for (const threadOption of threadOptions) {
    const configId = threadOption.id.trim()
    if (!configId || seen.has(configId)) continue
    merged.push({
      ...threadOption,
      options: [...(threadOption.options ?? [])],
    })
  }

  return merged
}

function resolveConfigPickerData(
  configOption: ConfigOption | null,
  fallbackValue: string,
  loading: boolean,
  labels: ConfigPickerLabels,
): ConfigPickerData {
  if (loading) {
    return {
      state: 'loading',
      configId: configOption?.id?.trim() ?? '',
      selectedValue: '',
      selectedLabel: labels.loadingLabel,
      options: [],
    }
  }

  const rawOptions = configOption?.options ?? []
  const options: ConfigPickerOption[] = rawOptions
    .map(option => ({
      value: option.value.trim(),
      name: (option.name || option.value).trim() || option.value.trim(),
      description: option.description?.trim() || '',
    }))
    .filter(option => !!option.value)

  if (!options.length) {
    if (fallbackValue) {
      return {
        state: 'ready',
        configId: configOption?.id?.trim() ?? '',
        selectedValue: fallbackValue,
        selectedLabel: fallbackValue,
        options: [{ value: fallbackValue, name: fallbackValue, description: '' }],
      }
    }
    return {
      state: 'empty',
      configId: configOption?.id?.trim() ?? '',
      selectedValue: '',
      selectedLabel: labels.emptyLabel,
      options: [],
    }
  }

  const selectedValue = configOption?.currentValue?.trim() || fallbackValue || options[0].value
  const selectedOption = options.find(option => option.value === selectedValue) ?? options[0]
  return {
    state: 'ready',
    configId: configOption?.id?.trim() ?? '',
    selectedValue: selectedOption.value,
    selectedLabel: selectedOption.name,
    options,
  }
}

function renderConfigMenuOptions(
  options: ConfigPickerOption[],
  selectedValue: string,
  state: ConfigPickerState,
  labels: ConfigPickerLabels,
): string {
  if (state === 'loading') {
    return `<div class="thread-model-option-item thread-model-option-item--disabled">
      <div class="thread-model-option-name">${escHtml(labels.loadingLabel)}</div>
    </div>`
  }
  if (state === 'empty' || !options.length) {
    return `<div class="thread-model-option-item thread-model-option-item--disabled">
      <div class="thread-model-option-name">${escHtml(labels.emptyLabel)}</div>
    </div>`
  }

  return options.map(option => {
    const activeClass = option.value === selectedValue ? ' thread-model-option-item--active' : ''
    const descHTML = option.description
      ? `<div class="thread-model-option-desc">${escHtml(option.description)}</div>`
      : ''
    return `<button
      class="thread-model-option-item${activeClass}"
      type="button"
      data-value="${escHtml(option.value)}"
      role="option"
      aria-selected="${option.value === selectedValue ? 'true' : 'false'}"
    >
      <div class="thread-model-option-name">${escHtml(option.name)}</div>
      ${descHTML}
    </button>`
  }).join('')
}

function buildThreadAgentOptions(
  base: Record<string, unknown>,
  options: ConfigOption[],
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...base }
  const modelValue = findModelOption(options)?.currentValue?.trim() ?? ''
  if (modelValue) {
    next.modelId = modelValue
  } else {
    delete next.modelId
  }

  const configOverrides: Record<string, string> = {}
  for (const option of options) {
    const configId = option.id.trim()
    if (!configId || configId.toLowerCase() === 'model') continue
    const value = option.currentValue?.trim() ?? ''
    if (!value) continue
    configOverrides[configId] = value
  }
  if (Object.keys(configOverrides).length) {
    next.configOverrides = configOverrides
  } else {
    delete next.configOverrides
  }
  return next
}

function renderComposerConfigSwitch(
  key: 'model' | 'reasoning',
  label: string,
  pickerData: ConfigPickerData,
  labels: ConfigPickerLabels,
  disabled: boolean,
): string {
  return `
    <div class="thread-model-switch thread-model-switch--composer" data-picker-key="${escHtml(key)}">
      <button
        id="thread-${escHtml(key)}-trigger"
        class="thread-model-trigger"
        type="button"
        data-state="${escHtml(pickerData.state)}"
        data-selected-value="${escHtml(pickerData.selectedValue)}"
        data-config-id="${escHtml(pickerData.configId)}"
        aria-haspopup="listbox"
        aria-expanded="false"
        aria-label="${escHtml(label)}"
        ${disabled || pickerData.state !== 'ready' ? 'disabled' : ''}
      >
        <span class="thread-model-trigger-copy">
          <span class="thread-model-trigger-value">${escHtml(pickerData.selectedLabel)}</span>
        </span>
        <span class="thread-model-trigger-arrow">▾</span>
      </button>
      <div class="thread-model-menu" id="thread-${escHtml(key)}-menu" role="listbox" hidden>
        ${renderConfigMenuOptions(pickerData.options, pickerData.selectedValue, pickerData.state, labels)}
      </div>
    </div>`
}

const modelPickerLabels: ConfigPickerLabels = {
  loadingLabel: 'Loading models…',
  emptyLabel: 'No models available',
}

const reasoningPickerLabels: ConfigPickerLabels = {
  loadingLabel: 'Loading reasoning…',
  emptyLabel: 'No reasoning',
}

function renderAgentAvatar(agentId: string, variant: 'thread' | 'message'): string {
  const normalized = (agentId || '').trim().toLowerCase()
  const cls = variant === 'thread' ? 'thread-item-avatar-icon' : 'message-avatar-icon'
  if (normalized === 'codex') {
    return `<img src="${codexIconURL}" alt="Codex" class="${cls}" loading="lazy" decoding="async">`
  }
  if (normalized === 'gemini') {
    return `<img src="${geminiIconURL}" alt="Gemini CLI" class="${cls}" loading="lazy" decoding="async">`
  }
  if (normalized === 'claude') {
    return `<img src="${claudeIconURL}" alt="Claude Code" class="${cls}" loading="lazy" decoding="async">`
  }
  if (normalized === 'opencode') {
    return `<img src="${opencodeIconURL}" alt="OpenCode" class="${cls} ${cls}--contain" loading="lazy" decoding="async">`
  }
  if (normalized === 'qwen') {
    return `<img src="${qwenIconURL}" alt="Qwen Code" class="${cls}" loading="lazy" decoding="async">`
  }
  return escHtml((agentId || 'A').slice(0, 1).toUpperCase())
}

type ThreadActivityIndicator = 'loading' | 'done' | null

function renderThreadStatusIndicator(status: ThreadActivityIndicator): string {
  if (status === 'loading') {
    return `
      <span
        class="thread-status-indicator thread-status-indicator--loading"
        role="status"
        aria-label="Thread is working"
        title="Thread is working"
      >
        <span class="thread-status-spinner" aria-hidden="true"></span>
      </span>`
  }
  if (status === 'done') {
    return `
      <span
        class="thread-status-indicator thread-status-indicator--done"
        role="img"
        aria-label="Latest turn finished"
        title="Latest turn finished"
      >
        ${iconCheck}
      </span>`
  }
  return ''
}

function renderThreadItem(
  t: Thread,
  activeId: string | null,
  query: string,
  activityIndicator: ThreadActivityIndicator,
): string {
  const isActive = t.threadId === activeId
  const isMenuOpen = openThreadActionMenuId === t.threadId
  const avatar = renderAgentAvatar(t.agent ?? '', 'thread')
  const displayTitle = threadTitle(t)
  const relTime = t.updatedAt ? formatRelativeTime(t.updatedAt) : ''

  const titleHtml = query
    ? escHtml(displayTitle).replace(
        new RegExp(`(${escHtml(query)})`, 'gi'),
        '<mark>$1</mark>',
      )
    : escHtml(displayTitle)

  return `
    <div class="thread-item ${isActive ? 'thread-item--active' : ''} ${isMenuOpen ? 'thread-item--menu-open' : ''}"
         data-thread-id="${escHtml(t.threadId)}"
         role="button"
         tabindex="0"
         aria-label="${escHtml(displayTitle)}">
      <div class="thread-item-avatar ${isActive ? '' : 'thread-item-avatar--inactive'}">${avatar}</div>
      <div class="thread-item-body">
        <div class="thread-item-title">${titleHtml}</div>
        <div class="thread-item-preview">${escHtml(t.cwd)}</div>
        <div class="thread-item-foot">
          <span class="badge badge--agent">${escHtml(t.agent ?? '')}</span>
          <span class="thread-item-time">${relTime}</span>
        </div>
      </div>
      <div class="thread-item-actions">
        ${renderThreadStatusIndicator(activityIndicator)}
        <button class="btn btn-ghost btn-sm thread-item-menu-trigger" type="button"
                data-thread-id="${escHtml(t.threadId)}"
                aria-expanded="${isMenuOpen ? 'true' : 'false'}"
                aria-label="Thread actions">
          ...
        </button>
      </div>
    </div>`
}

function updateThreadList(): void {
  const el = document.getElementById('thread-list')
  if (!el) return

  const { threads, activeThreadId, searchQuery, streamStates, threadCompletionBadges } = store.get()
  const q        = searchQuery.trim().toLowerCase()
  const filtered = q
    ? threads.filter(t =>
        (t.title || t.cwd).toLowerCase().includes(q) || threadTitle(t).toLowerCase().includes(q) || t.cwd.toLowerCase().includes(q),
      )
    : threads

  if (!filtered.length) {
    el.innerHTML = `
      <div class="thread-list-empty">
        ${q ? `No threads matching "<strong>${escHtml(q)}</strong>"` : 'No threads yet.<br>Click <strong>+</strong> to start one.'}
      </div>`
    renderThreadActionLayer()
    return
  }

  el.innerHTML = filtered
    .map(t => {
      const isActive = t.threadId === activeThreadId
      const activityIndicator: ThreadActivityIndicator = streamStates[t.threadId]
        ? 'loading'
        : (!isActive && threadCompletionBadges[t.threadId] ? 'done' : null)
      return renderThreadItem(t, activeThreadId, q, activityIndicator)
    })
    .join('')

  el.querySelectorAll<HTMLButtonElement>('.thread-item-menu-trigger').forEach(btn => {
    btn.addEventListener('click', e => {
      e.preventDefault()
      e.stopPropagation()
      const id = btn.dataset.threadId ?? ''
      if (!id) return
      toggleThreadActionMenu(id)
    })
    btn.addEventListener('keydown', e => e.stopPropagation())
  })

  el.querySelectorAll<HTMLElement>('.thread-item').forEach(item => {
    const handler = (event?: Event) => {
      const target = event?.target as HTMLElement | null
      if (target?.closest('.thread-item-menu-trigger') || target?.closest('.thread-action-popover')) return
      const id = item.dataset.threadId ?? ''
      activateThread(id)
      // Close mobile sidebar on thread select
      document.getElementById('sidebar')?.classList.remove('sidebar--open')
    }
    item.addEventListener('click', handler)
    item.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') handler(e)
    })
  })

  renderThreadActionLayer()
}

async function handleRenameThread(threadId: string, nextTitle: string): Promise<void> {
  const snapshot = store.get()
  const thread = snapshot.threads.find(t => t.threadId === threadId)
  if (!thread) return

  const title = nextTitle.trim()
  if (title === thread.title) {
    cancelThreadRename(threadId)
    return
  }

  let updatedThread: Thread
  try {
    updatedThread = await api.updateThread(threadId, { title })
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Unknown error'
    window.alert(`Failed to rename thread: ${message}`)
    return
  }

  resetThreadActionMenuState()
  const state = store.get()
  store.set({
    threads: state.threads.map(item => (item.threadId === threadId ? updatedThread : item)),
  })
  if (state.activeThreadId === threadId) {
    updateChatArea()
  }
}

async function handleDeleteThread(threadId: string): Promise<void> {
  const snapshot = store.get()
  const thread = snapshot.threads.find(t => t.threadId === threadId)
  if (!thread) return

  const label = threadTitle(thread)
  if (!window.confirm(`Delete thread "${label}"? This will permanently remove its history.`)) return

  try {
    await api.deleteThread(threadId)
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Unknown error'
    window.alert(`Failed to delete thread: ${message}`)
    return
  }

  resetThreadActionMenuState()
  const state = store.get()
  const nextThreads = state.threads.filter(t => t.threadId !== threadId)
  const nextMessages = { ...state.messages }
  delete nextMessages[threadId]

  const deletingActive = state.activeThreadId === threadId
  const nextActiveThreadId = deletingActive ? (nextThreads[0]?.threadId ?? null) : state.activeThreadId
  const deletingStream = !!streamsByThread.get(threadId)

  if (deletingStream) {
    streamsByThread.get(threadId)?.abort()
    clearThreadStreamRuntime(threadId)
  }
  clearPendingPermissions(threadId)
  threadConfigCache.delete(threadId)
  threadConfigSwitching.delete(threadId)
  let nextThreadCompletionBadges = omitThreadCompletionBadge(state.threadCompletionBadges, threadId)
  if (nextActiveThreadId) {
    nextThreadCompletionBadges = omitThreadCompletionBadge(nextThreadCompletionBadges, nextActiveThreadId)
  }

  store.set({
    threads: nextThreads,
    messages: nextMessages,
    activeThreadId: nextActiveThreadId,
    threadCompletionBadges: nextThreadCompletionBadges,
  })
}

// ── History helpers ───────────────────────────────────────────────────────

/** Convert server Turn[] to the client Message[] model. */
function turnsToMessages(turns: Turn[]): Message[] {
  const msgs: Message[] = []
  for (const t of turns) {
    if (t.isInternal) continue

    if (t.requestText) {
      msgs.push({
        id:        `${t.turnId}-u`,
        role:      'user',
        content:   t.requestText,
        timestamp: t.createdAt,
        status:    'done',
        turnId:    t.turnId,
      })
    }

    if (t.status !== 'running') {
      const agentStatus: Message['status'] =
        t.status === 'cancelled' ? 'cancelled' :
        t.status === 'error'     ? 'error'     :
        'done'

      msgs.push({
        id:           `${t.turnId}-a`,
        role:         'agent',
        content:      t.responseText,
        timestamp:    t.completedAt || t.createdAt,
        status:       agentStatus,
        turnId:       t.turnId,
        stopReason:   t.stopReason   || undefined,
        errorMessage: t.errorMessage || undefined,
      })
    }
  }
  return msgs
}

async function loadHistory(threadId: string): Promise<void> {
  try {
    const turns = await api.getHistory(threadId)
    const state = store.get()
    if (state.activeThreadId !== threadId) return
    // Don't overwrite while a turn is streaming on this thread
    if (getThreadStreamState(threadId)) return
    store.set({ messages: { ...state.messages, [threadId]: turnsToMessages(turns) } })
  } catch {
    if (store.get().activeThreadId !== threadId) return
    // Show error only if no messages were already rendered (empty thread)
    if (!store.get().messages[threadId]?.length) {
      const listEl = document.getElementById('message-list')
      if (listEl) {
        listEl.innerHTML = `<div class="thread-list-empty" style="color:var(--error)">Failed to load history.</div>`
      }
    }
  }
}

// ── Message rendering ─────────────────────────────────────────────────────

function renderMessage(msg: Message, agentAvatar: string): string {
  const renderMessageCopyBtn = (text: string): string => `
    <button
      class="msg-copy-btn"
      data-copy-text="${escHtml(encodeURIComponent(text))}"
      title="Copy message"
      aria-label="Copy message"
      type="button"
    >⎘</button>`

  if (msg.role === 'user') {
    const copyBtn = renderMessageCopyBtn(msg.content)
    return `
      <div class="message message--user" data-msg-id="${escHtml(msg.id)}">
        <div class="message-group">
          <div class="message-bubble">${escHtml(msg.content)}</div>
          <div class="message-meta">
            <span class="message-time">${formatTimestamp(msg.timestamp)}</span>
            ${copyBtn}
          </div>
        </div>
      </div>`
  }

  const isCancelled = msg.status === 'cancelled'
  const isError     = msg.status === 'error'
  const isDone      = msg.status === 'done'

  const bodyText = isError
    ? (msg.errorCode ? `[${msg.errorCode}] ` : '') + (msg.errorMessage ?? 'Unknown error')
    : (msg.content || '…')

  // Render markdown only for finalised done messages
  let bubbleExtra = ''
  let bubbleContent: string
  if (isDone) {
    bubbleExtra   = ' message-bubble--md'
    bubbleContent = renderMarkdown(bodyText)
  } else if (isError) {
    bubbleExtra   = ' message-bubble--error'
    bubbleContent = escHtml(bodyText)
  } else {
    bubbleExtra   = ' message-bubble--cancelled'
    bubbleContent = escHtml(bodyText)
  }

  const stopTag  = isCancelled ? `<span class="message-stop-reason">Cancelled</span>` : ''
  const copyBtn  = isDone      ? renderMessageCopyBtn(bodyText) : ''

  return `
    <div class="message message--agent" data-msg-id="${escHtml(msg.id)}">
      <div class="message-avatar">${agentAvatar}</div>
      <div class="message-group">
        <div class="message-bubble${bubbleExtra}">${bubbleContent}</div>
        <div class="message-meta">
          <span class="message-time">${formatTimestamp(msg.timestamp)}</span>
          ${stopTag}
          ${copyBtn}
        </div>
      </div>
    </div>`
}

function renderStreamingBubbleHTML(bubbleID: string, content = ''): string {
  return `
    <div class="message-bubble message-bubble--streaming" id="${escHtml(bubbleID)}">
      <div class="message-bubble__text">${escHtml(content)}</div>
      <div class="typing-indicator" aria-hidden="true"><span></span><span></span><span></span></div>
    </div>`
}

function updateStreamingBubbleContent(messageID: string, content: string): void {
  const bubbleEl = document.getElementById(`bubble-${messageID}`)
  if (!bubbleEl) return
  const contentEl = bubbleEl.querySelector('.message-bubble__text')
  if (!contentEl) return
  contentEl.textContent = content
}

function updateMessageList(): void {
  const listEl = document.getElementById('message-list')
  if (!listEl) return

  const { activeThreadId, threads, messages } = store.get()
  if (!activeThreadId) return

  const thread   = threads.find(t => t.threadId === activeThreadId)
  const msgs     = messages[activeThreadId] ?? []
  const agentAvatar = renderAgentAvatar(thread?.agent ?? '', 'message')

  if (!msgs.length) {
    listEl.innerHTML = `
      <div class="empty-state">
        <div class="empty-state-icon" style="font-size:28px">💬</div>
        <h3 class="empty-state-title" style="font-size:var(--font-size-lg)">Start the conversation</h3>
        <p class="empty-state-desc">Send your first message to begin working with ${escHtml(thread?.agent ?? 'the agent')}.</p>
      </div>`
    return
  }

  listEl.innerHTML = msgs.map(m => renderMessage(m, agentAvatar)).join('')
  bindMarkdownControls(listEl)
  listEl.scrollTop = listEl.scrollHeight
  // Sync scroll button (we just moved to the bottom)
  const scrollBtn = document.getElementById('scroll-bottom-btn')
  if (scrollBtn) scrollBtn.style.display = 'none'
}

// ── Input state ───────────────────────────────────────────────────────────

function updateInputState(): void {
  const { activeThreadId } = store.get()
  const streamState = getThreadStreamState(activeThreadId)
  const isStreaming   = !!streamState
  const isCancelling  = streamState?.status === 'cancelling'

  const sendBtn  = document.getElementById('send-btn')   as HTMLButtonElement   | null
  const cancelBtn = document.getElementById('cancel-btn') as HTMLButtonElement   | null
  const inputEl  = document.getElementById('message-input') as HTMLTextAreaElement | null
  const isSwitchingConfig = !!activeThreadId && threadConfigSwitching.has(activeThreadId)

  if (sendBtn)  sendBtn.disabled  = isStreaming || isSwitchingConfig
  if (inputEl)  inputEl.disabled  = isStreaming || isSwitchingConfig
  document.querySelectorAll<HTMLButtonElement>('.thread-model-trigger').forEach(triggerEl => {
    const pickerState = triggerEl.dataset.state ?? 'empty'
    const configID = triggerEl.dataset.configId?.trim() ?? ''
    const noSelectableValue = pickerState !== 'ready' || !configID
    const disabled = isStreaming || isSwitchingConfig || noSelectableValue
    triggerEl.disabled = disabled
    if (disabled) {
      triggerEl.setAttribute('aria-expanded', 'false')
      const menu = triggerEl.parentElement?.querySelector<HTMLElement>('.thread-model-menu')
      menu?.setAttribute('hidden', 'true')
    }
  })
  if (cancelBtn) {
    cancelBtn.style.display = isStreaming ? '' : 'none'
    cancelBtn.disabled      = isCancelling
    cancelBtn.textContent   = isCancelling ? 'Cancelling…' : 'Cancel'
  }
}

// ── Chat area rendering ───────────────────────────────────────────────────

function renderChatEmpty(): string {
  return `
    <div class="empty-state">
      <div class="empty-state-icon">◈</div>
      <h3 class="empty-state-title">No thread selected</h3>
      <p class="empty-state-desc">
        Select a thread from the sidebar, or create a new one to start chatting with an agent.
      </p>
      <button class="btn btn-primary" id="new-thread-empty-btn">
        ${iconPlus} New Thread
      </button>
    </div>`
}

function renderChatThread(t: Thread): string {
  const titleLabel   = threadTitle(t)
  const createdLabel = t.createdAt ? `Created ${formatTimestamp(t.createdAt)}` : ''
  const selectedModelID = fallbackThreadModelID(t)
  const catalogKey = normalizeAgentConfigCatalogKey(t.agent ?? '', selectedModelID)
  const hasConfigCache = threadConfigCache.has(t.threadId) || hasAgentConfigCatalog(t.agent ?? '', selectedModelID)
  const loadingConfig = !hasConfigCache || (!!catalogKey && agentConfigCatalogInFlight.has(catalogKey))
  const configOptions = getThreadConfigOptionsForRender(t)
  const modelOption = findModelOption(configOptions)
  const reasoningOption = findReasoningOption(configOptions)
  const modelPickerData = resolveConfigPickerData(
    modelOption,
    fallbackThreadConfigValue(t, 'model'),
    loadingConfig,
    modelPickerLabels,
  )
  const reasoningPickerData = resolveConfigPickerData(
    reasoningOption,
    reasoningOption ? fallbackThreadConfigValue(t, reasoningOption.id) : '',
    loadingConfig,
    reasoningPickerLabels,
  )
  const isSwitching = threadConfigSwitching.has(t.threadId)

  return `
    <div class="chat-header">
      <div class="chat-header-left">
        <button class="btn btn-icon mobile-menu-btn" aria-label="Open menu">${iconMenu}</button>
        <h2 class="chat-title" title="${escHtml(titleLabel)}">${escHtml(titleLabel)}</h2>
        <span class="badge badge--agent">${escHtml(t.agent ?? '')}</span>
        <span class="chat-cwd" title="${escHtml(t.cwd)}">${escHtml(t.cwd)}</span>
      </div>
      <div class="chat-header-right">
        <button class="btn btn-sm btn-danger" id="cancel-btn" style="display:none" aria-label="Cancel turn">Cancel</button>
        <span class="chat-header-meta">${escHtml(createdLabel)}</span>
      </div>
    </div>

    <div class="message-list-wrap">
      <div class="message-list" id="message-list"></div>
      <button class="scroll-bottom-btn" id="scroll-bottom-btn"
              aria-label="Scroll to bottom" style="display:none">↓</button>
    </div>

    <div class="input-area">
      <div class="input-wrapper">
        <textarea
          id="message-input"
          class="message-input"
          placeholder="Type a message…"
          rows="1"
          aria-label="Message input"
        ></textarea>
        <div class="input-compose-bar">
          <div class="thread-config-switches">
            ${renderComposerConfigSwitch('model', 'Model', modelPickerData, modelPickerLabels, isSwitching)}
            ${renderComposerConfigSwitch('reasoning', 'Reasoning', reasoningPickerData, reasoningPickerLabels, isSwitching)}
          </div>
          <button class="btn btn-primary btn-send" id="send-btn" aria-label="Send message">
            ${iconSend}
          </button>
        </div>
      </div>
      <div class="input-hint">Press <kbd>⌘ Enter</kbd> to send · <kbd>Esc</kbd> to cancel · <kbd>/</kbd> to search</div>
    </div>`
}

function updateChatArea(): void {
  const chat = document.getElementById('chat')
  if (!chat) return

  const { threads, activeThreadId } = store.get()
  const thread = activeThreadId ? threads.find(t => t.threadId === activeThreadId) : null

  // The streaming bubble is tied to the current chat DOM; reset sentinel on thread switch.
  activeStreamMsgId = null

  if (!thread) {
    chat.innerHTML = renderChatEmpty()
    document.getElementById('new-thread-empty-btn')?.addEventListener('click', openNewThread)
    document.querySelector('.mobile-menu-btn')?.addEventListener('click', () => {
      document.getElementById('sidebar')?.classList.toggle('sidebar--open')
    })
    return
  }

  chat.innerHTML = renderChatThread(thread)
  document.querySelector('.mobile-menu-btn')?.addEventListener('click', () => {
    document.getElementById('sidebar')?.classList.toggle('sidebar--open')
  })

  // Show locally loaded messages immediately (including empty threads).
  // Only show spinner when history for this thread has never been loaded.
  const hasLocalHistory = Object.prototype.hasOwnProperty.call(store.get().messages, thread.threadId)
  if (hasLocalHistory) {
    updateMessageList()
  } else {
    const listEl = document.getElementById('message-list')
    if (listEl) {
      listEl.innerHTML = `<div class="message-list-loading"><div class="loading-spinner"></div></div>`
    }
  }

  appendOrRestoreStreamingBubble(thread)
  renderPendingPermissionCards(thread.threadId)

  updateInputState()
  bindInputResize()
  bindSendHandler()
  bindCancelHandler()
  bindThreadConfigSwitches(thread)
  bindScrollBottom()

  // Always reload history from server (keeps view fresh; guards against overwrites during streaming)
  void loadHistory(thread.threadId)
}

function bindThreadConfigSwitches(thread: Thread): void {
  const switchEls = Array.from(document.querySelectorAll<HTMLElement>('.thread-model-switch[data-picker-key]'))
  if (!switchEls.length) return

  const closeMenu = (switchEl: HTMLElement): void => {
    const triggerEl = switchEl.querySelector<HTMLButtonElement>('.thread-model-trigger')
    const menuEl = switchEl.querySelector<HTMLElement>('.thread-model-menu')
    triggerEl?.setAttribute('aria-expanded', 'false')
    menuEl?.setAttribute('hidden', 'true')
  }

  const closeAllMenus = (): void => {
    switchEls.forEach(closeMenu)
  }

  const renderConfigUI = (): void => {
    const latest = store.get().threads.find(item => item.threadId === thread.threadId)
    if (!latest) return

    const selectedModelID = fallbackThreadModelID(latest)
    const catalogKey = normalizeAgentConfigCatalogKey(latest.agent ?? '', selectedModelID)
    const loading = (!threadConfigCache.has(thread.threadId) && !hasAgentConfigCatalog(latest.agent ?? '', selectedModelID))
      || (!!catalogKey && agentConfigCatalogInFlight.has(catalogKey))
    const options = getThreadConfigOptionsForRender(latest)
    const modelOption = findModelOption(options)
    const reasoningOption = findReasoningOption(options)
    const pickerDataByKey = {
      model: resolveConfigPickerData(modelOption, fallbackThreadConfigValue(latest, 'model'), loading, modelPickerLabels),
      reasoning: resolveConfigPickerData(
        reasoningOption,
        reasoningOption ? fallbackThreadConfigValue(latest, reasoningOption.id) : '',
        loading,
        reasoningPickerLabels,
      ),
    } as const
    const labelsByKey = {
      model: modelPickerLabels,
      reasoning: reasoningPickerLabels,
    } as const

    switchEls.forEach(switchEl => {
      const key = (switchEl.dataset.pickerKey === 'reasoning' ? 'reasoning' : 'model')
      const triggerEl = switchEl.querySelector<HTMLButtonElement>('.thread-model-trigger')
      const menuEl = switchEl.querySelector<HTMLDivElement>('.thread-model-menu')
      if (!triggerEl || !menuEl) return

      const pickerData = pickerDataByKey[key]
      const labels = labelsByKey[key]
      const isReady = pickerData.state === 'ready'
      const disabled = loading || threadConfigSwitching.has(thread.threadId) || !isReady || !pickerData.configId

      triggerEl.dataset.state = pickerData.state
      triggerEl.dataset.selectedValue = pickerData.selectedValue
      triggerEl.dataset.configId = pickerData.configId
      triggerEl.disabled = disabled
      const valueEl = triggerEl.querySelector<HTMLElement>('.thread-model-trigger-value')
      if (valueEl) valueEl.textContent = pickerData.selectedLabel
      menuEl.innerHTML = renderConfigMenuOptions(pickerData.options, pickerData.selectedValue, pickerData.state, labels)
      if (!isReady || disabled) {
        closeMenu(switchEl)
      }
    })
  }

  const setSwitching = (switching: boolean): void => {
    if (switching) {
      threadConfigSwitching.add(thread.threadId)
      closeAllMenus()
    } else {
      threadConfigSwitching.delete(thread.threadId)
    }
    if (store.get().activeThreadId === thread.threadId) {
      updateInputState()
    }
  }

  const switchConfig = async (configId: string, nextValue: string): Promise<void> => {
    const activeThreadID = store.get().activeThreadId
    if (!activeThreadID || activeThreadID !== thread.threadId) return
    if (getThreadStreamState(activeThreadID)) return

    const latest = store.get().threads.find(item => item.threadId === activeThreadID)
    if (!latest) return

    configId = configId.trim()
    nextValue = nextValue.trim()
    if (!configId || !nextValue) return

    const currentOption = getThreadConfigOptionsForRender(latest).find(option => option.id === configId)
    const currentValue = currentOption?.currentValue?.trim()
      || fallbackThreadConfigValue(latest, configId)
    if (nextValue === currentValue) return

    setSwitching(true)
    try {
      const updatedOptions = await api.setThreadConfigOption(activeThreadID, {
        configId,
        value: nextValue,
      })
      const nextModelID = findModelOption(updatedOptions)?.currentValue?.trim() ?? fallbackThreadModelID(latest)
      const normalized = cacheThreadConfigOptions(latest, updatedOptions, nextModelID)
      const { threads } = store.get()
      store.set({
        threads: threads.map(item => (
          item.threadId === activeThreadID
            ? { ...item, agentOptions: buildThreadAgentOptions(item.agentOptions, normalized) }
            : item
        )),
      })
      renderConfigUI()
    } catch (err) {
      renderConfigUI()
      const message = err instanceof Error ? err.message : String(err)
      const targetLabel = configId.toLowerCase() === 'model' ? 'model' : 'config option'
      window.alert(`Failed to update ${targetLabel}: ${message}`)
    } finally {
      setSwitching(false)
      renderConfigUI()
    }
  }

  renderConfigUI()
  if (!threadConfigCache.has(thread.threadId) && !hasAgentConfigCatalog(thread.agent ?? '', fallbackThreadModelID(thread))) {
    void loadThreadConfigOptions(thread.threadId)
      .then(() => {
        if (store.get().activeThreadId !== thread.threadId) return
        renderConfigUI()
        updateInputState()
      })
      .catch(err => {
        if (store.get().activeThreadId !== thread.threadId) return
        renderConfigUI()
        const message = err instanceof Error ? err.message : String(err)
        window.alert(`Failed to load thread config options: ${message}`)
      })
  }

  switchEls.forEach(switchEl => {
    const triggerEl = switchEl.querySelector<HTMLButtonElement>('.thread-model-trigger')
    const menuEl = switchEl.querySelector<HTMLDivElement>('.thread-model-menu')
    if (!triggerEl || !menuEl) return

    const toggleMenu = (): void => {
      const expanded = triggerEl.getAttribute('aria-expanded') === 'true'
      if (expanded) {
        closeMenu(switchEl)
        return
      }
      if (triggerEl.disabled) return
      closeAllMenus()
      triggerEl.setAttribute('aria-expanded', 'true')
      menuEl.removeAttribute('hidden')
    }

    triggerEl.addEventListener('click', e => {
      e.preventDefault()
      toggleMenu()
    })

    menuEl.addEventListener('click', e => {
      const target = e.target as HTMLElement | null
      const optionBtn = target?.closest('.thread-model-option-item[data-value]') as HTMLButtonElement | null
      if (!optionBtn || optionBtn.disabled) return
      const configId = triggerEl.dataset.configId?.trim() ?? ''
      const nextValue = optionBtn.dataset.value?.trim() ?? ''
      closeMenu(switchEl)
      void switchConfig(configId, nextValue)
    })

    switchEl.addEventListener('focusout', e => {
      const related = e.relatedTarget as Node | null
      if (!related || !switchEl.contains(related)) {
        closeMenu(switchEl)
      }
    })

    const onEsc = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.preventDefault()
        closeMenu(switchEl)
        triggerEl.focus()
      }
    }
    triggerEl.addEventListener('keydown', onEsc)
    menuEl.addEventListener('keydown', onEsc)
  })
}

// ── Scroll-to-bottom button ───────────────────────────────────────────────

function bindScrollBottom(): void {
  const listEl = document.getElementById('message-list')
  const btnEl  = document.getElementById('scroll-bottom-btn') as HTMLButtonElement | null
  if (!listEl || !btnEl) return

  const syncBtn = () => {
    btnEl.style.display = isNearBottom(listEl) ? 'none' : ''
  }

  listEl.addEventListener('scroll', syncBtn, { passive: true })
  btnEl.addEventListener('click', () => {
    listEl.scrollTo({ top: listEl.scrollHeight, behavior: 'smooth' })
  })
}

// ── Input resize ──────────────────────────────────────────────────────────

function bindInputResize(): void {
  const input = document.getElementById('message-input') as HTMLTextAreaElement | null
  if (!input) return
  const maxHeight = 220
  input.addEventListener('input', () => {
    input.style.height = 'auto'
    input.style.height = Math.min(input.scrollHeight, maxHeight) + 'px'
  })
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      document.getElementById('send-btn')?.click()
    }
  })
}

// ── Send ──────────────────────────────────────────────────────────────────

function bindSendHandler(): void {
  document.getElementById('send-btn')?.addEventListener('click', handleSend)
}

function handleSend(): void {
  const inputEl = document.getElementById('message-input') as HTMLTextAreaElement | null
  if (!inputEl) return

  const text = inputEl.value.trim()
  if (!text) return

  const { activeThreadId, threads } = store.get()
  if (!activeThreadId || getThreadStreamState(activeThreadId)) return

  const thread       = threads.find(t => t.threadId === activeThreadId)
  const agentAvatar  = renderAgentAvatar(thread?.agent ?? '', 'message')
  const capturedThreadID = activeThreadId

  // Clear input immediately
  inputEl.value = ''
  inputEl.style.height = 'auto'

  const now = new Date().toISOString()

  // ── 1. Add user message (fires subscribe → updateMessageList renders it) ──
  const userMsg: Message = {
    id:        generateUUID(),
    role:      'user',
    content:   text,
    timestamp: now,
    status:    'done',
  }
  addMessageToStore(capturedThreadID, userMsg)

  // ── 2. Reserve streaming message ID before touching stream state ───────────
  //    This prevents subscribe → updateMessageList from wiping the bubble.
  const agentMsgID = generateUUID()
  activeStreamMsgId = agentMsgID
  streamBufferByThread.set(capturedThreadID, '')
  streamStartedAtByThread.set(capturedThreadID, now)
  setThreadStreamState(capturedThreadID, {
    turnId: '',
    threadId: capturedThreadID,
    messageId: agentMsgID,
    status: 'streaming',
  })

  // ── 4. Append streaming bubble directly to DOM ─────────────────────────────
  const listEl = document.getElementById('message-list')
  if (listEl) {
    listEl.querySelector('.empty-state')?.remove()
    const div = document.createElement('div')
    div.className        = 'message message--agent'
    div.dataset.msgId    = agentMsgID
    div.innerHTML = `
      <div class="message-avatar">${agentAvatar}</div>
      <div class="message-group">
        ${renderStreamingBubbleHTML(`bubble-${agentMsgID}`)}
        <div class="message-meta">
          <span class="message-time">${formatTimestamp(now)}</span>
        </div>
      </div>`
    listEl.appendChild(div)
    listEl.scrollTop = listEl.scrollHeight
  }

  // ── 5. Start SSE stream ────────────────────────────────────────────────────
  const stream = api.startTurn(capturedThreadID, text, {

    onTurnStarted({ turnId }) {
      const state = getThreadStreamState(capturedThreadID)
      if (!state) return
      setThreadStreamState(capturedThreadID, { ...state, turnId })
    },

    onDelta({ delta }) {
      const previous = streamBufferByThread.get(capturedThreadID) ?? ''
      const next = previous + delta
      streamBufferByThread.set(capturedThreadID, next)

      if (store.get().activeThreadId !== capturedThreadID) return
      const list      = document.getElementById('message-list')
      const atBottom  = !list || isNearBottom(list)
      updateStreamingBubbleContent(agentMsgID, next)
      if (atBottom && list) list.scrollTop = list.scrollHeight
    },

    onPermissionRequired(event) {
      const pending = upsertPendingPermission(capturedThreadID, event)
      mountPendingPermissionCard(capturedThreadID, pending)
    },

    onCompleted({ stopReason }) {
      // Clear stream tracking BEFORE addMessageToStore (so subscribe calls updateMessageList)
      const finalContent = streamBufferByThread.get(capturedThreadID) ?? ''
      clearThreadStreamRuntime(capturedThreadID)
      clearPendingPermissions(capturedThreadID)
      markThreadCompletionBadge(capturedThreadID)

      addMessageToStore(capturedThreadID, {
        id:         agentMsgID,
        role:       'agent',
        content:    finalContent,
        timestamp:  now,
        status:     stopReason === 'cancelled' ? 'cancelled' : 'done',
        stopReason,
      })
    },

    onError({ code, message: msg }) {
      const partialContent = streamBufferByThread.get(capturedThreadID) ?? ''
      clearThreadStreamRuntime(capturedThreadID)
      clearPendingPermissions(capturedThreadID)

      addMessageToStore(capturedThreadID, {
        id:           agentMsgID,
        role:         'agent',
        content:      partialContent,
        timestamp:    now,
        status:       'error',
        errorCode:    code,
        errorMessage: msg,
      })
    },

    onDisconnect() {
      const partialContent = streamBufferByThread.get(capturedThreadID) ?? ''
      clearThreadStreamRuntime(capturedThreadID)
      clearPendingPermissions(capturedThreadID)

      addMessageToStore(capturedThreadID, {
        id:           agentMsgID,
        role:         'agent',
        content:      partialContent,
        timestamp:    now,
        status:       'error',
        errorMessage: 'Connection lost',
      })
    },
  })

  streamsByThread.set(capturedThreadID, stream)
}

// ── Cancel ────────────────────────────────────────────────────────────────

function bindCancelHandler(): void {
  document.getElementById('cancel-btn')?.addEventListener('click', () => void handleCancel())
}

async function handleCancel(): Promise<void> {
  const { activeThreadId } = store.get()
  const streamState = getThreadStreamState(activeThreadId)
  if (!activeThreadId || !streamState?.turnId) return

  setThreadStreamState(activeThreadId, { ...streamState, status: 'cancelling' })
  try {
    await api.cancelTurn(streamState.turnId)
  } catch {
    // Ignore — stream will eventually deliver turn_completed with stopReason=cancelled
  }
}

// ── New thread ────────────────────────────────────────────────────────────

function openNewThread(): void {
  newThreadModal.open()
}

// ── Static layout shell ───────────────────────────────────────────────────

function renderShell(): void {
  const root = document.getElementById('app')
  if (!root) return

  root.innerHTML = `
    <div class="layout">
      <aside class="sidebar" id="sidebar">
        <div class="sidebar-header">
          <div class="sidebar-brand">
            <div class="sidebar-brand-icon">A</div>
            <span>Agent Hub</span>
          </div>
          <button class="btn btn-icon" id="new-thread-btn" title="New thread" aria-label="New thread">
            ${iconPlus}
          </button>
        </div>

        <div class="sidebar-search">
          <input
            id="search-input"
            class="search-input"
            type="search"
            placeholder="Search threads…"
            aria-label="Search threads"
          />
        </div>

        <div class="thread-list" id="thread-list">
          ${skeletonItems()}
        </div>

        <div class="sidebar-footer">
          <button class="btn btn-ghost sidebar-settings-btn" id="settings-btn">
            ${iconSettings} Settings
          </button>
        </div>

        <div class="thread-action-layer" id="thread-action-layer" hidden></div>
      </aside>

      <main class="chat" id="chat">
        ${renderChatEmpty()}
      </main>
    </div>`

  document.getElementById('settings-btn')?.addEventListener('click', () => settingsPanel.open())
  document.getElementById('new-thread-btn')?.addEventListener('click', openNewThread)
  document.getElementById('new-thread-empty-btn')?.addEventListener('click', openNewThread)

  const searchEl = document.getElementById('search-input') as HTMLInputElement | null
  searchEl?.addEventListener('input', () => {
    store.set({ searchQuery: searchEl.value })
    updateThreadList()
  })
}

// ── Global keyboard shortcuts ─────────────────────────────────────────────

function bindGlobalShortcuts(): void {
  document.addEventListener('keydown', e => {
    const active = document.activeElement as HTMLElement | null
    const inInput = active?.tagName === 'INPUT' || active?.tagName === 'TEXTAREA'

    // '/' — focus search input
    if (e.key === '/' && !inInput && !e.metaKey && !e.ctrlKey) {
      const searchEl = document.getElementById('search-input')
      if (searchEl) {
        e.preventDefault()
        searchEl.focus()
      }
      return
    }

    // Cmd+N / Ctrl+N — open new thread modal
    if (e.key === 'n' && (e.metaKey || e.ctrlKey) && !e.shiftKey) {
      e.preventDefault()
      openNewThread()
      return
    }

    // Escape — contextual (most-specific first)
    if (e.key === 'Escape') {
      // (1) close mobile sidebar if open
      const sidebar = document.getElementById('sidebar')
      if (sidebar?.classList.contains('sidebar--open')) {
        sidebar.classList.remove('sidebar--open')
        return
      }
      // (2) close thread action menu if open
      if (openThreadActionMenuId) {
        e.preventDefault()
        resetThreadActionMenuState()
        updateThreadList()
        return
      }
      // (3) clear search if focused
      const searchEl = document.getElementById('search-input') as HTMLInputElement | null
      if (searchEl && document.activeElement === searchEl) {
        searchEl.value = ''
        store.set({ searchQuery: '' })
        searchEl.blur()
        return
      }
      // (4) cancel active stream
      const { activeThreadId } = store.get()
      const streamState = getThreadStreamState(activeThreadId)
      if (streamState?.turnId) {
        void handleCancel()
      }
    }
  })
}

// ── Bootstrap ─────────────────────────────────────────────────────────────

async function init(): Promise<void> {
  renderShell()
  bindGlobalShortcuts()
  const repositionThreadActionLayer = (): void => {
    if (!openThreadActionMenuId) return
    renderThreadActionLayer()
  }
  document.getElementById('thread-list')?.addEventListener('scroll', repositionThreadActionLayer, { passive: true })
  window.addEventListener('resize', repositionThreadActionLayer)
  document.addEventListener('click', e => {
    if (!openThreadActionMenuId) return
    const target = e.target as HTMLElement | null
    if (target?.closest('.thread-item-menu-trigger') || target?.closest('.thread-action-popover')) return
    resetThreadActionMenuState()
    updateThreadList()
  })

  store.subscribe(() => {
    const { activeThreadId } = store.get()
    const threadChanged = activeThreadId !== lastRenderThreadId

    updateThreadList()

    if (threadChanged) {
      lastRenderThreadId = activeThreadId
      updateChatArea()
    } else {
      // activeStreamMsgId is non-null while the streaming bubble is in the DOM.
      // Re-rendering the message list would destroy that bubble, so we skip it.
      if (!activeStreamMsgId) updateMessageList()
      updateInputState()
    }
  })

  try {
    const [agents, threads] = await Promise.all([
      api.getAgents(),
      api.getThreads(),
    ])
    store.set({ agents, threads })
  } catch {
    const el = document.getElementById('thread-list')
    if (el) {
      el.innerHTML = `<div class="thread-list-empty" style="color:var(--error)">
        Failed to load threads.<br>Check the server connection in Settings.
      </div>`
    }
  }
}

void init()
