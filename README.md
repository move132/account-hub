# Account Hub

Go + React 账号凭据管理后台。

## 功能

- 账号总览，按 GPT / 即梦分别统计账号总数、启用数、禁用数，并查看数量变化折线图
- 管理员 Cookie 会话、CSRF、防暴力登录和 Argon2id 密码哈希
- OpenAI/Sora GPT账号、检查与刷新
- GPT 账号存储用量、图片列表/预览/批量删除，以及聊天记录分页、消息与图片查看
- 即梦账号管理、Session 刷新、积分查询/领取与历史作品
- TXT/CSV 预检与批量导入、选中 Token 导出、启用 Session ZIP 导出、持久化批量任务和自动刷新
- 精确 10 项系统设置、审计日志、健康检查
- 实时运行日志，支持级别筛选、搜索、自动滚动和断线重连
- Radix UI + Tailwind CSS 管理端，图标统一使用 Lucide React，支持暗色、亮色和跟随系统

ChatGPT/Sora 的 Token 检查和刷新使用 `tls-client v1.16.0`，固定 Chrome 150 的 TLS/HTTP/2 指纹及对应请求头，HTTP/3 关闭。沿用系统设置中的 HTTP、HTTPS 或 SOCKS5 代理以及现有超时、重试规则。各账号逐次传入自己的凭据，不共享 Cookie Jar；即梦使用独立的标准 HTTP 客户端。

## 本地运行

依赖 Go 1.26、Node.js 24 和 pnpm。

在项目根目录一键启动 Go API 和 Vite 开发服务器：

```sh
sh ./run.sh
```

首次启动空数据库时，系统会自动生成格式为 `hub_` 加 10 位随机字符的管理员密码，并在启动日志中输出。密码只在首次创建管理员时生成，之后保存在数据库中；如果在页面修改密码，后续重启会继续使用修改后的密码。

脚本先下载 Go 依赖并编译后端，等待 <http://127.0.0.1:8500/health/ready> 就绪后，再在 <http://localhost:8501> 启动开发页面，将 `/api` 与 `/health` 代理到 `127.0.0.1:8500`。按 `Ctrl+C` 会同时停止前后端。

未自定义 `GOPROXY` 时，脚本会将 Go 的默认下载源改为本次启动使用的 `https://goproxy.cn|https://proxy.golang.org|direct`，支持连接超时后切换下载源。可在项目根目录的 `.env` 中设置 `GOPROXY='你的下载源'`；环境变量和已有的自定义 Go 配置优先。依赖下载或编译失败时，脚本会直接退出并显示原因。

手动运行时，在项目根目录启动后端：

```powershell
cd backend
$env:GOPROXY = 'https://goproxy.cn|https://proxy.golang.org|direct'
go mod download
go run ./cmd/account-hub
```

再在另一个终端，从项目根目录启动前端：

```powershell
cd frontend
pnpm install --ignore-scripts
pnpm dev
```

运行时数据固定写入 `backend/data/account-hub.db`。请勿将 `backend/data/` 或 `.env` 提交到版本库或复制进镜像。

## 检查与构建

```powershell
cd backend
go test ./...
cd ..\frontend
pnpm test
pnpm typecheck
pnpm lint
pnpm build
```

生产构建：

```powershell
docker compose -f compose-local.yaml build
docker compose -f compose-local.yaml up -d
```

使用 GHCR 中的预构建镜像：

```powershell
docker compose pull
docker compose up -d
```

`compose.yaml` 使用 GHCR 预构建镜像，`compose-local.yaml` 用于本地构建。

## 账号总览

登录后默认进入“账号总览”，也可以从侧栏打开。GPT 与即梦分别展示账号总数、启用数、禁用数，按现有账号记录计数。点击数量进入原有账号列表及其启用状态筛选。

总览页面可见时每 30 秒读取一次现有统计接口，也可以点击“刷新数据”。实时折线图支持切换 GPT / 即梦，悬停或使用左右方向键查看本次页面打开期间各个时间点的总数、启用数和禁用数。账号管理继续使用原有的列表、搜索、状态筛选、详情及批量操作。

图表只在当前页面内存中保留最近 240 次成功读取的统计结果，不新增数据表或后台采集任务，也不保存到浏览器存储。刷新或离开总览页面后重新记录；页面隐藏时暂停更新，恢复可见时读取最新数量。尚未取得第二个记录点时显示单个数据点，长时间没有读取结果的区间保留断点。

