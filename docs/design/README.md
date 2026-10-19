# 界面设计

这里是 JEC 网页界面的设计稿和设计系统，实现前端时以这里为准。

在线版本（只有仓库主人能打开）：

- 设计系统：https://claude.ai/artifact/3BNJpFxgek9tD4qht5DmL9
- 页面设计稿：https://claude.ai/artifact/R6rRYGqBX22BzGfT74Rs4k

## system/：设计系统

| 文件 | 内容 |
|---|---|
| `README.md` | 设计规范：文案语气、颜色用法、耐久条和格子、文字、间距、断点、图标、动效、国际化、无障碍 |
| `tokens.json` | 颜色（浅色和深色两套，另有可选的主题色 `color.accents`）、可选的风格（`styles`，方块风）、字体、字号、间距、圆角、阴影、布局尺寸，每个 token 带用法说明 |
| `tokens.css` | 由 `tokens.json` 生成的 CSS 变量，深色主题用 `[data-theme="dark"]`，主题色用 `[data-accent]`，方块风用 `[data-style="blocks"]` |
| `bundle.css` | 全部组件的参考样式，类名前缀 `jec-` |
| `components/*.md` | 每个组件的用法：Button、StatusBadge、LifeBar、CertSlot、CertRow（含手机端 CertItem 和两列小格）、Field、Switch、ChoiceCard、Segmented、LogView、Dialog |
| `jec-mark.svg` | 像素锁标志 |

实现 Vue 组件时，把 token 接进 Tailwind 主题，组件按 `bundle.css` 的样式和 `components/*.md` 的规则做；类名不必照搬。

## boards/：页面设计稿

每个 `.dc.html` 是一个画板，`canvas.json` 是画布布局。文件里的 `{{...}}`、`<sc-for>`、`<sc-if>` 是设计工具的模板语法，底部 `<script type="text/x-dc">` 里是演示数据和交互逻辑。可以当作结构和样式参考，不能直接在浏览器里打开。

| 桌面端 | 手机端 | 页面 |
|---|---|---|
| `Main` | `OverviewMobile` | 总览：物品栏格子、选中证书摘要、需要处理、未来 90 天到期、最近动态 |
| `Certs` | `CertsMobile` | 证书列表（手机端：需要处理整行 + 正常证书两列小格） |
| `CertDetail` | `CertDetailMobile` | 证书详情（`cert` 属性切换正常 / 失败两种状态） |
| `AddCert` | `AddCertMobile` | 添加证书：一整页表单，桌面端右侧显示签发计划 |
| `Issue` | `IssueMobile` | 签发进度：步骤和实时日志 |
| `History` | `HistoryMobile` | 签发记录：按日期分组，用时间范围筛选代替分页 |
| `Sources`、`Dns`、`Targets`、`Notify`、`Credentials` | 同名加 `Mobile` | 配置页，都引用同一个模板 `Providers` / `ProvidersMobile`，用 `kind` 属性切换 |
| `TargetsAdd` | — | 添加部署目标对话框：左边搜索模块，右边是根据模块配置说明生成的表单 |
| `Modules` | `ModulesMobile` | 模块：按类别用开关启用或停用 |
| `Settings` | `SettingsMobile` | 设置 |
| `Login` | — | 登录 |
| — | `MoreMobile` | 手机端“更多”标签 |
| `MainEn` | — | 英文版总览，用来检查文字变长后的布局 |
