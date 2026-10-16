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

- 后端：Go，chi + huma（OpenAPI），SQLite（驱动在 M0 实测 `modernc.org/sqlite` 和 `ncruces/go-sqlite3` 后定），goose 迁移，go-acme/lego v5，log/slog，mvdan.cc/sh 跑钩子脚本。
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

- 已完成：`PLAN.md`；设计系统和全部页面设计稿（`docs/design/`）。
- 下一步：按 `PLAN.md` 第 13 节做 M0 骨架（仓库结构、CI、发布流水线、`scratch` 镜像和体积检查、模块注册表、i18n 模块和门禁、中英文登录页和初始化向导、SQLite 驱动实测）。
