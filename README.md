# New API

`new-api` 是由 `QuantumNous` 维护的统一 AI 网关与管理平台，负责把多家上游模型服务聚合到同一套 API 与管理后台中，提供渠道管理、令牌分发、用户体系、计费、日志、限流和运维能力。

## 项目概览

- 后端：Go、Gin、GORM
- 前端：React 18、Vite、Semi Design
- 数据库：SQLite / MySQL / PostgreSQL
- 缓存：Redis + 本地缓存
- 能力：统一 AI 转发、用户管理、令牌与渠道管理、计费统计、后台管理、多上游兼容

## 当前登录与注册规则

当前项目已调整为以下认证规则：

- 注册必须同时提交 `邮箱`、`用户名`、`密码`
- 注册邮箱必须为 `qq.com` 后缀
- 登录时支持使用 `用户名` 或 `邮箱`
- 邮箱登录会先做小写归一化，再参与查询

## 目录结构

```text
router/        HTTP 路由
controller/    请求处理
service/       业务逻辑
model/         数据模型与数据库访问
middleware/    鉴权、限流、日志、中间件
relay/         各上游渠道转发适配
common/        公共能力与工具函数
setting/       系统配置
web/           前端项目
i18n/          后端国际化
oauth/         第三方登录实现
pkg/           内部公共包
```

## 本地开发

### 1. 后端

```bash
go mod download
go run main.go
```

默认启动后可通过 `http://127.0.0.1:3000` 访问。

### 2. 前端

项目约定优先使用 `bun`：

```bash
cd web
bun install
bun run dev
```

生产构建：

```bash
cd web
bun run build
```

## 通用部署方式

仓库保留了基于 Docker Compose 的通用部署方案，适合快速启动：

```bash
docker-compose up -d
```

默认编排中包含：

- `new-api` 主服务
- `postgres` 数据库容器
- `redis` 缓存容器

启动后可访问：

```text
http://127.0.0.1:3000
```

## 当前生产环境说明

当前线上环境采用“源码构建 + systemd 托管”方式，而不是容器直接运行主服务。

### 服务器结构

- 部署用户：`deployer`
- 部署目录：`/home/deployer/new-api`
- 进程管理：`systemd`
- 服务名：`new-api`
- 监听端口：`3000`
- 反向代理：`nginx` 将 `80/443` 转发到 `3000`
- 数据库：PostgreSQL 容器

### 服务检查

```bash
sudo systemctl status new-api
curl http://127.0.0.1:3000/api/status
```

### 当前生产构建方式

服务器上已统一到新的 Go 路径，使用：

```bash
/usr/local/go/bin/go build -o new-api main.go
```

前端产物在服务器上使用：

```bash
cd web
npm install
npm run build
```

说明：

- 项目约定前端优先使用 `bun`
- 当前生产机未安装 `bun`，所以实际构建使用 `npm`
- 旧的 Go 1.19 路径已清理，当前以 `/usr/local/go/bin/go` 为准

## 生产更新流程

建议按以下顺序更新：

1. 本地完成代码修改并提交到 GitHub
2. 服务器进入 `/home/deployer/new-api`
3. 拉取最新代码
4. 构建前端静态资源
5. 使用 `/usr/local/go/bin/go build -o new-api main.go` 重新编译后端
6. 执行 `sudo systemctl restart new-api`
7. 用 `curl http://127.0.0.1:3000/api/status` 验证服务健康

## 这次整理后的仓库说明

本次已同步整理以下内容：

- 清理本地构建和临时产物
- 清理旧的 README 多语言副本
- 合并为单一中文 `README.md`
- 补充新的注册/登录规则说明
- 补充当前生产环境的真实部署方式

## 维护说明

- 生成目录如 `web/dist`、本地缓存、临时日志、部署中转文件不建议提交到仓库
- 前端优先使用 `bun`，但服务器应以实际环境为准
- 涉及数据库操作时，必须同时兼容 SQLite、MySQL 和 PostgreSQL
- 业务代码中的 JSON 编解码应统一走 `common/json.go`

## 合规说明

- `new-api` 与 `QuantumNous` 相关标识为项目受保护信息，不应删除或替换
- 使用本项目时，请自行确保符合所在地区法律法规及上游服务条款

## 许可证

项目许可证见仓库中的 [LICENSE](LICENSE)。
