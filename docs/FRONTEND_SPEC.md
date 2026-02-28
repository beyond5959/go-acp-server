# FRONTEND SPEC

## 1. 目标

为 Code Agent Hub Server 提供内嵌的 Web 前端，通过 Go `embed` 包随服务二进制分发。
前端提供类 IM 的会话界面，让用户无需 CLI 或独立客户端即可与 Agent 交互。

## 2. UI 布局

```
┌─────────────────────────────────────────────────────────────────┐
│  ┌──────────────┐  ┌───────────────────────────────────────────┐│
│  │ Agent Hub    │  │ Thread: My Project  [Codex] /home/proj    ││
│  │           [+]│  │                    [Compact] [Cancel]     ││
│  ├──────────────┤  ├───────────────────────────────────────────┤│
│  │ ● Thread A   │  │                                           ││
│  │   Codex · 2m │  │  [user]  hello                      12:01 ││
│  │──────────────│  │                                           ││
│  │ ○ Thread B   │  │  [agent] Hello! How can I help?     12:01 ││
│  │   Codex · 1h │  │          ```go                            ││
│  │──────────────│  │          fmt.Println("hi")                ││
│  │ ○ Thread C   │  │          ```                       [copy] ││
│  │   Codex · 3h │  │                                           ││
│  │              │  │  [perm]  ⚠ Run: ls /home  [Allow][Deny]  ││
│  │              │  │          Timeout in 12s                   ││
│  │              │  │                                           ││
│  │              │  │  ···  (streaming)                         ││
│  ├──────────────┤  ├───────────────────────────────────────────┤│
│  │  [⚙ Settings]│  │  ┌──────────────────────────────────────┐ ││
│  └──────────────┘  │  │ Type a message...            [Send ↵] │ ││
│                    │  └──────────────────────────────────────┘ ││
│                    └───────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

## 3. 功能清单

### 3.1 必须实现（Must Have）

| 功能 | 说明 |
|---|---|
| IM 布局 | 左侧会话列表 + 右侧消息区，固定布局 |
| 会话列表 | 显示 agent 类型、标题、最后消息预览、时间 |
| 切换会话 | 点击侧边栏会话加载历史并切换焦点 |
| 新建会话 | Modal 弹窗：选择 Agent、填写 CWD（绝对路径）、可选标题 |
| SSE 流式显示 | 实时追加 `message_delta`，有打字指示动画 |
| 消息气泡 | 用户消息靠右，Agent 消息靠左，时间戳显示 |
| 取消 Turn | 流式进行中显示 Cancel 按钮，调用 cancel 接口 |
| 权限请求 UI | 内联卡片显示权限详情，含 Allow / Deny 按钮和倒计时 |
| 历史加载 | 切换会话时调用 `/history` 还原完整消息列表 |
| 错误展示 | API 错误和 SSE error 事件以醒目方式内联显示 |
| Client ID | 自动生成 UUID，存 localStorage，所有请求携带 |

### 3.2 应该实现（Should Have）

| 功能 | 说明 |
|---|---|
| Markdown 渲染 | Agent 回复支持 Markdown（标题、列表、代码块、链接） |
| 代码高亮 + 复制 | 代码块语法高亮，右上角 Copy 按钮 |
| 深色 / 浅色主题 | 系统主题跟随，可手动切换，偏好存 localStorage |
| 键盘快捷键 | `Cmd/Ctrl+Enter` 发送；`Escape` 关闭 Modal |
| 自动滚动 | 新消息时滚动到底部；用户向上滚动时暂停自动滚动 |
| 未读标记 | 非活跃会话有新消息时侧边栏显示小圆点 |
| 连接状态 | 顶部/底部指示器显示 SSE 连接状态 |
| Auth Token 配置 | Settings 面板中输入 Bearer Token，存 localStorage |
| Agent 状态展示 | 新建会话时 Agent 选项显示 available / unavailable |

### 3.3 锦上添花（Nice to Have）

| 功能 | 说明 |
|---|---|
| 会话搜索 / 过滤 | 侧边栏上方搜索框，实时过滤标题和内容预览 |
| Compact 触发 | 会话头部"压缩摘要"按钮，调用 compact 接口 |
| 消息复制 | 单条消息右键 / hover 菜单中复制文本 |
| 会话重命名 | 侧边栏双击 / 右键菜单修改标题 |
| 跳转到底部 | 右下角悬浮箭头，点击滚动到最新消息 |
| Server URL 配置 | Settings 中可修改 API 基础地址（适配反代场景） |
| 移动端适应 | 侧边栏折叠，响应式布局（768px 断点） |

## 4. 技术选型

| 层次 | 选型 | 理由 |
|---|---|---|
| 构建工具 | Vite | 快速、零配置、输出小体积 |
| 语言 | TypeScript（无框架） | 类型安全，无运行时框架，bundle 更小 |
| 样式 | 纯 CSS + CSS 变量 | 无依赖，主题切换靠 `data-theme` 属性 |
| Markdown | marked.js (CDN bundle 打包进 dist) | 轻量，支持 GFM |
| 代码高亮 | highlight.js (子集构建) | 与 marked.js 配合，只打包常用语言 |
| SSE 客户端 | 原生 `EventSource` API | 浏览器内置，无需额外依赖 |
| 状态管理 | 自实现轻量 store（发布订阅） | 避免引入 Redux/Zustand 等框架依赖 |
| Go 内嵌 | `//go:embed web/dist` | 单二进制分发，无外部文件依赖 |

