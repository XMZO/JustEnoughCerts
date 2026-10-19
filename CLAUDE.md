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
- 前端：Vue 3 + TypeScript + Vite + Tailwind CSS + shadcn-vue，Vue Router，Pinia，TanStack Query，vue-i18n，图标 Lucide（按需引入）。字体随界面打包三款 SIL OFL 开源字体（Inter、JetBrains Mono、思源黑体只含界面用字的子集），由 `pnpm -C web fonts` 从 Google Fonts 的固定版本生成到 `web/src/assets/fonts`；中文消息用了新字时，`web/src/fonts.test.ts` 会失败，重新生成即可。
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

- **Namecheap SSL Manager**（M2 的第一个厂商来源，`PLAN.md` 5.6）：Namecheap 给直客的服务器端自动化工具，闭源，后端 externalautossl.service.spaceship.com。账户面板激活或重签时选 “Server-side automation”，得到安装命令和一次性令牌（24 小时有效）；装好后靠 cron 自动取得每一张 200 天的证书。私钥和 CSR 在本地生成，HTTP 文件验证，目前只支持单域名证书。JEC 扮演它的客户端，做成实验模块，默认关闭。协议已从官方客户端 v1.15.10 静态分析出来（没有运行它），写在 `PLAN.md` 5.6：全是 POST + JSON + Bearer 令牌，注册、刷新令牌、按域名列证书、交 CSR、取 HTTP 验证、报告已放好、下载 PEM、确认安装；还有几点要用真实账号试。
- **Namecheap 官方 API**（排到“以后”）：`namecheap.ssl.reissue` / `ssl.activate` / `ssl.getInfo`，域名验证用 `DNSDCValidation=CNAME`。开通门槛：账号里有 20 个域名，或余额 $50，或近两年消费满 $50；必须设置 IPv4 白名单。重签后证书 ID 会变。
- **阿里云 ESA API（2024-09-10 版）**：`SetCertificate`（Type=upload）、`ListCertificates`、`DeleteCertificate`。免费版每个站点最多 2 张自定义证书，所以部署新证书后要删旧证书。RAM 子账号最小权限：`esa:ListSites`、`ListCertificates`、`SetCertificate`、`DeleteCertificate`。
- **通用路线**：以 ACME + EAB（RFC 8555）为核心，大多数 CA 都支持；lego v5.5.2 内置 223 个 DNS 服务商（去掉 `manual`、`exec` 后 221 个可用）。lego v5 删除了旧的 `dnspod`，DNSPod 用腾讯云 API 密钥（`tencentcloud`）。厂商专有 API（如 Namecheap）是可选模块。
- **lego 的坑**（`PLAN.md` 5.5）：DNS 服务商只能靠环境变量配置，所以放在子进程里跑；全部服务商会让程序涨到约 69 MiB；lego 会改写传入的 `http.Client`；依赖里已有阿里云 ESA 2024-09-10 版的 Go SDK（`github.com/go-acme/esa-20240910/v3`），ESA 部署目标可以直接用。

## 进度

