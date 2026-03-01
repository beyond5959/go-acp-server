# FRONTEND TASKS

研发任务拆分与验收标准。对应 `docs/FRONTEND_SPEC.md`。

## 里程碑概览

| ID | 名称 | 关键交付物 |
|---|---|---|
| F0 | 基础设施 | Vite 工程、Go embed 接入、路由注册 |
| F1 | 核心布局 | IM 两栏 UI 骨架，静态样式完成 |
| F2 | 状态管理 | Store、Client ID、Settings 存储 |
| F3 | 会话管理 | 加载列表、新建 Modal、切换会话 |
| F4 | 流式 Turn | SSE 流、消息渲染、取消 Turn |
| F5 | 历史回放 | 切换会话时加载历史，时间戳显示 |
| F6 | 权限处理 | permission_required 卡片、批准 / 拒绝、倒计时 |
| F7 | 消息渲染 | Markdown、代码高亮、代码复制按钮 |
| F8 | UX 打磨 | 主题切换、键盘快捷键、自动滚动、会话搜索 |
| F9 | 集成收尾 | 启动摘要 Web 行、README 更新、文档同步 |

---

## F0：基础设施

**范围：** 建立前端工程脚手架，完成 Go embed 接入，使 `make run` 时前端可访问。

### 任务清单

- [ ] 在 `web/` 下初始化 Vite + TypeScript 工程（`npm create vite`）
- [ ] 配置 `vite.config.ts`：输出目录 `web/dist`，base 为 `/`
- [ ] 创建 `internal/webui/webui.go`，用 `//go:embed web/dist` 嵌入静态资源
- [ ] `webui.Handler()` 实现 SPA fallback（非 API 路径均返回 `index.html`）
- [ ] 在 `internal/httpapi/httpapi.go` 注册前端路由（优先级低于 `/v1/*` 和 `/healthz`）
- [ ] Makefile 新增 `build-web`（`cd web && npm ci && npm run build`），更新 `run` 依赖
- [ ] `.gitignore` 排除 `web/node_modules`；`web/dist` 提交到仓库（供无 Node 环境使用）
- [ ] 提交占位 `index.html`（显示"Agent Hub"标题），验证 embed 流程通端到端

### 验收标准

- `make build-web && make run` 后，浏览器访问 `http://127.0.0.1:8686/` 返回 200，内容包含 "Agent Hub"。
- `make build-web && make run` 后，局域网设备扫描启动输出中的二维码可打开 Web UI（页面包含 "Agent Hub"）。
- `curl http://127.0.0.1:8686/v1/agents -H 'X-Client-ID: test'` 仍返回正确 JSON（API 路由不受影响）。
- `go test ./...` 全部通过（含 webui 包单测：验证 Handler 对 `/` 返回 200 且 Content-Type 为 `text/html`）。

---

## F1：核心布局

**范围：** 完成两栏 IM 布局骨架，无交互逻辑，纯静态 HTML + CSS。

### 任务清单

- [ ] 实现左侧 Sidebar（固定宽度 260px，含 App 标题、「+」新建按钮、会话列表占位、Settings 按钮）
- [ ] 实现右侧 Chat 区域（顶部 ThreadHeader、中部消息列表滚动区、底部输入栏）
- [ ] 定义 CSS 变量体系（颜色、间距、圆角、字体，支持 `[data-theme=dark]` 覆盖）
- [ ] 用户消息气泡（右对齐，主色背景）+ Agent 消息气泡（左对齐，中性背景）静态样式
- [ ] 输入框 + 发送按钮静态样式
- [ ] 空状态页（尚无会话时右侧显示引导文字）
- [ ] 基础响应式：≤768px 时侧边栏隐藏，主区域全宽（后续 F8 补完整）

### 验收标准

- 打开页面，布局结构与 Spec §2 草图一致。
- 调整窗口宽度到 600px，侧边栏收起，右侧占满。
- Light / Dark 主题下，所有元素均可读（对比度 ≥ 4.5:1）。

---

## F2：状态管理

**范围：** 建立前端 store 模型，处理 Client ID 和 Settings 持久化。

