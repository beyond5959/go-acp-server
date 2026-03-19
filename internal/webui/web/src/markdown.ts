import { marked } from 'marked'
import hljs from 'highlight.js/lib/core'
import go from 'highlight.js/lib/languages/go'
import typescript from 'highlight.js/lib/languages/typescript'
import javascript from 'highlight.js/lib/languages/javascript'
import python from 'highlight.js/lib/languages/python'
import bash from 'highlight.js/lib/languages/bash'
import json from 'highlight.js/lib/languages/json'
import yaml from 'highlight.js/lib/languages/yaml'
import { copyText, escHtml } from './utils.ts'

// Register language subset to minimise bundle size
hljs.registerLanguage('go',         go)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('ts',         typescript)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('js',         javascript)
hljs.registerLanguage('python',     python)
hljs.registerLanguage('py',         python)
hljs.registerLanguage('bash',       bash)
hljs.registerLanguage('sh',         bash)
hljs.registerLanguage('json',       json)
hljs.registerLanguage('yaml',       yaml)
hljs.registerLanguage('yml',        yaml)

// Lines threshold above which a code block gets a "Show all" expand button
const FOLD_LINES = 20

let blockId = 0

function nextId(): string {
  return `cb-${++blockId}`
}

function highlightCode(code: string, lang: string | undefined): string {
  if (lang && hljs.getLanguage(lang)) {
    try {
      return hljs.highlight(code, { language: lang }).value
    } catch { /* ignore highlight errors */ }
  }
  return escHtml(code)
}

marked.use({
  renderer: {
    // Prevent raw HTML from agent output from executing — return as escaped text
    html(html: string): string {
      return escHtml(html)
    },

    code(code: string, language: string | undefined): string {
      const id          = nextId()
      const highlighted = highlightCode(code, language)
      const langLabel   = language ? escHtml(language) : 'text'
      const lineCount   = code.split('\n').length
      const needsFold   = lineCount > FOLD_LINES
      const expandBtn   = needsFold
        ? `\n  <button class="code-expand-btn" data-target="${id}" type="button">Show all ${lineCount} lines ↓</button>`
        : ''

      return `<div class="code-block">
  <div class="code-block-header">
    <span class="code-lang">${langLabel}</span>
    <button class="code-copy-btn" data-code="${escHtml(code)}" type="button">Copy</button>
  </div>
  <pre class="code-pre${needsFold ? ' code-pre--collapsed' : ''}" id="${id}"><code class="hljs">${highlighted}</code></pre>${expandBtn}
</div>`
    },
  },
})

/** Render markdown text to sanitised HTML. Only call for finalised agent messages. */
export function renderMarkdown(text: string): string {
  const html = marked.parse(text) as string
  return html
    .replace(/<table>/g, '<div class="md-table-wrap"><table>')
    .replace(/<\/table>/g, '</table></div>')
}

/** Bind copy/expand/message-copy buttons within a container. Idempotent. */
export function bindMarkdownControls(container: HTMLElement): void {
  // Code copy buttons
  container
    .querySelectorAll<HTMLButtonElement>('.code-copy-btn:not([data-bound])')
    .forEach(btn => {
      btn.dataset.bound = '1'
      btn.addEventListener('click', () => {
        void copyText(btn.dataset.code ?? '').then(copied => {
          if (!copied) return
          btn.textContent = 'Copied ✓'
          btn.classList.add('code-copy-btn--copied')
          setTimeout(() => {
            btn.textContent = 'Copy'
            btn.classList.remove('code-copy-btn--copied')
          }, 2_000)
        })
      })
    })

  // Code expand buttons
  container
    .querySelectorAll<HTMLButtonElement>('.code-expand-btn:not([data-bound])')
    .forEach(btn => {
      btn.dataset.bound = '1'
      btn.addEventListener('click', () => {
        document
          .getElementById(btn.dataset.target ?? '')
          ?.classList.remove('code-pre--collapsed')
        btn.remove()
      })
    })

  // Tool-call content expand buttons
  container
    .querySelectorAll<HTMLButtonElement>('.message-tool-call__expand-btn:not([data-bound])')
    .forEach(btn => {
      const hiddenPanel = btn.closest<HTMLElement>('.message-tool-call__panel[hidden]')
      if (hiddenPanel) return
      btn.dataset.bound = '1'
      const preEl = document.getElementById(btn.dataset.target ?? '')
      if (!(preEl instanceof HTMLElement)) {
        btn.remove()
        return
      }
      if (preEl.scrollHeight <= preEl.clientHeight + 1) {
        preEl.classList.remove('message-tool-call__pre--collapsed')
        btn.remove()
        return
      }
      btn.hidden = false
      btn.addEventListener('click', () => {
        preEl.classList.remove('message-tool-call__pre--collapsed')
        btn.remove()
      })
    })

  // Message copy buttons
  container
    .querySelectorAll<HTMLButtonElement>('.msg-copy-btn:not([data-bound])')
    .forEach(btn => {
      btn.dataset.bound = '1'
      btn.addEventListener('click', () => {
        const encoded = btn.dataset.copyText ?? ''
        const text = encoded
          ? decodeURIComponent(encoded)
          : btn.closest('.message-group')
            ?.querySelector('.message-bubble')
            ?.textContent ?? ''
        void copyText(text).then(copied => {
          if (!copied) return
          btn.textContent = '✓'
          btn.classList.add('msg-copy-btn--copied')
          setTimeout(() => {
            btn.textContent = '⎘'
            btn.classList.remove('msg-copy-btn--copied')
          }, 1_500)
        })
      })
    })
}
