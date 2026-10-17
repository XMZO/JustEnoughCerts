# Just Enough Certs — 项目说明（给 Claude Code）

自托管的 SSL/TLS 证书管理面板：从各种来源获取证书，自动完成域名验证，部署到 CDN 和服务器，到期前自动续期。名字和视觉致敬 Minecraft 的 JEI 模组。口号：Just enough certs to keep your site green. MIT 许可。

- 完整规划：`PLAN.md`（架构、模块接口、i18n、部署、CI、路线图都在里面，动手前先读对应章节）
- 界面设计：`docs/design/`（设计系统 + 32 个页面设计稿，见 `docs/design/README.md`）

## 必须遵守

- **提交署名**：提交作者用仓库主人 XMZO。提交信息和 PR 里不加 `Co-Authored-By: Claude`，也不加任何 Claude 署名。
- **模块化可插拔**：证书来源、域名验证、部署目标、通知都是模块，实现 `PLAN.md` 4.2 和 5.1 里的接口。核心代码不依赖任何具体服务商；新增或删除一个模块不需要改核心。
- **i18n 严格隔离**：界面文字只写在每种语言一个的语言文件里（先做 `zh-CN` 和 `en-US`），代码里不写死任何界面文字，包括错误信息和通知内容。测试和 CI 里有 i18n 门禁（`PLAN.md` 7.4），不能绕过。
- **镜像要小，但不丢功能**：`scratch` 基础镜像，内置 CA 根证书和时区数据。不用 UPX 之类的压缩，会影响性能。
- **不做 Windows 原生构建**。发布：按 `v*.*.*` 标签自动构建 linux amd64/arm64 原生二进制和 ghcr.io 多架构镜像。
- **单文件部署**：一个 `docker-compose.yml` 就能跑起来。
- **密钥不进仓库**：阿里云、腾讯云、Namecheap 等 API 密钥只放在部署机器的配置或环境变量里，示例里用占位值。

## 技术栈（已定）

- 后端：Go，chi + huma（OpenAPI），SQLite（驱动用 `ncruces/go-sqlite3`，选型数据见 `PLAN.md` 8.3），goose 迁移，go-acme/lego v5，log/slog，mvdan.cc/sh 跑钩子脚本。
- 前端：Vue 3 + TypeScript + Vite + Tailwind CSS + shadcn-vue，Vue Router，Pinia，TanStack Query，vue-i18n，图标 Lucide（按需引入），不打包网页字体。
- 发布：GoReleaser + Docker Buildx，cosign 签名，syft 生成 SBOM。

## 界面设计要点

设计稿和规范以 `docs/design/` 为准；它比 `PLAN.md` 第 6 节新，两者冲突时按设计稿。

- 颜色、字号、间距、圆角全部用 `docs/design/system/tokens.json` 里的 token，浅色和深色两套都校验过对比度。组件样式参考 `docs/design/system/bundle.css`，用法说明在 `docs/design/system/components/`。
- 唯一的个性元素是**耐久条**（LifeBar，证书剩余寿命）和**物品栏格子**（CertSlot，总览里每张证书一个格子）。这两个造型只表示证书寿命，不用在别处。
- 任何状态都同时有图标和文字，不只靠颜色。
- 用户偏好：**不要太多折叠栏和分页**；手机端不要拖得很长，但也不能太密。证书列表在手机上试过三种密度，定下来的是“需要处理的证书整行显示原因 + 正常的证书两列小格”。记录类列表用时间范围筛选代替分页。
- 添加证书是一整页表单，不做分步向导。

## 背景和已经查清的事实

用户自己的使用场景（也是 M2 的验收场景）：域名在阿里云 ESA 国内版建站（`esa.cn-hangzhou.aliyuncs.com`），DNS 在腾讯云 DNSPod，证书是 Namecheap 买的 PositiveSSL。证书最长有效期已降到 200 天，一年的订阅要签两张，需要自动重签、验证、部署到 ESA、删掉旧证书。

- **Namecheap API**：`namecheap.ssl.reissue` / `ssl.activate` / `ssl.getInfo`，域名验证用 `DNSDCValidation=CNAME`。开通门槛：账号里有 20 个域名，或余额 $50，或近两年消费满 $50；必须设置 IPv4 白名单。重签后证书 ID 会变。
- **Namecheap SSL Manager**：Namecheap / Spaceship 自有的闭源工具（后端 externalautossl.service.spaceship.com），不是通用协议。只作为实验模块，默认关闭。
- **阿里云 ESA API（2024-09-10 版）**：`SetCertificate`（Type=upload）、`ListCertificates`、`DeleteCertificate`。免费版每个站点最多 2 张自定义证书，所以部署新证书后要删旧证书。RAM 子账号最小权限：`esa:ListSites`、`ListCertificates`、`SetCertificate`、`DeleteCertificate`。
- **通用路线**：以 ACME + EAB（RFC 8555）为核心，大多数 CA 都支持；lego 内置 225 个 DNS 服务商。厂商专有 API（如 Namecheap）是可选模块。