### 任务清单

- [ ] `src/store.ts`：轻量发布订阅 Store，管理以下状态：
  - `clientId: string`（UUID，首次生成后存 localStorage）
  - `authToken: string`（存 localStorage）
  - `serverUrl: string`（默认 `window.location.origin`，可覆盖）
  - `theme: 'light' | 'dark' | 'system'`（存 localStorage）
  - `threads: Thread[]`
  - `activeThreadId: string | null`
  - `messages: Map<threadId, Message[]>`
  - `activeStreamState: StreamState | null`
- [ ] `src/types.ts`：定义 `Thread`、`Turn`、`Message`（含 role、content、timestamp、status）、`StreamState`、`PermissionRequest` 等接口
- [ ] `src/utils.ts`：`generateUUID()`、`formatRelativeTime()`、`formatTimestamp()`、`isAbsolutePath()`
- [ ] Settings 面板（静态）：展示 Client ID、Auth Token 输入、Theme 切换、Server URL 输入
- [ ] theme 变更时更新 `document.documentElement.dataset.theme`

### 验收标准

- 首次打开，localStorage 中自动写入 `clientId`（格式为 UUID v4）。
- 修改 Auth Token 并刷新页面，Token 仍然保留。
- 切换主题，页面颜色立即响应，刷新后保持。
- Store `subscribe` 回调在状态变更时触发。

---

## F3：会话管理

**范围：** 实现会话列表加载、新建会话 Modal、切换会话。

### 任务清单

- [ ] `src/api.ts`：封装 `getAgents()`、`getThreads()`、`createThread(body)` 方法，所有请求自动带 `X-Client-ID` 和 `Authorization` header
- [ ] 应用初始化时并行调用 `GET /v1/agents` + `GET /v1/threads`，填充 store
- [ ] 侧边栏渲染 Thread 列表（title / agent badge / 相对时间）
- [ ] 点击 Thread item → 设置 `activeThreadId`，加载历史（见 F5）
- [ ] 新建会话 Modal：
  - Agent 下拉选择（带 available/unavailable 标记）
  - CWD 输入框（前端验证：非空、以 `/` 开头）
  - Title 输入框（可选）
  - 高级选项折叠区（agentOptions JSON）
  - 提交 `POST /v1/threads`，成功后刷新列表并自动选中新会话
- [ ] 新建失败时在 Modal 内显示 API 错误信息

### 验收标准

- 页面加载后侧边栏显示真实 Thread 列表（需服务端有数据）。
- 空列表时显示「暂无会话，点击 + 新建」。
- 新建 Modal 中 Agent 选择器显示从 `/v1/agents` 获取的 agent，不可用的灰色禁用。
- CWD 填写 `abc`（非绝对路径），提交按钮禁用或显示前端校验错误。
- 成功创建会话后，Modal 关闭，新会话出现在列表顶部并自动选中。

---

## F4：流式 Turn 执行

**范围：** 实现发送消息、SSE 流式接收、实时渲染、取消 Turn。

### 任务清单

- [ ] `src/sse.ts`：封装 `TurnStream` 类，管理 `EventSource` 生命周期
  - 支持 `onTurnStarted`、`onDelta`、`onCompleted`、`onError`、`onPermissionRequired` 回调
  - 连接断开时触发 `onDisconnect`，置 StreamState 为 `disconnected`
- [ ] `src/api.ts`：封装 `startTurn(threadId, input)` 返回 `TurnStream`；`cancelTurn(turnId)`
- [ ] 输入框发送逻辑：
  - 按 Send / `Cmd+Enter` → 禁用输入框 → 清空内容 → 添加用户消息气泡 → 创建 Agent 气泡占位（带打字动画）
  - `turn_started` → 记录 `turnId`，启动 Cancel 按钮
  - `message_delta` → 追加内容到 Agent 气泡
  - `turn_completed` → 完成动画消除，更新 stopReason 标记
  - `error` → 气泡变为错误状态（红色提示 + 错误 code/message）
