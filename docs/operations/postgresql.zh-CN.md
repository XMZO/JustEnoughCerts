# PostgreSQL

[English](postgresql.md)

JEC 默认把数据保存在数据目录中的 SQLite 里，无需其他服务。也可以改用 PostgreSQL，适合已有 PostgreSQL 服务器、并希望统一备份的部署。支持 PostgreSQL 14 或更高版本，测试使用 PostgreSQL 18。

## 配置

将 `JEC_DATABASE_URL`（或 `config.yaml` 中的 `database_url`）设为连接地址：

```
postgres://jec:password@db.example.com:5432/jec?sslmode=require
```

- 连接地址只从环境变量或配置文件读取，不接受命令行参数（其他进程可以看到命令行）。
- JEC 在搜索路径的第一个 schema（默认为 `public`）中建表，每次启动时自动升级表结构。该数据库应由 JEC 独用。
- 仍需要数据目录，用于保存主密钥、配置文件、插件与脚本。

带 PostgreSQL 服务的 Compose 文件片段：

```yaml
services:
  jec:
    image: ghcr.io/xmzo/justenoughcerts:beta
    environment:
      JEC_DATABASE_URL: postgres://jec:change-me@db:5432/jec?sslmode=disable
    depends_on: [db]
  db:
    image: postgres:18
    environment:
      POSTGRES_USER: jec
      POSTGRES_PASSWORD: change-me
      POSTGRES_DB: jec
    volumes:
      - ./postgres:/var/lib/postgresql
```

## 从 SQLite 迁移

1. 停止 JEC。
2. 设置 `JEC_DATABASE_URL` 后运行 `jec import-sqlite`；在容器中为 `docker compose run --rm jec import-sqlite`。该命令先将双方升级到当前版本，再把数据目录中的 `jec.db`（或 `-from` 指定的文件）复制到 PostgreSQL。PostgreSQL 中已有账号时拒绝复制，除非加上 `-replace`。
3. 设置 `JEC_DATABASE_URL` 后启动 JEC。须保留主密钥：复制过去的敏感数据仍以它加密。

`jec.db` 保持不变，确认 JEC 运行正常后可以删除。

## 备份

- 设置页的**下载备份**与使用 SQLite 时相同：生成一份一致的 SQLite 文件，两种数据库均可用它恢复。
- 设置页的**恢复**接受这种文件：下次启动时，JEC 先将 PostgreSQL 中的现有数据另存为数据目录中的 `jec.db.before-restore-<时间>`，再以备份中的数据替换。
- 也可以用 PostgreSQL 自带的工具（如 `pg_dump`）备份；恢复时须先恢复到空数据库，再启动 JEC。
