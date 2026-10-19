# 参与开发

[English](contributing.md)

## 环境要求

Go 1.26 或更高版本、Node.js 24、pnpm 10（`corepack enable`）。

## 常用命令

```sh
make web         # 构建前端至 web/dist，Go 编译时将其嵌入程序
make run         # 以 ./data 为数据目录运行服务
make dev         # 前端热更新开发服务器（:5173），/api 转发至 make run
make test        # Go 测试（含竞态检测）与前端测试
make lint        # golangci-lint、类型检查与 ESLint
make i18n-check  # i18n 检查
make gen         # 修改界面文字或 API 后，重新生成 key 常量、类型与字体
make new-module id=acme category=source  # 生成新模块
```

各目标均为单条 `go`、`pnpm` 或 `docker` 命令，Windows 上可直接运行对应命令；`make help` 列出全部目标。

## 规则

以下规则由自动检查强制执行：

- 界面文字仅写在 `i18n/locales/*.json`（每种语言一个文件），代码通过生成的常量引用；修改语言文件后须运行 `go run ./tools/i18n fix`，参见[翻译指南](translating.zh-CN.md)。
- 代码、注释与脚本使用英文。
- 模块之间仅通过内核通信：模块不得引用其他模块，内核不得引用模块。

## 文档

- 架构、模块接口与路线图见 [PLAN.md](../../PLAN.md)，设计系统与页面设计稿见 [docs/design](../design)。
- 本文档站的页面位于 `docs/` 下的 `guide`、`usage`、`operations`、`reference` 与 `development` 目录，每页一份英文（`*.md`）与一份中文（`*.zh-CN.md`）。`docs/site/pages.mjs` 负责组织页面，VitePress 负责构建；首页与导航在 `docs/site` 中定义。
- 本地预览：执行 `pnpm -C docs/site install` 后运行 `pnpm -C docs/site dev`，然后访问 `http://localhost:5173/JustEnoughCerts/`。main 分支更新后，docs 工作流自动发布文档站。