`GET /api/v1/overview` 由管理员 Cookie 会话保护，直接查询现有账号表，仅返回 `data.tokens`、`data.dreamina` 和毫秒时间戳 `data.as_of`。每个平台包含 `total`、`enabled`、`disabled` 三项数量，前端复用同一响应更新统计卡片和实时折线图。

## 导入与导出

Token 支持直接粘贴内容，或上传 `.txt` / `.csv` 文件。可以每行只放一个 Access Token：

```text
access-token-value
another-access-token-value
```

也可以使用完整格式，Session Token 和描述可省略：

```csv
Example,access-token-value,session-token-value,描述
Another,another-access-token-value,,
```

点击“预览”可查看新增、重复和格式错误的行。导入按 Access Token 去重，跳过文件内及数据库中已有的 Token；名称相同但 Access Token 不同的记录仍会新增。勾选列表记录后点击“导出选中”，会下载 `access_tokens_时间戳.txt`，每行一个 Access Token 明文，请妥善保管。

即梦 Session 支持直接粘贴内容，或上传 `.txt` / `.csv` 文件。每行一个 Session，无需表头，描述可省略：

```csv
user@example.com,password-value,session-value,描述
user@example.com,password-value,another-session
```

点击“预览”可查看新增、重复和格式错误的行，也可以直接点击“导入”。按 Session ID 跳过文件内及数据库中已有的重复记录，同一邮箱的不同 Session 可以分别导入。字段中包含逗号时请用双引号包裹。导入任务完成后账号列表自动刷新。

即梦仍兼容带表头的 CSV：

```csv
email,password,session_id,session_expires_at,status,note
user@example.com,password-value,session-value,,enabled,
```

即梦点击“导出启用 Session”会下载 ZIP：每 300 个启用且非空的 Session ID 生成一个范围命名的 TXT，并额外生成包含全部 Session 的 `session-all.txt`；文件内使用 `|` 分隔 Session ID。

## 实时日志

在“系统管理 → 实时日志”中查看 Go 服务的运行日志，包括 HTTP 请求、批量任务、账号刷新和上游请求异常。页面先显示最近记录，随后持续接收新日志；时间按北京时间显示。支持级别筛选、消息和字段搜索、自动滚动及清空当前视图。

日志缓存在内存中，最多保留最近 1,000 条，并限制总大小和单条长度；服务重启后重新记录。断线后自动重连，缓存仍在的日志会继续补发，发生覆盖时页面会提示。“清空当前视图”只清理当前页面显示的记录。

接口 `GET /api/v1/runtime-logs/stream` 使用 SSE，由管理员 Cookie 会话保护，并在连接期间定期检查登录有效性。网页日志中的密码、Token、Cookie、Authorization 等敏感字段会脱敏；原有终端日志继续输出。此功能采集后端应用日志，本地 Vite 和构建工具的输出由各自进程管理。

## API Key

在“系统设置 → API Key 管理”中点击“新增 Key”，输入名称（最多 15 个字符）并保存。完整 Key 仅在创建成功的弹窗中显示和复制，请立即保存；关闭弹窗、刷新页面或重启后无法再次查看。列表只显示脱敏值。Key 持续有效到被删除，删除后该 Key 的后续请求立即失效。

数据库只保存鉴权哈希和展示用的部分字符，不保存完整 Key。

列表的“最近使用时间”记录 Key 最近一次成功通过接口鉴权的时间，按北京时间显示，尚未使用时显示“未使用”。业务操作失败但鉴权成功的请求也计入使用；查看设置或使用 Cookie 调用不会更新此时间。

API Key 直接用于现有 Token 接口，通过请求头 `Authorization: Bearer <API Key>` 鉴权：

| 操作 | 接口 |
| --- | --- |
| 列表、详情 | `GET /api/v1/tokens`、`GET /api/v1/tokens/{id}` |
| 新增 | `POST /api/v1/tokens` |
| 修改 | `PATCH /api/v1/tokens/{id}` |
| 删除 | `DELETE /api/v1/tokens/{id}` |
| 检查、刷新 | `POST /api/v1/tokens/{id}/checks`、`POST /api/v1/tokens/{id}/refreshes` |

下面的 curl 示例适用于 macOS / Linux Shell。使用 API Key 调用这 7 个接口时不需要 Cookie 或 CSRF。先替换地址、完整 Key 和账号 ID，再按需执行各条命令：

```sh
BASE_URL='http://127.0.0.1:8500'
API_KEY='ahk_替换为你的完整APIKey'
TOKEN_ID='tok_替换为账号ID'
```