## 5. 文件结构

```
web/                          # 前端源码根目录
  src/
    main.ts                   # 应用入口，初始化和路由
    api.ts                    # 所有 HTTP API 调用封装
    sse.ts                    # SSE 流式连接管理
    store.ts                  # 客户端状态（threads、active thread、settings）
    types.ts                  # TypeScript 接口定义（Thread、Turn、Message 等）
    utils.ts                  # 工具函数（时间格式化、UUID、路径校验）
    components/
      sidebar.ts              # 侧边栏：会话列表 + 新建按钮
      chat.ts                 # 消息区：历史 + 流式消息渲染
      input.ts                # 输入框组件，发送逻辑
      permission-card.ts      # 权限请求内联卡片
      new-thread-modal.ts     # 新建会话 Modal
      settings-panel.ts       # Settings 侧滑面板
    markdown.ts               # marked.js + highlight.js 封装
  index.html                  # SPA 入口 HTML
  style.css                   # 全局 CSS（变量 + 布局）
  package.json
  tsconfig.json
  vite.config.ts
web/dist/                     # Vite 构建输出（由 make build-web 生成）

internal/webui/
  webui.go                    # //go:embed web/dist + ServeHTTP handler
```

## 6. Go 集成方式

### 6.1 embed 包方案

```go
// internal/webui/webui.go
package webui

import (
    "embed"
    "net/http"
)

//go:embed web/dist
var staticFiles embed.FS

// Handler 返回服务前端静态资源的 http.Handler。
// SPA 路由：所有非 /v1/ /healthz 请求回退到 index.html。
func Handler() http.Handler { ... }
```

### 6.2 路由集成

在 `internal/httpapi/httpapi.go` 的 `serveHTTP` 中增加：

```
GET /          → serve index.html
GET /assets/*  → serve Vite 打包的 JS/CSS/font 资源
```

所有 `/v1/*` 和 `/healthz` 优先匹配，其余回退 SPA。

### 6.3 启动摘要更新

`printStartupSummary` 增加 `Web` 行：

```
Agent Hub Server started
  Time:   2026-02-28 10:00:00 CST
  HTTP:   http://127.0.0.1:8686
  Web:    http://127.0.0.1:8686/          ← 新增
  DB:     ~/.go-agent-server/agent-hub.db
  Agents: Codex (available), Claude Code (unavailable)
  Help:   agent-hub-server --help
```

### 6.4 Makefile 更新

```makefile
build-web:
	cd web && npm ci && npm run build

build: build-web
	go build ./...

run: build-web
	go run ./cmd/agent-hub-server
```

## 7. API 使用映射

| 前端操作 | API 调用 |
|---|---|
| 应用初始化 | `GET /v1/agents`（获取 agent 列表和状态） |
| 打开应用 | `GET /v1/threads`（加载会话列表） |
| 新建会话 | `POST /v1/threads` |
| 切换会话 | `GET /v1/threads/{threadId}/history` |
| 发送消息 | `POST /v1/threads/{threadId}/turns`（SSE） |
| 取消 Turn | `POST /v1/turns/{turnId}/cancel` |
| 响应权限 | `POST /v1/permissions/{permissionId}` |
| 触发 Compact | `POST /v1/threads/{threadId}/compact` |

## 8. 设计原则

- **协议优先**：前端不存储任何对话内容到 localStorage，历史从 API 加载。
- **单向数据流**：store → UI 单向，事件（SSE、用户操作）→ store → 重渲染。
- **降级友好**：SSE 断开时显示 Reconnecting 状态，不静默丢失事件。
- **离线能力**：CDN 依赖全部打包进 dist，无运行时外部请求。
- **安全**：Markdown 渲染使用 `marked` 的 sanitize 模式；不 eval 任何 agent 输出。