- 已完成：`PLAN.md`；设计系统和全部页面设计稿（`docs/design/`）；M0 第一段：`jec serve` / `healthcheck` / `version`，分层配置，`scratch` 镜像，GoReleaser，CI 和按标签发布的流水线（`.github/workflows/`）；SQLite 驱动选型；i18n 模块（`i18n/`：语言文件、ICU 子集解析和格式化、key 常量生成）和后端门禁（`go run ./tools/i18n check`、`go run ./tools/i18nlint ./...`，新增或改动文字后跑 `go run ./tools/i18n fix`）；内核：`internal/kernel/store`（SQLite + goose，内核和每个模块各有一张迁移版本表）、`internal/kernel/module`（Manifest、按依赖排序、启用状态存在 `kernel_modules` 表）、`internal/kernel/api`（huma，`/api/v1`，错误一律是 `{"code": key, "params", "fields"}`，`GET /api/v1/manifest`）；模块：一个 ID 一个目录 `internal/modules/<id>/`，注册文件 `internal/modules/all/<id>.go`（build tag `!no_<id>`），新模块用 `make new-module id=xxx category=source` 生成（`PLAN.md` 4.2），模块之间、内核和模块之间不能 import（`internal/modules` 的架构测试）；账号：`internal/kernel/auth`（一次性初始化令牌打印在日志里，`/api/v1/setup`、`/api/v1/session`，argon2id，会话 Cookie，CSRF 防护，连续失败 5 次锁 15 分钟，API 默认要求登录，公开接口用 `api.Public`；`jec reset-password` 改密码）；前端骨架：`web/`（Vue 3 + Vite + Tailwind，登录页、初始化向导、总览占位页，`go:embed` 进二进制，详见 `PLAN.md` 6.3），前端用到的 key 也算进"未使用 key"检查，`MessageKey` 类型和语言列表由 `go generate ./i18n` 生成到 `web/src/i18n/generated.ts`；改了 API 要跑 `make gen`（或 `go run ./tools/openapi` 再 `pnpm -C web gen:api`）；伪语言测试（`web/e2e/`，`PLAN.md` 7.4 第 5 条）：新页面要在里面走到，截断的文字要带完整内容的 `title`；体积预算：程序 20 MiB，压缩包和镜像 8 MiB，在 `scripts/release-sizes.sh`，超出时 CI 失败，要提高就在那次改动里说明原因（`PLAN.md` 8.3）。
- `v0.0.1` 已于 2026-10-17 发布（M0 完成）。README 有中英两份（`README.md`、`README.zh-CN.md`），改一份要同步另一份。
- M1 进行中：已完成事件总线 `internal/kernel/events`（`PLAN.md` 4.5，事件和业务变化在同一个事务里写入，至少送达一次）、调度器 `internal/kernel/jobs`、重试规则 `internal/kernel/retry`（`PLAN.md` 4.4），模块通过 `r.Events`、`r.Jobs` 使用。
- M1 还完成了：主密钥和加密存储 `internal/kernel/secrets`（`JEC_SECRET_KEY` 或数据目录里的 `secret.key`，模块用 `r.Secrets`）；证书解析 `internal/lib/cryptox`（测试用证书在 `cryptoxtest`）；跨模块的事件类型和数据结构 `internal/kernel/contracts`；证书模块 `internal/modules/certs`（`PLAN.md` 4.6，目前支持上传、新版本、到期提醒事件）。
- 前端：应用框架 `web/src/components/AppShell.vue`（侧栏、手机底部标签栏）、总览、证书列表、证书详情（上传新版本、下载、删除、续期设置；下载前要重新输入密码）、添加证书（目前只有手动上传）、手机端“更多”页；证书寿命的计算和文字在 `web/src/certs/life.ts`。单独显示的日期用 `<time>`、数字用 `<data>`、域名等数据加 `translate="no"`，伪语言测试会跳过它们（`PLAN.md` 7.4）。
- M1 还完成了：provider 注册表 `internal/kernel/providers`（模块用 `r.Providers` 登记类型、打开实例；接口在 `/api/v1/providers`）；子进程机制 `internal/kernel/children`；lego 模块 `internal/modules/lego`（221 家 DNS 服务商，每家在子进程里跑，目录 `catalog.json` 由 `go run ./tools/legocatalog` 生成）；ACME 模块 `internal/modules/acme`（账号就是 provider 实例，用 lego 签发，CI 里对着 Pebble 测）。体积预算已按 lego 实测提高到程序 80 MiB、压缩包和镜像 27 MiB。
- M1 还完成了：证书模块的签发和续期（`PLAN.md` 4.3 的“实现”、4.6）：证书来源选 ACME 账号等来源实例、验证选 DNS 服务商实例，签发记录和逐行日志存在数据库里，失败按退避重试，进入续期窗口自动续期，签发中、失败两种状态；接口 `POST /api/v1/certs/certificates/issue` 等。
- M1 还完成了：前端的配置页 `/sources`、`/dns`（provider 表单由类型的字段生成，字段可标 `advanced`、`when`，`PLAN.md` 5.5），添加证书可选证书来源和 DNS 服务商，签发进度页 `/certificates/:id/issuances/:issuance`（步骤和日志，进行中自动刷新），证书详情的签发记录和“立即签发”。
- M1 还完成了：部署（`PLAN.md` 4.6 的“部署”）：证书选一组部署目标（类型为 target 的 provider 实例），每个新版本自动部署到每个目标，失败按退避重试，目标负责删掉 JEC 以前部署的版本；目标模块 `internal/modules/esa`（阿里云 ESA，`PLAN.md` 5.4，只用替身 API 测过，还没对真实账号验证）和 `internal/modules/files`（写到本地目录，`PLAN.md` 5.3.3）。前端：部署目标页 `/targets`，添加证书和证书详情里选目标，部署记录和日志页 `/certificates/:id/deployments/:deployment`；日志组件 `web/src/components/LogView.vue` 签发和部署共用。
- M1 还完成了：通知（`PLAN.md` 5.3.4）：分发模块 `internal/modules/notify` 订阅证书事件，按每个通知渠道选的事件和语言渲染消息（渠道都带 `contracts.NotifierFields`：`locale` 字段和事件开关），发送记录防重复，可在界面上发送测试；渠道模块 `telegram`、`email`（SMTP）、`webhook`（通知和部署两种类型）；共用的 HTTP 客户端 `internal/lib/httpx`（代理、只连公网地址）。前端通知页 `/notify`。
- 证书状态多了 `deploy_failed`（和签发失败的 `failed` 分开）。伪语言测试的标记检查以前被 `<html lang>` 整个豁免掉了，已修正：真实页面的文字现在确实会检查。
- M1 还完成了：SSH 部署目标 `internal/modules/ssh`（`PLAN.md` 5.3.3：经 SSH 执行命令写文件、一起改名，再执行设置的命令；主机公钥必须核对）。M1 计划里的部署目标和通知渠道都已具备。
- M1 还完成了：签发记录页 `/history`（`GET /api/v1/certs/runs`，签发、续期、部署合在一起，按天分组、筛选、导出 CSV）和总览的“最近动态”；手机底部标签栏是总览、证书、签发记录、更多。
- M1 还完成了：测试连接（`POST /api/v1/providers/{id}/test`，类型标 `testable`，provider 实现 `contracts.Tester`）：阿里云 ESA、SSH、本地目录、ACME 账号。
- M2 进行中：厂商来源的通用框架已完成（`PLAN.md` 4.3 的“等待来源”：来源返回 `contracts.Wait` 时签发进入 `waiting`，私钥和来源的状态加密存在签发记录里，跨多次检查；彻底失败但来源还有订单时，下一次签发接着做）。
- M2 还完成了：HTTP 文件验证（`PLAN.md` 5.3.2 ④）：`contracts.FileSolver`，验证类型的 `challenge`（`dns`/`http`）和来源类型的 `challenges`，文件只能放在 `/.well-known/` 下；`files.webroot`（本地网站目录）、`ssh.webroot`（SSH 网站目录）；ACME 用网站目录时走 HTTP-01。
- M2 还完成了：模块页 `/modules`（`PLAN.md` 6.1，`PUT /api/v1/modules/{id}` 在运行中开关模块，实验模块要在这里打开）。
- M2 还完成了：`namecheap` 模块（`PLAN.md` 5.6 的“JEC 的做法”）：类型 `namecheap.ssl_manager`，实验模块默认关闭；一次性令牌注册、access token 加密保存和刷新（`namecheap.refresh` 任务）；签发按 Namecheap 的证书状态一步步走（交 CSR、经网站目录类验证方式放验证文件、等 CA、下载并核对私钥），只签 RSA；测试用假服务器（`vendor_test.go`），还没有用真实账号试过。类型列表和表单会标出实验类型。
- M2 还完成了：JEC 自己应答（模块 `wellknown`，类型 `wellknown.answer`，`PLAN.md` 5.3.2 ④）：内核的 `r.WellKnown` 让模块回答根路径 `/.well-known/` 下的请求（不要登录，回 404 交给下一个模块）；测试连接从外网经填的域名取回临时文件。
- 测试版 `v0.1.0-alpha.1`、`v0.1.0-alpha.2`（加了 ESA 边缘应答）已于 2026-10-17 发布，`v0.1.0-alpha.3`（修了读不出阿里云错误码的问题，加了 M3 的部署目标、通知渠道、插件、两步验证和手动 DNS 验证）、`v0.1.0-alpha.4`（加了 NPM、Kubernetes、AWS ACM、群晖、七牛部署目标，Discord、Slack、Pushover 通知，ACME 续期信息，安全响应头，API 令牌，审计日志，在界面里恢复备份）于 2026-10-18 发布（预发布：镜像标签是版本号和 `beta`，`latest` 不动），给用户用真实账号试。
- M2 还完成了：ESA 边缘应答（验证类型 `esa.edge`，在 `esa` 模块里，`PLAN.md` 5.4）：验证文件存进 ESA 的边缘 KV，第一次用时自动建 KV 命名空间 `jec_validation`、边缘函数 `jec-validation`（代码 `internal/modules/esa/routine.js`，经 OSS 上传）和站点上 `/.well-known/` 的路由；放好后从外网取到才算放好。`httpx` 的 `SkipVerify`：核对验证文件时像 CA 一样跟随跳转、不校验证书（wellknown、namecheap 也用）。测试用替身 API 和替身边缘，装了 Node.js 时实际运行函数代码；还没有用真实账号试过。
- 设置页 `/settings`（按设计稿，改动立即保存）：语言、续期默认值（证书模块的 `certs_settings`，`GET`/`PUT /api/v1/certs/settings`：提前几天、每天检查时间、默认密钥类型）、账号和安全（改密码 `PUT /api/v1/account/password`，其他地方的登录随之退出）、面板访问和数据（内核的 `internal/kernel/system`：`GET /api/v1/system`；下载备份 `POST /api/v1/system/backup`，要密码，`VACUUM INTO` 出副本）、关于（清单接口带了构建日期和平台）。续期改为进入续期窗口后在每天检查时间开始（默认 02:00，服务器时区），只剩一周或更少时立即开始；证书接口带 `renewAt`。两步验证（TOTP，`internal/kernel/auth/twofactor.go`，`/api/v1/account/two-factor/*`，登录页按 `kernel.error.two_factor_required` 再要验证码；`jec reset-password` 同时关闭两步验证）和登录有效期（`PATCH /api/v1/account`，1/7/30 天，默认 7）已做；在界面里恢复备份也做了（`POST /api/v1/system/restore` 检查后放成 `restore.db`，`jec serve` 以退出码 3 停下，下次启动时 `store.ApplyRestore` 换上并留下原来的数据库）。
- 本机证书目录（来源类型 `files.watch`，`PLAN.md` 5.3.3）：取进 certbot、acme.sh 在本机续期的证书和私钥，只读；来源接口多了 `contracts.KeySource`（类型标 `ownKey`，不生成私钥、不选密钥类型），文件里还是旧证书时等待 6 小时再看。M1 的导入就此做完（PEM、PFX/P12 上传，读目录）。
- ACME 续期信息（ARI，RFC 9773，`PLAN.md` 5.5）：`contracts.RenewalAdvisor`，任务 `certs.renewal_info` 存 CA 建议的窗口和窗口里随机选的时间（`certs_versions` 的 `renewal_*` 列），续期按计划和建议中较早的那个；版本记下签发它的来源（`source_provider`），续期订单只在同一来源时带 `replaces`，被拒就去掉重下。
- 安全响应头（`PLAN.md` 10）：`internal/kernel/httpserver` 给每个回答加 CSP 等，界面的 `web.go` 不再自己设；HSTS 靠 `JEC_HSTS=true` 打开。客户端地址由 `internal/kernel/clientip` 定（`JEC_TRUSTED_PROXIES` 列出可信的反向代理，才读 `X-Forwarded-For`），登录锁定按它算。伪语言测试遇到 CSP 拦截就失败，界面不能用内联脚本、内联样式和 `eval`。
- 通知渠道又加了 Discord（`discord.webhook`）、Slack（`slack.webhook`，Mattermost、Rocket.Chat 也能用）、Pushover（`pushover.user`），只用替身测过。邮件、ntfy、Gotify 有对着真实服务端（Mailpit、ntfy、Gotify 容器）的 `live_test.go`。
- API 令牌（`internal/kernel/auth/tokens.go`，`PLAN.md` 10）：`Authorization: Bearer jec_…`，只读或读写；管理账号的接口在注册时带 `Metadata: {sessionOnly: true}`，令牌不能用，新加这类接口也要标上；`jec reset-password` 删掉全部令牌。
- 审计日志（模块 `audit`，`PLAN.md` 10）：内核中间件把每个 GET、HEAD 以外的接口调用交给 `module.Registry.Audit` 的记录者（会话检查之后，所以没有会话被拒的不记）；页面 `/audit` 仿签发记录。新增改动类接口时，在 `web/src/pages/AuditPage.vue` 的 `operations` 里给它起个名字（`audit.op.*`），否则显示方法和路径。
- 线上巡检（模块 `monitor`，`PLAN.md` 4.7）：连每个域名的 443 端口核对线上证书，部署 2 分钟后、平时每 6 小时查；当前版本生效 30 分钟后还是旧证书就发 `certificate.served_old`（通知默认打开）；证书事件带了指纹，`certificates.announce` 让证书模块把全部证书报一遍（`certificate.current`）。界面：证书详情的“线上检查”、列表的“需要处理”。
- M3 开始：群机器人通知渠道 `dingtalk`、`feishu`（也接受 Lark）、`wecom`（`PLAN.md` 5.3.4）：发消息的主题（`contracts.Note.Subject`，分发模块生成，标题前加“【JEC】”，邮件标题也用它），机器人可以用关键词 `JEC`；钉钉、飞书支持加签。推送服务 `bark`、`serverchan`（Turbo 版和 Server酱³ 按 SendKey 区分）、`ntfy`、`gotify`；能自建的三种有“允许内网地址”开关、不跟随跳转。`PLAN.md` 5.3.4 列的通知渠道已全部完成。本地目录目标可以在写入后执行命令（`files/command.go`，`mvdan.cc/sh`，带 `JEC_*` 和 certbot 的 `RENEWED_*` 环境变量，去掉 JEC 自己的 `JEC_` 设置）。Docker 容器目标 `internal/modules/docker`（经 Docker API 把文件 tar 进容器再发信号或重启，unix socket 或 `tcp://` socket 代理，API 路径不带版本号）。外部插件 `internal/modules/plugins`（`PLAN.md` 5.2，协议在 `docs/development/plugins.md` 和 `docs/development/plugins.zh-CN.md`，改一份要同步另一份；翻译指南 `docs/development/translating.md`、`docs/development/translating.zh-CN.md` 也一样）：数据目录 `plugins/` 里的程序启动时 `describe`，登记成 `plugins.<插件>.<类型>`；插件自带的文字放在类型的 `texts` 里、键在 `plugins.texts.` 下，前端用 `addMessages` 并入；测试里测试程序自己扮演插件（`PLUGINS_TEST_HELPER`），e2e 在非 Windows 上放一个 shell 脚本插件。插件能做证书来源（`issue` 按 CSR 签发、`own_key` 的 `collect` 带私钥，都能等待；来源的 `challenges` 让 JEC 经证书的验证方式放验证文件和记录）、部署目标、通知渠道和验证方式（DNS 的可以 `records`）。尝试中途失败但有进展时，来源用 `contracts.KeepState` 留下新的状态（Namecheap 也用）。阿里云 CDN / 全站加速目标 `internal/modules/aliyun`（`aliyun.cdn`、`aliyun.dcdn`，OpenAPI 客户端的通用 RPC 调用，只用替身 API 测过）。腾讯云目标 `internal/modules/tencentcloud`（`tencentcloud.ssl`：上传到 SSL 证书服务再“一键更新”旧证书所在的资源，SDK 通用客户端，只用替身 API 测过）。面板目标 `baota`（`baota.site`）、`onepanel`（`onepanel.certificate`，原地替换面板证书的内容）：`http://` 只连内网（`httpx.Options.PrivateOnly`），自签名 HTTPS 按指纹信任（`httpx.Options.Pin`，测试连接报出指纹，`httpx.Fingerprint`）；Proxmox VE 目标 `proxmox`（`proxmox.node`，同样的规则）；Nginx Proxy Manager 目标 `npm`（`npm.certificate`：JEC 维护面板里的一张自定义证书，原地上传后重新保存一个用它的主机让 nginx 重载；对着真实的 NPM 2.16.0 验证过，`live_test.go` 靠 `JEC_TEST_NPM` 等环境变量运行）；群晖 DSM 目标 `synology`（`synology.dsm`：按 acme.sh 的做法登录、列证书、导入替换，可选两步验证密钥；只用替身测过，DSM 不许装在非群晖硬件上）；七牛云 CDN 目标 `qiniu`（`qiniu.cdn`：QBox 签名，按名字找已上传的证书，换证书或开 HTTPS，删旧证书；只用替身测过）；又拍云 CDN 目标 `upyun`（`upyun.cdn`，实验模块：开放接口不能上传证书，所以用账号密码登录控制台的接口，Cookie 会话，上传后换证书或开 HTTPS，删不了旧证书；只用替身测过）；AWS ACM 目标 `aws`（`aws.acm`：SigV4 签名直接调 ACM 的 JSON API，按 ARN 重新导入；对着 moto 验证过，`live_test.go` 靠 `JEC_TEST_ACM` 运行）；Kubernetes 目标 `kubernetes`（`kubernetes.secret`：直接调 REST 写 TLS Secret，集群内用 Pod 的服务账号，测试连接用 SelfSubjectAccessReview；`httpx.Options.Roots` 只信任给的 CA；对着真实的 kube-apiserver 验证过，`live_test.go` 靠 `JEC_TEST_K8S` 等环境变量运行）。手动 DNS 验证 `manual`（`manual.dns`：什么都不加，只给较长的等待；ACME 来源在日志里写出每条 TXT 记录 `acme.log.record`）。阿里云 SDK 的错误一律用 `internal/lib/aliyunapi.ErrorCode` 读：客户端返回的是 `*tea.SDKError`，不是 `*dara.SDKError`（ESA 以前只认后者，已修）。
- M3 还完成了：钩子 `internal/modules/hooks`（通知渠道类型 `hooks.script`，`PLAN.md` 5.2 的“钩子的实现”：内置 shell 在数据目录的 `hooks/` 里运行脚本，消息在 `JEC_*` 环境变量和标准输入的 JSON 里，退出码 75 稍后重试）；运行脚本的共用库 `internal/lib/shellx`，本地目录部署的命令也改用它。
- M3 还完成了：Webhook 的自定义请求（`webhook.notify`、`webhook.deploy` 的高级设置，`PLAN.md` 5.3.3）：请求体可以写成 Go 模板（字段名同默认 JSON，`json` 函数），可选 POST/PUT 和 Content-Type。
- M3 还完成了：YAML 导入导出（内核 `internal/kernel/configfile`，接口 `configfileapi`，`PLAN.md` 11）：模块用 `r.Config` 登记自己那部分（证书模块的 `renewal`、`certificates`），导入在一个事务里先预览后执行，服务商按种类和名称对应、证书按域名对应，只增改不删；设置页“数据和备份”的“配置文件”。模块的导入只能经给它的事务写数据库。
- M3 还完成了：阿里云数字证书管理服务 `aliyun.cas` 和传统型负载均衡 `aliyun.clb`（在 `internal/modules/aliyun`，通用 RPC 调用 `rpc.do`，按名字 `jec-<ID>-<指纹>` 找上次传的，删除旧证书；只用替身测过）。参考实现：certimate 的源码可以浅克隆到草稿目录里查接口用法（只读，不运行）。
- M3 还完成了：华为云 CDN `internal/modules/huaweicloud`（`huaweicloud.cdn`，用官方 SDK，先读出域名的 HTTPS 设置再连同 HTTP/2、TLS、OCSP 一起写回；只用替身测过）。引入新的 SDK 前先实测体积（用 `-tags no_<模块>` 对比 `-trimpath -ldflags "-s -w"` 的构建大小）。SDK 的客户端不要存进会被转成接口的结构体：程序里有用反射调方法的代码（text/template）时，链接器会保留这个类型的全部方法，华为云 CDN 的客户端因此多出 1 MiB；只存要用的方法值（`client.ShowDomainFullConfig`）。和旧提交比体积时，旧提交的工作区要放进同一份 `web/dist`，否则差出整个界面（约 1.2 MiB）。
- M3 还完成了：Cloudflare 自定义证书 `internal/modules/cloudflare`（`cloudflare.custom`，第一次添加、以后原地 PATCH 同一张，按主机和到期时间找中断前添加的；只用替身测过）。
- M3 还完成了：阿里云 OSS 自定义域名 `aliyun.oss`（先传到数字证书管理服务，再 `PutCname`；OSS 自己的 V4 签名在 `aliyun/osssign.go`，规范请求按文档示例的哈希核对过；只用替身测过）。
- M3 还完成了：DNS 记录管理层（`PLAN.md` 5.3.2 ②）：`contracts.RecordSolver`（`AddRecord` 返回的标识由来源存进签发状态；手动添加的标识为空，记录留给用户删），验证类型标 `records`、来源的 `challenges` 写 `record`，合不合用看 `providers.Type.Takes`，类型列表的“任意记录”分组由接口按 `records` 加上；`internal/lib/dnsrecord`（腾讯云 DNSPod、阿里云 DNS、Cloudflare、华为云 DNS，用 lego 带的 SDK、沿用 lego 的变量名，lego 模块里这四家的验证方式同时是 `RecordSolver`）；`internal/lib/dnscheck`（直接问权威 DNS，只依赖 DNS 库，来源用它等记录生效）。Namecheap 来源能做 DNS 验证（记录名用 `dnscheck.RecordName` 转成完整域名，权威 DNS 查得到才报告，签好后删除），也能配手动添加。只用替身测过。
- M3 还完成了：取回阿里云、腾讯云签发的证书（`PLAN.md` 5.3.1）：自带私钥的来源 `aliyun.fetch`（`ListUserCertificateOrder` + `GetUserCertificateDetail`）、`tencentcloud.fetch`（`DescribeCertificates` + `DownloadCertificate` 的 ZIP），上传的证书不算；覆盖判断共用 `cryptox.Covers`。只用替身测过。
- M3 还完成了：向阿里云申请证书 `aliyun.apply`（`PLAN.md` 5.3.1：资源包接口，默认个人测试证书，JEC 自己的私钥和 CSR，DNS 或文件验证，CA 自己来查）；`contracts.Restart`：来源标出订单作废时，证书模块失败或重试都不留私钥和状态（`PLAN.md` 4.3）。阿里云 ALB 目标 `aliyun.alb`（默认证书或扩展证书，改之前等监听配置完），只用替身测过。Caddy 目标 `caddy.admin`（管理接口，`load_pem` 里按 `@id` 原地替换；对着真实的 Caddy 2.11.7 验证过，`live_test.go` 靠 `JEC_TEST_CADDY` 等环境变量运行）。火山引擎 CDN 目标 `volcengine.cdn`（签名用 lego 带的火山引擎 SDK 的 `base.Credentials.Sign`，请求自己发）、百度智能云 CDN 目标 `baiducloud.cdn`（自己实现 bce-auth-v1 签名，测试里和官方 SDK 的签名器核对），都只用替身测过。
- M3 的验收测试：`cmd/jec/modules_test.go` 的 `TestEverySwitchOff` 带着全部真实模块启动，在运行中逐个停用、再打开每个可停用的模块，检查其余功能照常；新模块自动覆盖。
- M3 还完成了：腾讯云免费证书 `tencentcloud.free`（`DNS_AUTO`，腾讯云生成私钥，自带私钥的来源；`CollectRequest.State` 让这类来源也能在等待之间保留状态）。只用替身测过。
- 代理：`httpx` 的客户端不用环境变量的代理；连境外服务的类型加 `httpx.ProxyField()`，客户端用 `httpx.ClientFor(opts, in.Config)`（经代理时去掉 PublicOnly），新加这类类型照办。解析服务器 `JEC_DNS_RESOLVERS`（`dnscheck.UseResolvers`，`jec serve` 启动时设，子进程经环境变量继承）。
- Cloudflare 边缘应答（验证类型 `cloudflare.edge`，在 `cloudflare` 模块里，`PLAN.md` 5.3.2）：Worker（`worker.js`）+ Workers KV + 路由（按主机，SaaS 自定义主机名退回 `*/.well-known/*`）+ 区域最前面的跳过规则（`ref: jec_validation`），网站服务器不用改动；Cloudflare API 客户端在 `cloudflare/api.go`（API 令牌，或邮箱加 Global API Key：`X-Auth-Email`、`X-Auth-Key`；“Cloudflare 凭据”同样两种都行）。用户的 sukimoe.com 是 0page.de 账号里 0page.de 区域（Business）上 Cloudflare for SaaS 的自定义主机名，区域有对境外 IP 的质询规则，所以要靠 JEC 的跳过规则。Namecheap 来源在等证书时先经证书的验证方式放好 `installed.txt`（Namecheap 找到它才给证书），来源表单顶部有分步引导。只用替身测过。
- 反向代理（`docs/operations/reverse-proxy.md`）：`deploy/docker-compose.yml` 只在 `127.0.0.1:8080` 上发布，默认 `JEC_TRUSTED_PROXIES: 172.16.0.0/12`（宿主机上的反向代理经容器网络的网关连进来）；页面里 Caddy、nginx 的配置都实测过，前置 CDN 时由反向代理认 CDN 的头（`deploy/reverse-proxy/cdn-ranges.sh` 写地址段），再以只含客户端的 `X-Forwarded-For` 交给 JEC。`/.well-known/` 的分发器在模块没命中时只去掉该模块设的头，服务器的安全响应头保留。
- 写文件的部署目标（本地目录、SSH、Docker）共用 `internal/lib/certfiles`：文件名可改（`{domain}`）、可选合并文件和 PKCS #12（`cryptox.EncodePFX`）、权限；属主各自处理。
- 续期默认值多了“失败后重试”（`certs_settings` 的 `retries`，默认 7，0 到 20，签发和部署都按它；配置文件 `renewal.retries`）。
- 测试版 `v0.1.0-alpha.5`（凭据、时区、失败后重试、证书列表菜单、部署文件可配置、代理、解析服务器等）、`v0.1.0-alpha.6`（systemd 服务、32 位 ARM、吊销、无障碍检查等）、`v0.1.0-alpha.7`（界面自带字体、Go 依赖更新）、`v0.1.0-alpha.8`（紧凑的设置页和服务商表单、中文标语、`/.well-known/` 应答保留安全响应头）、`v0.1.0-alpha.9`（Cloudflare 边缘应答、Namecheap 先放标记文件和分步引导）、`v0.1.0-alpha.10`（Cloudflare 也能用邮箱加 Global API Key）于 2026-10-19 发布。`latest` 还是 0.0.1，所以 `deploy/docker-compose.yml` 在 1.0.0 之前用 `beta`，发正式版时改回 `latest`。
- 文档站 https://getjec.dev/ （`docs/site`，VitePress；`docs.yml` 从 main 发布到 GitHub Pages，Pages 的来源是 GitHub Actions；自定义域名 getjec.dev，DNS 由用户自己在 Cloudflare 上设置；站点的路径由 `actions/configure-pages` 的 `base_path` 经 `DOCS_BASE` 传给 VitePress，所以有没有自定义域名都能用，换了域名要重新跑一次 docs）：页面在 `docs/` 的 `guide`（开始）、`usage`（使用）、`operations`（运维）、`reference`（参考）、`development`（开发）五个目录，每页一份英文 `x.md` 和一份中文 `x.zh-CN.md`，改一份要同步另一份；`docs/site/pages.mjs` 的 `sections` 定侧栏顺序（目录里多出或缺少页面时构建失败），侧栏文字取每页的一级标题。首页是 `docs/site/home.md`、`home.zh-CN.md`（hero、功能卡片），下面的总览截图由 `.vitepress/theme/HomePreview.vue` 显示（`public/preview/`，界面大改后重拍）；动画在 `theme/jec.css` 和 `Layout.vue`，都照顾 `prefers-reduced-motion`。站内链接要能对上（VitePress 遇到失效链接会构建失败）；以数字开头的标题在 GitHub 和 VitePress 上锚点不同，不要链接到这种锚点。README 只放概要，详细说明写进文档站的页面。首页的锁有苦力怕彩蛋（`.vitepress/theme/HeroLock.vue`：连点 8 下裂开、变成苦力怕的脸、膨胀爆炸后复原，然后像 MC 完成挑战一样弹出“挑战已完成！”提示框、聊天栏消息，奖励 100 点经验（经验条按 MC 的公式升级，每 5 级响一次）；照顾“减少动态效果”），404 页也有一只（`NotFound.vue`）；像素图是自己画的（`creeper.ts`），音效用 MC 原版（用户的决定），由 `docs/site/sounds.mjs` 在每次构建时从 Mojang 的资源服务器按 SHA-1 下载到 `public/sounds`，不进仓库。
- 时区（设置页“外观”，`internal/kernel/zone`，存在 `kernel_meta`）：整个面板共用，默认跟随服务器；`PUT /api/v1/system/time-zone`，清单接口带 `timeZone`；模块用 `r.Zone`（每次现取），续期的每天检查时间和通知里的时间按它；前端 `web/src/zone.ts`（`useIntl` 的日期、按天分组用 `dayKey`、导出 CSV 用 `stamp`，不要再用 `toDateString` 或 `getHours` 之类按浏览器时区的写法）；配置文件里是 `time_zone`。
- M4 开始：PWA（`web/public/manifest.webmanifest`，图标由 `go run ./tools/icons` 从 favicon 画出，`make gen` 也会跑，测试核对图标是最新的）。
- M4 还完成了：OIDC 单点登录（模块 `oidc`，授权码 + PKCE，go-jose 校验 ID token，名单里的人以唯一的账号登录；对着真实的 Dex v2.44.0 验证过，`live_test.go` 靠 `JEC_TEST_OIDC` 等环境变量运行）；内核给模块开了 `module.Registry.StartSession`（为账号建会话），GET 操作在 `Metadata` 里标 `api.Audited` 也会进审计日志。
- systemd 服务（`PLAN.md` 8.4）：`jec service install|uninstall|unit`（`cmd/jec/service.go`，单元模板 `cmd/jec/jec.service.tmpl`；`deploy/systemd/jec.service` 是默认路径下的同一份，改了模板要 `go run ./cmd/jec service unit > deploy/systemd/jec.service`，测试核对）。沙箱 `ProtectSystem=full` 等；写入后执行的命令继承沙箱，所以不加 `MemoryDenyWriteExecute`、`SystemCallFilter`。写入遇到只读文件系统时本地目录类目标报 `files.error.read_only`。
- 32 位 ARM（`PLAN.md` 9.1、9.2）：发布多了 `linux_armv7` 的程序和 `linux/arm/v7` 镜像（`stage-image-context.sh`、`release-sizes.sh` 按 Docker 的平台名带上 `/v7`）；CI 的 `test (linux/arm/v7, emulated)` 在 QEMU 里跑全部测试。本机没有注册 binfmt_misc，只能 `go test -exec qemu-arm`（在装了 qemu-user 的临时容器里），这时会自己启动子进程的 lego、插件测试报 exec format error，属正常。
- 无障碍检查（`PLAN.md` 7.4）：伪语言测试每一步都用 axe（`@axe-core/playwright`）查整页，有违规就失败；列表项里放链接（链接本身不能是 `listitem`），正文里的链接加下划线。
- 吊销证书（`PLAN.md` 4.6）：证书详情的历史版本里可以吊销较早的版本（`certs/revoke.go`，来源实现 `contracts.Revoker`、类型标 `Revocable`，目前是 ACME）；当前版本不能直接吊销，要先签发新版本。e2e 里没有能签发的 CA，吊销界面靠 `page.route` 改写详情接口显示编出来的版本。
- 升级测试：`cmd/jec/upgrade_test.go` 在 0.1.0-alpha.5 的数据（`cmd/jec/testdata/alpha5`：该版本镜像经 API 建好再导出的备份和它的主密钥，密钥都是编的）上启动，检查迁移后各类数据照常、密钥能解开。改了迁移或数据格式要让它继续通过；以后发版可以照此加一份新的。
- 测试里不要访问界面（`/`）：本机的 `web/dist` 是构建过的，CI 里只有 `.gitkeep`，本机过了 CI 也会失败。
- 凭据（`PLAN.md` 6.1，设计稿 `Credentials`）：前后端都已完成（界面：`/credentials`、`CredentialDialog.vue`，服务商表单里的“凭据”选择在 `ProviderForm.vue`，`providers/fields.ts` 的 `credentialChoice`）。凭据是一种“凭据种类”（`providers.CredentialKind`）的一组值，多个实例共用，存在 `kernel_credentials`；类型用 `Type.Credential` 说明哪些字段可以由凭据填，打开实例时由注册表填好。几个模块共用的种类放在 `internal/lib/credentials`（各家云的访问密钥、SSH 私钥），新加的云服务类型要声明它用的种类（`credentials.Use`），lego 的映射在 `lego/catalog.go` 的 `credentialOf`。接口在 `/api/v1/credentials`，添加和修改实例可以带 `credentialId` 或 `newCredential`；配置文件有 `credentials` 一节。
- DNS 服务商的测试连接：`dnsrecord` 的四家（腾讯云 DNSPod、阿里云 DNS、Cloudflare、华为云 DNS）列出账号下的域名（`API.Domains`），lego 模块里这四家的类型标 `testable`。
- 服务商卡片的状态行（设计稿 `Providers`）：实例记下最近一次测试（`kernel_providers` 的 `tested_at`、`test_result`，接口里是 `lastTest`；改了设置或凭据就作废），部署目标还看最近一次部署，取较新的显示。
- 按设计稿补了一批界面：总览选中证书的主操作（重试、查看进度、立即续期或检查）、“部署到”、自动续期的证书合成一组、手机上的最近动态；证书列表的来源筛选、“部署到”列和“导入已有证书”（添加页 `?source=upload`）；证书详情逐个目标重新部署；手机上从“更多”进入的页面有返回箭头（`PageHeader` 的 `phoneBack`，`web/src/shell.ts` 的 `fromMore`）。
- 设置页按设计稿补上了主题（`web/src/theme.ts`，跟随系统、浅色、深色，存在浏览器里）和“检查更新”（`GET /api/v1/system/release`，点了才问 GitHub）。
- 设置页按用户要求改成紧凑布局（和设计稿 `Settings` 不同，以此为准）：卡片按 `columns-[24rem]` 分列、各列从上往下排满，不折叠、不分页；顺序是外观、续期默认值、数据和备份、账号和安全、关于，宽屏两列时左右差不多高；只读的版本、构建、数据目录、面板地址、监听地址、信任的反向代理、HSTS 都在“关于”里，一项一行，环境变量名作为数据显示在标签下。
- 下一步：用真实账号验证阿里云 ESA 部署、ESA 边缘应答和 Namecheap 模块（M1/M2 验收，`PLAN.md` 5.4、5.6 列了要确认的点，包括 Namecheap 给不给 DNS 验证；给用户的操作清单在 `docs/development/testing.zh-CN.md`，和英文版同步）；M4（`PLAN.md` 11）。

## 工程约定

- 代码、注释、脚本、部署文件都用英文；中文只出现在语言文件和文档里（i18n 门禁会扫描）。命令行输出和运维日志按 `PLAN.md` 7.3 算开发者日志，用英文。
- Go：`go.mod` 只写最低版本（1.26），CI 和发布用最新稳定版。GitHub Actions 一律钉到 commit SHA 并在注释里写版本号，由 Dependabot 每周更新（包括 `.github/actions/` 下的组合动作；Node 大版本除外，镜像和 CI 一起手动升到下一个长期支持版）。
- CI 和发布里的 Go 一律用 `.github/actions/setup-go` 安装：每个任务有自己的编译缓存，只有 main 上的运行写缓存，发布和发布演练共用一份（`PLAN.md` 9.1）。新加的任务照此办理，不要用 setup-go 自带的缓存。
- 容器默认以 65532:65532 运行，不在启动时切换用户；挂载的数据目录要先 `chown`，启动检查会给出明确报错。
- ghcr.io 上的镜像包从 v0.0.1 起就是公开的（发布后实测匿名能拉取），不需要手动改可见性。
