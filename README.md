# Account Hub

全新的 Go + React 账号凭据管理后台。项目与仓库中的旧 Python/Vue 系统完全独立，不读取或修改旧代码和旧数据库。

## 功能

- 管理员 Cookie 会话、CSRF、防暴力登录和 Argon2id 密码哈希
- OpenAI/Sora GPT账号、检查与刷新
- GPT 账号存储用量、图片列表/预览/批量删除，以及聊天记录分页、消息与图片查看
- 即梦账号管理、Session 刷新、积分查询/领取与历史作品
- CSV 预检/导入、元数据导出、持久化批量任务和自动刷新
- 精确 10 项系统设置、审计日志、健康检查
- Radix UI + Tailwind CSS 管理端，图标统一使用 Lucide React，支持暗色、亮色和跟随系统

ChatGPT/Sora 的 Token 检查和刷新使用 `tls-client v1.16.0`，固定 Chrome 150 的 TLS/HTTP/2 指纹及对应请求头，HTTP/3 关闭。沿用系统设置中的 HTTP、HTTPS 或 SOCKS5 代理以及现有超时、重试规则。各账号逐次传入自己的凭据，不共享 Cookie Jar；即梦使用独立的标准 HTTP 客户端。

## 本地运行

依赖 Go 1.26、Node.js 24 和 pnpm。

从 `account-manage/` 一键启动 Go API 和 Vite 开发服务器：

```sh
export ADMIN_PASSWORD="replace-with-a-strong-password"
sh ./run.sh
```

也可以在 `account-manage/.env` 中设置 `ADMIN_PASSWORD`；数据库已经初始化后该变量可省略。

脚本先下载 Go 依赖并编译后端，等待 <http://127.0.0.1:8500/health/ready> 就绪后，再在 <http://localhost:8501> 启动开发页面，将 `/api` 与 `/health` 代理到 `127.0.0.1:8500`。按 `Ctrl+C` 会同时停止前后端。

未自定义 `GOPROXY` 时，脚本会将 Go 的默认下载源改为本次启动使用的 `https://goproxy.cn|https://proxy.golang.org|direct`，支持连接超时后切换下载源。可在 `account-manage/.env` 中设置 `GOPROXY='你的下载源'`；环境变量和已有的自定义 Go 配置优先。依赖下载或编译失败时，脚本会直接退出并显示原因。

手动运行时，先启动后端：

```powershell
cd account-manage\backend
$env:GOPROXY = 'https://goproxy.cn|https://proxy.golang.org|direct'
$env:ADMIN_PASSWORD = "replace-with-a-strong-password"
go mod download
go run ./cmd/account-hub
```

再在另一个终端启动前端：

```powershell
cd account-manage\frontend
pnpm install --ignore-scripts
pnpm dev
```

首次空数据库启动必须提供至少 12 个字符的 `ADMIN_PASSWORD`。初始化后可移除该环境变量并重启，此后可以在页面修改密码；如果持续提供，密码由环境变量托管。

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
$env:ADMIN_PASSWORD = "replace-with-a-strong-password"
docker compose build
docker compose up -d
```

数据库已经初始化且希望由页面维护密码时，可在后续启动前移除 `ADMIN_PASSWORD`。Compose 仅透传宿主机已经设置的同名变量，不提供默认值。

`compose.yaml` 只属于本项目，不会调用或修改仓库根目录的旧 Compose 配置。

## CSV 格式

Token：

```csv
name,email,access_token,session_token,access_expires_at,status,note
Example,user@example.com,access-value,session-value,,enabled,
```

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

Token 上传时先执行预检，确认无错误后再正式导入。普通导出只包含元数据，不包含 Token、Session 或密码原文。

## 固定运行参数

- HTTP：`0.0.0.0:8500`
- SQLite：`backend/data/account-hub.db`
- 展示时区：`Asia/Shanghai`
- 管理员会话：24 小时
- 上游超时：30 秒
- 临时错误重试：2 次；积分领取不自动重试
- Job 扫描：5 秒
