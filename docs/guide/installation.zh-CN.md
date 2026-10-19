# 安装与升级

[English](installation.md)

JEC 以容器镜像与静态二进制文件两种形式发布。推荐使用 Docker Compose 部署。

## Docker Compose

```sh
mkdir jec && cd jec
curl -fsSLO https://raw.githubusercontent.com/XMZO/JustEnoughCerts/main/deploy/docker-compose.yml
mkdir -p data && sudo chown 65532:65532 data
docker compose up -d
```

- **镜像**：`ghcr.io/xmzo/justenoughcerts`，支持 linux/amd64 与 linux/arm64。标签 `latest` 为最新正式版，`beta` 为最新测试版，也可指定版本号（如 `0.1.0-alpha.5`）。
- **运行用户**：容器默认以 65532:65532 运行，`./data` 须对该用户可写。如需以现有目录的属主运行，请在 `docker-compose.yml` 中设置 `user:`；启动时若目录不可写，JEC 会给出明确的错误提示。
- **端口**：默认监听 8080，可通过 `JEC_LISTEN` 修改，参见[配置参考](../operations/configuration.zh-CN.md)。
- **时区**：`docker-compose.yml` 默认设置 `TZ: Asia/Shanghai`；面板的时区也可在设置页单独选择。

### 可选挂载

`docker-compose.yml` 中以注释给出了以下挂载，按需启用：

| 挂载 | 用途 |
|---|---|
| `/etc/letsencrypt:/etc/letsencrypt:ro` | 证书来源“本机证书目录”读取 certbot（或 acme.sh）续期的证书。私钥通常属于 root，此时 JEC 须以 `user: "0:0"` 运行 |
| `/var/run/docker.sock` | 部署目标“Docker 容器”向其他容器写入证书并使其重载。该套接字等同于 Docker 的完全控制权，建议改用仅放开 `CONTAINERS=1` 与 `POST=1` 的套接字代理 |

## 二进制文件

每个版本在[发布页](https://github.com/XMZO/JustEnoughCerts/releases)提供 Linux 与 macOS 的静态二进制文件（amd64 与 arm64）。不提供 Windows 版本，Windows 用户请使用 Docker 或 WSL。

```sh
tar -xzf jec_<版本>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

`jec <命令> -h` 列出命令的全部参数。其他命令包括 `jec version`（版本信息）、`jec healthcheck`（健康检查）与 `jec reset-password`（重置密码）。

## 升级

- **Docker Compose**：拉取新镜像并重新创建容器。数据库迁移在启动时自动完成。

  ```sh
  docker compose pull
  docker compose up -d
  ```

- **二进制文件**：停止 JEC，替换二进制文件后重新启动。

升级前建议先备份，参见[数据与备份](../operations/backup.zh-CN.md)。发布文件的签名校验方法见[校验发布文件](../operations/releases.zh-CN.md)。