- [ ] Thread Header 中「Cancel」按钮：流式进行中显示，点击调用 `cancelTurn()`
- [ ] 流式进行中不允许再次发送（发送按钮禁用 + tooltip「当前有正在进行的对话」）
- [ ] 409 Conflict（同线程并发 turn）时显示友好提示

### 验收标准

- 发送消息后，用户气泡立即出现，Agent 气泡出现打字动画。
- Agent 流式回复字符逐步追加，完成后动画消除。
- 流式进行中点击 Cancel，Agent 气泡显示「已取消」标记，`stopReason=cancelled`。
- 关闭浏览器标签页（SSE 断开），服务端 turn 按 fail-closed 收敛（验证服务端日志）。

---

## F5：历史回放

**范围：** 切换会话时从 `/history` 接口加载并渲染历史消息。

### 任务清单

- [ ] `src/api.ts`：封装 `getHistory(threadId)` 调用 `GET /v1/threads/{threadId}/history`
- [ ] 切换会话时触发加载（显示骨架屏 loading）
- [ ] `Turn` 转换为 `Message[]`：
  - `requestText` → 用户气泡
  - `responseText` → Agent 气泡（含 Markdown 渲染）
  - `status=cancelled` → Agent 气泡末尾显示取消标记
  - `status=error` → Agent 气泡显示错误信息
- [ ] 每条消息显示 `createdAt` 时间戳（气泡右下角，格式：`HH:mm` 当天 / `MM-DD HH:mm` 非当天）
- [ ] 历史加载完成后滚动到底部

### 验收标准

- 切换到有历史的会话，消息列表还原完整，顺序正确，时间戳准确。
- 已取消的 turn 有明显标记（灰色 / 斜体 + 小标签）。
- 空历史会话（新创建）显示「发送第一条消息开始对话」引导文字。

---

## F6：权限处理

**范围：** 处理 `permission_required` SSE 事件，渲染权限卡片，发送决策。

### 任务清单

- [ ] `src/components/permission-card.ts`：渲染 `permission_required` 事件
  - 显示字段：approval 类型（`command|file|network|mcp` 配色 badge）、command 内容、权限描述
  - Allow（绿色）/ Deny（红色）按钮
  - 倒计时进度条（默认 15s，对应服务端 `permissionTimeout`）
  - 超时后自动变为「已超时（已拒绝）」状态
- [ ] `src/api.ts`：封装 `resolvePermission(permissionId, outcome: 'approved'|'declined'|'cancelled')`
- [ ] 点击 Allow/Deny 后禁用按钮，调用接口，成功后更新卡片状态（已批准/已拒绝）
- [ ] 409（已决策）时：卡片静默更新为已有决策状态
- [ ] 流式过程中收到多个 `permission_required`，每个独立渲染（turn 串行，但保持 UI 一致）

### 验收标准

- SSE 收到 `permission_required` 时，消息区出现权限卡片，倒计时动画正确。
- 点击 Allow，卡片变绿「已批准」，Turn 继续流式输出。
- 点击 Deny，卡片变红「已拒绝」，Turn 以 `stopReason=cancelled` 结束。
- 超时（15s 不操作），卡片自动更新为「已超时（自动拒绝）」。

---

## F7：消息渲染增强

**范围：** Markdown 渲染、代码高亮、代码复制按钮、消息复制。

### 任务清单

- [ ] 安装 `marked`（`^9`）、`highlight.js`（子集构建，包含 go/ts/js/python/bash/json/yaml）
- [ ] `src/markdown.ts`：配置 `marked` 使用 `highlight.js` renderer，sanitize HTML 输出
- [ ] Agent 气泡内容使用 markdown 渲染（streaming 时可保持纯文本，complete 后再渲染）
- [ ] 代码块右上角 Copy 按钮（hover 显示），点击后 2s「已复制 ✓」反馈
- [ ] 长代码块超过 20 行时折叠（「展开」按钮），展开后可收起
- [ ] 消息气泡 hover 时显示浮动「复制全文」图标

### 验收标准

