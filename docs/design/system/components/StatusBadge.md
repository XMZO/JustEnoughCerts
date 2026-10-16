# StatusBadge

状态标签说明一张证书、一次签发或一个部署目标当前处于什么状态，永远同时带图标和文字。

- **调用方提供**：状态值（由后端返回）和对应的翻译 key；组件按状态选颜色和图标，调用方不自己挑颜色。
- **固定映射**（全站一致，不要另起名字）：

| 状态 | 类名 | 图标（Lucide） | 中文 | English |
|---|---|---|---|---|
| 正常 | `jec-badge--ok` | `circle-check` | 正常 | Healthy |
| 进入续期窗口 | `jec-badge--warn` | `triangle-alert` | 即将到期 | Expiring soon |
| 已过期 | `jec-badge--danger` | `circle-x` | 已过期 | Expired |
| 签发或部署失败 | `jec-badge--danger` | `octagon-x` | 失败 | Failed |
| 进行中 | `jec-badge--info` | `loader-circle`（旋转） | 签发中 / 部署中 | Issuing / Deploying |
| 未部署、已停用 | 默认 | `circle-dashed` | 未部署 | Not deployed |

- “即将到期”的阈值跟随证书的续期策略（默认剩余 ≤ 30 天），和 LifeBar 的黄色保持一致。
- 旋转动画是全站唯一的常驻动效，用户开启“减少动态效果”时停止。
