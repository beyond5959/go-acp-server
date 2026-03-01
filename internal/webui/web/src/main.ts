import './style.css'
import { store } from './store.ts'
import { api } from './api.ts'
import { applyTheme, settingsPanel } from './components/settings-panel.ts'
import { newThreadModal } from './components/new-thread-modal.ts'
import { mountPermissionCard } from './components/permission-card.ts'
import { renderMarkdown, bindMarkdownControls } from './markdown.ts'
import type { Thread, Message, Turn } from './types.ts'
import type { TurnStream } from './sse.ts'
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

// ── Active stream state (DOM-managed, not in store) ───────────────────────

/**
 * Non-null while a streaming bubble is live in the DOM.
 * We use this to prevent updateMessageList() from wiping the in-progress bubble.
 */
let activeStream:         TurnStream | null = null
let activeStreamMsgId:    string | null     = null
let activeStreamThreadId: string | null     = null

/** Last threadId that triggered a full chat-area re-render. */
let lastRenderThreadId: string | null = null

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

function renderThreadItem(t: Thread, activeId: string | null, query: string): string {
  const isActive     = t.threadId === activeId
  const initials     = (t.agent ?? 'A').slice(0, 1).toUpperCase()
  const displayTitle = threadTitle(t)
  const relTime      = t.updatedAt ? formatRelativeTime(t.updatedAt) : ''

  const titleHtml = query
    ? escHtml(displayTitle).replace(
        new RegExp(`(${escHtml(query)})`, 'gi'),
        '<mark>$1</mark>',
      )
    : escHtml(displayTitle)

  return `
    <div class="thread-item ${isActive ? 'thread-item--active' : ''}"
         data-thread-id="${escHtml(t.threadId)}"
         role="button"
         tabindex="0"
         aria-label="${escHtml(displayTitle)}">
      <div class="thread-item-avatar ${isActive ? '' : 'thread-item-avatar--inactive'}">${initials}</div>
      <div class="thread-item-body">
        <div class="thread-item-title">${titleHtml}</div>
        <div class="thread-item-preview">${escHtml(t.cwd)}</div>
        <div class="thread-item-foot">
          <span class="badge badge--agent">${escHtml(t.agent ?? '')}</span>
          <span class="thread-item-time">${relTime}</span>
        </div>
      </div>
    </div>`
}