- Agent 回复的 Markdown（标题、列表、代码块、加粗）正确渲染。
- Go 代码块有语法高亮颜色。
- 点击 Copy 按钮，剪贴板内容与代码块文本一致。
- 渲染后不出现 XSS（例如 `<script>alert(1)</script>` 在 agent 输出中被转义显示，不执行）。

---

## F8：UX 打磨

**范围：** 主题切换、键盘快捷键、自动滚动、会话搜索、连接状态、移动端基础适配。

### 任务清单

- [ ] **主题切换**：Settings 中 Light / Dark / System 三选一，System 模式监听 `prefers-color-scheme`
- [ ] **键盘快捷键**：
  - `Cmd/Ctrl+Enter`：发送消息
  - `Escape`：关闭 Modal / Settings
  - `Cmd/Ctrl+K`：打开会话搜索（或 focus 搜索框）
- [ ] **自动滚动**：收到新消息自动滚动到底部；用户手动上滑时暂停（检测 `scrollTop` 距底 > 100px）；显示「↓ 跳转到最新」浮动按钮
- [ ] **会话搜索**：侧边栏上方搜索框，实时过滤 `title + cwd`（不需要服务端支持）
- [ ] **SSE 连接状态**：
  - 底部状态栏：`● 已连接` / `○ 重连中...` / `✗ 断开`
  - 断开时提示用户可刷新页面
- [ ] **未读标记**：非 active 会话有新消息（SSE delta）时，侧边栏显示蓝色小圆点
- [ ] **移动端（≤768px）**：侧边栏可通过汉堡按钮展开/收起，全屏聊天区
- [ ] **Compact 按钮**：Thread Header 中按钮，点击调用 `POST compact`，显示 loading，完成后 toast 提示

### 验收标准

- 浅色 / 深色切换即时生效，无闪烁。
- `Cmd+Enter` 在消息框 focus 时触发发送，在 Modal 中不误触发。
- 上滑后不再自动滚动；点击「↓」按钮准确滚到底部并恢复自动滚动。
- 搜索框输入 "proj" 后，只显示标题或 cwd 中含 "proj" 的会话。
- 在 768px 宽度下，侧边栏默认收起，点击菜单按钮展开。

---

## F9：集成收尾

**范围：** 启动输出二维码与端口提示，README/文档同步，PROGRESS.md 更新。

### 任务清单

- [ ] `cmd/agent-hub-server/main.go`：启动输出包含二维码，并在二维码下方提示端口与「局域网用户可以扫上方二维码访问」。
- [ ] 对应更新 `main_test.go` 中的启动摘要格式测试
- [ ] `README.md`：新增「Web UI」章节，说明打开浏览器访问的地址
- [ ] `docs/API.md`：新增 `GET /` 和 `GET /assets/*` 端点描述
- [ ] `PROGRESS.md`：记录 Frontend 里程碑完成状态
- [ ] `docs/DECISIONS.md`：添加 ADR-018（前端内嵌方案决策）
- [ ] 运行 `go test ./...` 确认全绿
- [ ] 运行 `make build-web && make build` 确认全量构建成功

### 验收标准

- 启动服务，stderr 中包含二维码，并在二维码下方提示端口与「局域网用户可以扫上方二维码访问」。
- `go test ./...` 全部通过（含新增 webui 包测试、main 测试中启动输出格式验证）。
- `make build-web && go build ./...` 无错误完成。
- 浏览器完整使用流程：新建会话 → 发送消息 → 查看历史 → 切换会话，全部功能端到端正常。

---

## 全局验收门槛（Global Gate）

所有里程碑完成后执行：

```bash
# 格式化
gofmt -w $(find . -name '*.go' -type f)

# 后端测试
go test ./...

# 前端构建
cd web && npm ci && npm run build

# 全量构建
go build ./...

# 端到端手动验证
make run
# 浏览器打开 Web UI（本机可用 127.0.0.1，局域网设备扫二维码）
# 1. 新建会话（选 Codex，填入真实绝对路径）
# 2. 发送消息，确认流式回复
# 3. 触发权限请求，点击 Allow
# 4. 切换回会话，历史正常显示
# 5. 触发 Cancel
```