```sh
# 1. 查询账号列表
curl -sS --get "$BASE_URL/api/v1/tokens" \
  -H "Authorization: Bearer $API_KEY" \
  --data-urlencode 'page=1' \
  --data-urlencode 'page_size=20'


# 2. 查询账号详情
curl -sS "$BASE_URL/api/v1/tokens/$TOKEN_ID" \
  -H "Authorization: Bearer $API_KEY"


# 3. 新增账号
curl -sS -X POST "$BASE_URL/api/v1/tokens" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: create-token-001' \
  --data-raw '{
    "name": "测试账号",
    "email": "user@example.com",
    "access_token": "替换为账号的AccessToken",
    "session_token": "替换为账号的SessionToken",
    "status": "enabled",
    "note": "通过API创建"
  }'


# 4. 修改账号
# 将 version 改成详情接口 data.token.version 返回的最新值
curl -sS -X PATCH "$BASE_URL/api/v1/tokens/$TOKEN_ID" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  --data-raw '{
    "version": 1,
    "name": "修改后的账号名称",
    "status": "enabled",
    "note": "通过API更新"
  }'


# 5. 检查账号凭据（不需要请求体）
curl -sS -X POST "$BASE_URL/api/v1/tokens/$TOKEN_ID/checks" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Idempotency-Key: check-token-001'


# 6. 刷新账号凭据（不需要请求体）
# 账号需要已保存有效的 session_token
curl -sS -X POST "$BASE_URL/api/v1/tokens/$TOKEN_ID/refreshes" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Idempotency-Key: refresh-token-001'


# 7. 删除账号
curl -sS -X DELETE "$BASE_URL/api/v1/tokens/$TOKEN_ID" \
  -H "Authorization: Bearer $API_KEY"
```

参数与返回说明：

- 请求头的 API Key 是 Account Hub 调用凭证，请求体的 `access_token` 和 `session_token` 是被管理账号的凭据。
- 新增账号时 `access_token` 必填，`session_token` 可省略；创建成功返回 `201`，从 `data.token.id` 获取账号 ID。
- 修改账号时 `version` 必填，使用详情接口 `data.token.version` 返回的最新值；其他字段按需提交，版本冲突返回 `409`。禁用账号可提交 `"status": "disabled"`。
- 新增、检查和刷新支持可选的 `Idempotency-Key`。同一次操作重试时沿用相同值和请求内容；发起新操作时换一个值。每个 API Key 的幂等记录相互隔离。
- 列表和详情返回脱敏凭据。

上述接口同时支持原有 Cookie 登录态；Cookie 写请求仍校验 CSRF。提供 Authorization 请求头时必须使用有效 API Key，否则返回 `401`，不回退到 Cookie。API Key 不授权设置、密码、导入导出、内容、任务或即梦账号接口；这些接口继续使用管理员登录态。

Key 管理复用 `GET/PATCH /api/v1/settings`，由管理员 Cookie 与 CSRF 保护：GET 增加 `data.api_keys`；PATCH 的 Key 操作独立提交，不与 `settings` 更新混用。

```json
{"api_key_action":{"action":"create","name":"自动化脚本"}}
```

创建返回 `201`，`data.api_key` 为脱敏元数据，`data.secret` 为仅返回一次的完整 Key。管理操作仅支持新增和删除。

完整 Key 不进入数据库、列表、日志或持久化响应缓存。删除提交：

```json
{"api_key_action":{"action":"delete","id":"key_对应ID"}}
```

迁移 `003_account_operations.sql` 统一包含本次账号操作、任务执行和 API Key 功能的数据库变更，应用启动时自动执行。独立的 Key 表保存哈希、脱敏值、名称及时间信息，并为审计日志增加 API Key 身份字段。删除保留历史身份信息，审计日志可追溯使用过的 Key。

Key 使用 32 字节安全随机数生成，接口鉴权通过 SHA-256 哈希匹配，不使用可逆加密，也不生成或依赖独立密钥文件。旧数据库启动时会移除此前保存密文的字段，保留原有哈希，因此已有 Key 的接口调用能力保持有效；旧的 `account-hub.db.api-key.key` 文件可删除。

## 固定运行参数

- HTTP：`0.0.0.0:8500`
- SQLite：`backend/data/account-hub.db`
- 展示时区：`Asia/Shanghai`
- 管理员会话：24 小时
- 上游超时：30 秒
- 临时错误重试：2 次；积分领取不自动重试
- Job 扫描：5 秒
