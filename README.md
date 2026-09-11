# Account Hub

全新的 Go + React 账号凭据管理后台。项目与仓库中的旧 Python/Vue 系统完全独立，不读取或修改旧代码和旧数据库。

## 功能

- 管理员 Cookie 会话、CSRF、防暴力登录和 Argon2id 密码哈希
- OpenAI/Sora GPT账号、检查与刷新
- GPT 账号存储用量、图片列表/预览/批量删除，以及聊天记录分页、消息与图片查看
- 即梦账号管理、Session 刷新、积分查询/领取与历史作品
- TXT/CSV 预检与批量导入、选中 Token 导出、启用 Session ZIP 导出、持久化批量任务和自动刷新
- 精确 10 项系统设置、审计日志、健康检查
- Radix UI + Tailwind CSS 管理端，图标统一使用 Lucide React，支持暗色、亮色和跟随系统

ChatGPT/Sora 的 Token 检查和刷新使用 `tls-client v1.16.0`，固定 Chrome 150 的 TLS/HTTP/2 指纹及对应请求头，HTTP/3 关闭。沿用系统设置中的 HTTP、HTTPS 或 SOCKS5 代理以及现有超时、重试规则。各账号逐次传入自己的凭据，不共享 Cookie Jar；即梦使用独立的标准 HTTP 客户端。

## 本地运行

依赖 Go 1.26、Node.js 24 和 pnpm。

从 `account-manage/` 一键启动 Go API 和 Vite 开发服务器：

```sh
sh ./run.sh
```

首次启动空数据库时，系统会自动生成格式为 `hub_` 加 10 位随机字符的管理员密码，并在启动日志中输出。密码只在首次创建管理员时生成，之后保存在数据库中；如果在页面修改密码，后续重启会继续使用修改后的密码。

脚本先下载 Go 依赖并编译后端，等待 <http://127.0.0.1:8500/health/ready> 就绪后，再在 <http://localhost:8501> 启动开发页面，将 `/api` 与 `/health` 代理到 `127.0.0.1:8500`。按 `Ctrl+C` 会同时停止前后端。

未自定义 `GOPROXY` 时，脚本会将 Go 的默认下载源改为本次启动使用的 `https://goproxy.cn|https://proxy.golang.org|direct`，支持连接超时后切换下载源。可在 `account-manage/.env` 中设置 `GOPROXY='你的下载源'`；环境变量和已有的自定义 Go 配置优先。依赖下载或编译失败时，脚本会直接退出并显示原因。

手动运行时，先启动后端：

```powershell
cd account-manage\backend
$env:GOPROXY = 'https://goproxy.cn|https://proxy.golang.org|direct'
go mod download
go run ./cmd/account-hub
```

再在另一个终端启动前端：

```powershell
cd account-manage\frontend
pnpm install --ignore-scripts
pnpm dev
```

运行时数据固定写入 `backend/data/account-hub.db`。请勿将 `backend/data/` 或 `.env` 提交到版本库或复制进镜像。

## 检查与构建

```powershell
cd account-manage\backend
go test ./...
cd ..\frontend
pnpm test
pnpm typecheck
pnpm lint
pnpm build
```

生产构建：

```powershell
docker compose build
docker compose up -d
```

`compose.yaml` 只属于本项目，不会调用或修改仓库根目录的旧 Compose 配置。

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

## 固定运行参数

- HTTP：`0.0.0.0:8500`
- SQLite：`backend/data/account-hub.db`
- 展示时区：`Asia/Shanghai`
- 管理员会话：24 小时
- 上游超时：30 秒
- 临时错误重试：2 次；积分领取不自动重试
- Job 扫描：5 秒