## 进度

- 已完成：`PLAN.md`；设计系统和全部页面设计稿（`docs/design/`）；M0 第一段：`jec serve` / `healthcheck` / `version`，分层配置，`scratch` 镜像，GoReleaser，CI 和按标签发布的流水线（`.github/workflows/`）；SQLite 驱动选型；i18n 模块（`i18n/`：语言文件、ICU 子集解析和格式化、key 常量生成）和后端门禁（`go run ./tools/i18n check`、`go run ./tools/i18nlint ./...`，新增或改动文字后跑 `go run ./tools/i18n fix`）；内核：`internal/kernel/store`（SQLite + goose，内核和每个模块各有一张迁移版本表）、`internal/kernel/module`（Manifest、按依赖排序、启用状态存在 `kernel_modules` 表）、`internal/kernel/api`（huma，`/api/v1`，错误一律是 `{"code": key, "params", "fields"}`，`GET /api/v1/manifest`）；模块：一个 ID 一个目录 `internal/modules/<id>/`，注册文件 `internal/modules/all/<id>.go`（build tag `!no_<id>`），新模块用 `make new-module id=xxx category=source` 生成（`PLAN.md` 4.2），模块之间、内核和模块之间不能 import（`internal/modules` 的架构测试）；账号：`internal/kernel/auth`（一次性初始化令牌打印在日志里，`/api/v1/setup`、`/api/v1/session`，argon2id，会话 Cookie，CSRF 防护，连续失败 5 次锁 15 分钟，API 默认要求登录，公开接口用 `api.Public`；`jec reset-password` 改密码）；前端骨架：`web/`（Vue 3 + Vite + Tailwind，登录页、初始化向导、总览占位页，`go:embed` 进二进制，详见 `PLAN.md` 6.3），前端用到的 key 也算进"未使用 key"检查，`MessageKey` 类型和语言列表由 `go generate ./i18n` 生成到 `web/src/i18n/generated.ts`；改了 API 要跑 `make gen`（或 `go run ./tools/openapi` 再 `pnpm -C web gen:api`）；伪语言测试（`web/e2e/`，`PLAN.md` 7.4 第 5 条）：新页面要在里面走到，截断的文字要带完整内容的 `title`；体积预算：程序 20 MiB，压缩包和镜像 8 MiB，在 `scripts/release-sizes.sh`，超出时 CI 失败，要提高就在那次改动里说明原因（`PLAN.md` 8.3）。
- `v0.0.1` 已于 2026-10-17 发布（M0 完成）。README 有中英两份（`README.md`、`README.zh-CN.md`），改一份要同步另一份。
- M1 进行中：已完成事件总线 `internal/kernel/events`（`PLAN.md` 4.5，事件和业务变化在同一个事务里写入，至少送达一次）、调度器 `internal/kernel/jobs`、重试规则 `internal/kernel/retry`（`PLAN.md` 4.4），模块通过 `r.Events`、`r.Jobs` 使用。
- M1 还完成了：主密钥和加密存储 `internal/kernel/secrets`（`JEC_SECRET_KEY` 或数据目录里的 `secret.key`，模块用 `r.Secrets`）；证书解析 `internal/lib/cryptox`（测试用证书在 `cryptoxtest`）；跨模块的事件类型和数据结构 `internal/kernel/contracts`；证书模块 `internal/modules/certs`（`PLAN.md` 4.6，目前支持上传、新版本、到期提醒事件）。
- 前端：应用框架 `web/src/components/AppShell.vue`（侧栏、手机底部标签栏）、总览、证书列表、证书详情（上传新版本、下载、删除、续期设置；下载前要重新输入密码）、添加证书（目前只有手动上传）、手机端“更多”页；证书寿命的计算和文字在 `web/src/certs/life.ts`。单独显示的日期用 `<time>`、数字用 `<data>`、域名等数据加 `translate="no"`，伪语言测试会跳过它们（`PLAN.md` 7.4）。
- 下一步：ACME + lego 和签发状态机（签发中、失败两种状态，签发记录页），然后是部署目标和通知。事件总线和调度器放到 M1，和签发状态机一起设计（通知、部署要求事件可靠送达）。

## 工程约定

- 代码、注释、脚本、部署文件都用英文；中文只出现在语言文件和文档里（i18n 门禁会扫描）。命令行输出和运维日志按 `PLAN.md` 7.3 算开发者日志，用英文。
- Go：`go.mod` 只写最低版本（1.26），CI 和发布用最新稳定版。GitHub Actions 一律钉到 commit SHA 并在注释里写版本号，由 Dependabot 每周更新。
- 容器默认以 65532:65532 运行，不在启动时切换用户；挂载的数据目录要先 `chown`，启动检查会给出明确报错。
- ghcr.io 上的镜像包从 v0.0.1 起就是公开的（发布后实测匿名能拉取），不需要手动改可见性。