function updateThreadList(): void {
  const el = document.getElementById('thread-list')
  if (!el) return

  const { threads, activeThreadId, searchQuery } = store.get()
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
    return
  }

  el.innerHTML = filtered
    .map(t => renderThreadItem(t, activeThreadId, q))
    .join('')

  el.querySelectorAll<HTMLElement>('.thread-item').forEach(item => {
    const handler = () => {
      const id = item.dataset.threadId ?? ''
      if (id && id !== store.get().activeThreadId) {
        store.set({ activeThreadId: id })
      }
      // Close mobile sidebar on thread select
      document.getElementById('sidebar')?.classList.remove('sidebar--open')
    }
    item.addEventListener('click', handler)
    item.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') handler() })
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
    if (state.streamState?.threadId === threadId) return
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

function renderMessage(msg: Message, agentInitial: string): string {
  if (msg.role === 'user') {
    return `
      <div class="message message--user" data-msg-id="${escHtml(msg.id)}">
        <div class="message-group">
          <div class="message-bubble">${escHtml(msg.content)}</div>
          <div class="message-meta">
            <span class="message-time">${formatTimestamp(msg.timestamp)}</span>
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
  const copyBtn  = isDone      ? `<button class="msg-copy-btn" title="Copy message" type="button">⎘</button>` : ''

  return `
    <div class="message message--agent" data-msg-id="${escHtml(msg.id)}">
      <div class="message-avatar">${escHtml(agentInitial)}</div>
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

function updateMessageList(): void {
  const listEl = document.getElementById('message-list')
  if (!listEl) return

  const { activeThreadId, threads, messages } = store.get()
  if (!activeThreadId) return

  const thread   = threads.find(t => t.threadId === activeThreadId)
  const msgs     = messages[activeThreadId] ?? []
  const agentInitial = (thread?.agent ?? 'A').slice(0, 1).toUpperCase()

  if (!msgs.length) {
    listEl.innerHTML = `
      <div class="empty-state">
        <div class="empty-state-icon" style="font-size:28px">💬</div>
        <h3 class="empty-state-title" style="font-size:var(--font-size-lg)">Start the conversation</h3>
        <p class="empty-state-desc">Send your first message to begin working with ${escHtml(thread?.agent ?? 'the agent')}.</p>
      </div>`
    return
  }

  listEl.innerHTML = msgs.map(m => renderMessage(m, agentInitial)).join('')
  bindMarkdownControls(listEl)
  listEl.scrollTop = listEl.scrollHeight
  // Sync scroll button (we just moved to the bottom)
  const scrollBtn = document.getElementById('scroll-bottom-btn')
  if (scrollBtn) scrollBtn.style.display = 'none'
}

// ── Input state ───────────────────────────────────────────────────────────

function updateInputState(): void {
  const { streamState } = store.get()
  const isStreaming   = !!streamState
  const isCancelling  = streamState?.status === 'cancelling'

  const sendBtn  = document.getElementById('send-btn')   as HTMLButtonElement   | null
  const cancelBtn = document.getElementById('cancel-btn') as HTMLButtonElement   | null
  const inputEl  = document.getElementById('message-input') as HTMLTextAreaElement | null

  if (sendBtn)  sendBtn.disabled  = isStreaming
  if (inputEl)  inputEl.disabled  = isStreaming
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
        <button class="btn btn-primary btn-send" id="send-btn">
          Send ${iconSend}
        </button>
      </div>
      <div class="input-hint">Press <kbd>⌘ Enter</kbd> to send · <kbd>Esc</kbd> to cancel · <kbd>/</kbd> to search</div>
    </div>`
}

function updateChatArea(): void {
  const chat = document.getElementById('chat')
  if (!chat) return

  const { threads, activeThreadId } = store.get()
  const thread = activeThreadId ? threads.find(t => t.threadId === activeThreadId) : null

  // Abort any stream that belongs to a thread we're leaving
  if (activeStream && activeStreamThreadId !== activeThreadId) {
    activeStream.abort()
    activeStream         = null
    activeStreamMsgId    = null
    activeStreamThreadId = null
    store.set({ streamState: null })
  }

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

  // Show cached messages immediately (avoids spinner flash on revisit).
  // If none yet, put up a loading spinner; loadHistory() will replace it.
  const hasCached = !!(store.get().messages[thread.threadId]?.length)
  if (hasCached) {
    updateMessageList()
  } else {
    const listEl = document.getElementById('message-list')
    if (listEl) {
      listEl.innerHTML = `<div class="message-list-loading"><div class="loading-spinner"></div></div>`
    }
  }

  updateInputState()
  bindInputResize()
  bindSendHandler()
  bindCancelHandler()
  bindScrollBottom()

  // Always reload history from server (keeps view fresh; guards against overwrites during streaming)
  void loadHistory(thread.threadId)
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
  input.addEventListener('input', () => {
    input.style.height = 'auto'
    input.style.height = Math.min(input.scrollHeight, 160) + 'px'
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

  const { activeThreadId, threads, streamState } = store.get()
  if (!activeThreadId || streamState) return

  const thread       = threads.find(t => t.threadId === activeThreadId)
  const agentInitial = (thread?.agent ?? 'A').slice(0, 1).toUpperCase()

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
  addMessageToStore(activeThreadId, userMsg)

  // ── 2. Reserve streaming message ID before touching streamState ───────────
  //    This prevents subscribe → updateMessageList from wiping the bubble.
  const agentMsgId          = generateUUID()
  activeStreamMsgId         = agentMsgId
  activeStreamThreadId      = activeThreadId
  // activeStream set below, after DOM setup

  // ── 3. Gate the subscribe handler via streamState ──────────────────────────
  const capturedThreadId = activeThreadId
  store.set({ streamState: { turnId: '', threadId: activeThreadId, messageId: agentMsgId, status: 'streaming' } })

  // ── 4. Append streaming bubble directly to DOM ─────────────────────────────
  const listEl = document.getElementById('message-list')
  if (listEl) {
    listEl.querySelector('.empty-state')?.remove()
    const div = document.createElement('div')
    div.className        = 'message message--agent'
    div.dataset.msgId    = agentMsgId
    div.innerHTML = `
      <div class="message-avatar">${escHtml(agentInitial)}</div>
      <div class="message-group">
        <div class="message-bubble message-bubble--streaming" id="bubble-${escHtml(agentMsgId)}">
          <div class="typing-indicator"><span></span><span></span><span></span></div>
        </div>
        <div class="message-meta">
          <span class="message-time">${formatTimestamp(now)}</span>
        </div>
      </div>`
    listEl.appendChild(div)
    listEl.scrollTop = listEl.scrollHeight
  }

  // ── 5. Start SSE stream ────────────────────────────────────────────────────
  activeStream = api.startTurn(activeThreadId, text, {

    onTurnStarted({ turnId }) {
      const ss = store.get().streamState
      if (ss) store.set({ streamState: { ...ss, turnId } })
    },

    onDelta({ delta }) {
      const bubbleEl = document.getElementById(`bubble-${agentMsgId}`)
      if (!bubbleEl) return
      const list      = document.getElementById('message-list')
      const atBottom  = !list || isNearBottom(list)
      // Replace typing indicator with first delta
      if (bubbleEl.querySelector('.typing-indicator')) {
        bubbleEl.classList.remove('message-bubble--streaming')
        bubbleEl.textContent = delta
      } else {
        bubbleEl.textContent = (bubbleEl.textContent ?? '') + delta
      }
      if (atBottom && list) list.scrollTop = list.scrollHeight
    },

    onPermissionRequired(event) {
      const listEl = document.getElementById('message-list')
      if (listEl) mountPermissionCard(listEl, event)
    },

    onCompleted({ stopReason }) {
      const bubbleEl    = document.getElementById(`bubble-${agentMsgId}`)
      const finalContent = bubbleEl?.textContent ?? ''

      // Clear stream tracking BEFORE addMessageToStore (so subscribe calls updateMessageList)
      activeStream         = null
      activeStreamMsgId    = null
      activeStreamThreadId = null

      addMessageToStore(capturedThreadId, {
        id:         agentMsgId,
        role:       'agent',
        content:    finalContent,
        timestamp:  now,
        status:     stopReason === 'cancelled' ? 'cancelled' : 'done',
        stopReason,
      })
      store.set({ streamState: null })
    },

    onError({ code, message: msg }) {
      activeStream         = null
      activeStreamMsgId    = null
      activeStreamThreadId = null

      addMessageToStore(capturedThreadId, {
        id:           agentMsgId,
        role:         'agent',
        content:      '',
        timestamp:    now,
        status:       'error',
        errorCode:    code,
        errorMessage: msg,
      })
      store.set({ streamState: null })
    },

    onDisconnect() {
      const bubbleEl     = document.getElementById(`bubble-${agentMsgId}`)
      const partialContent = bubbleEl?.textContent ?? ''

      activeStream         = null
      activeStreamMsgId    = null
      activeStreamThreadId = null

      addMessageToStore(capturedThreadId, {
        id:           agentMsgId,
        role:         'agent',
        content:      partialContent,
        timestamp:    now,
        status:       'error',
        errorMessage: 'Connection lost',
      })
      store.set({ streamState: null })
    },
  })
}

// ── Cancel ────────────────────────────────────────────────────────────────

function bindCancelHandler(): void {
  document.getElementById('cancel-btn')?.addEventListener('click', () => void handleCancel())
}

async function handleCancel(): Promise<void> {
  const { streamState } = store.get()
  if (!streamState?.turnId) return

  store.set({ streamState: { ...streamState, status: 'cancelling' } })
  try {
    await api.cancelTurn(streamState.turnId)
  } catch {
    // Ignore — stream will eventually deliver turn_completed with stopReason=cancelled
  }
}

// ── New thread ────────────────────────────────────────────────────────────

function openNewThread(): void {
  newThreadModal.open(threadId => {
    store.set({ activeThreadId: threadId })
  })
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
      // (2) clear search if focused
      const searchEl = document.getElementById('search-input') as HTMLInputElement | null
      if (searchEl && document.activeElement === searchEl) {
        searchEl.value = ''
        store.set({ searchQuery: '' })
        searchEl.blur()
        return
      }
      // (3) cancel active stream
      const { streamState } = store.get()
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
