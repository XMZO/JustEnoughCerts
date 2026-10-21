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

- **镜像**：`ghcr.io/xmzo/justenoughcerts`，支持 linux/amd64、linux/arm64，以及自 0.1.0-alpha.6 起支持 linux/arm/v7（32 位 ARM，如运行 32 位系统的树莓派）。标签 `latest` 为最新正式版，`beta` 为最新测试版，也可指定版本号（如 `0.1.0-alpha.6`）。正式版发布前，`docker-compose.yml` 使用 `beta`。
- **运行用户**：容器默认以 65532:65532 运行，`./data` 须对该用户可写。如需以现有目录的属主运行，请在 `docker-compose.yml` 中设置 `user:`；启动时若目录不可写，JEC 会给出明确的错误提示。
- **端口**：`docker-compose.yml` 仅在 `127.0.0.1:8080` 上发布 JEC，只能从服务器本机访问，由反向代理对外提供，参见[反向代理与 CDN](../operations/reverse-proxy.zh-CN.md)。容器内默认监听 8080，可通过 `JEC_LISTEN` 修改，参见[配置参考](../operations/configuration.zh-CN.md)。
- **时区**：`docker-compose.yml` 默认设置 `TZ: Asia/Shanghai`；面板的时区也可在设置页单独选择。

### 可选挂载

`docker-compose.yml` 中以注释给出了以下挂载，按需启用：

| 挂载 | 用途 |
|---|---|
| `/etc/letsencrypt:/etc/letsencrypt:ro` | 证书来源“本机证书目录”读取 certbot（或 acme.sh）续期的证书。私钥通常属于 root：按[从 certbot 或 acme.sh 迁移](migration.zh-CN.md#保留原客户端)改为组 65532 可读，或以 `user: "0:0"` 运行 JEC |
| `/var/run/docker.sock` | 部署目标“Docker 容器”向其他容器写入证书并使其重载。该套接字等同于 Docker 的完全控制权，建议改用仅放开 `CONTAINERS=1` 与 `POST=1` 的套接字代理 |

## 二进制文件

每个版本在[发布页](https://github.com/XMZO/JustEnoughCerts/releases)提供 Linux 与 macOS 的静态二进制文件（amd64 与 arm64），以及 Linux 32 位 ARM 版本（`linux_armv7`，自 0.1.0-alpha.6 起）。不提供 Windows 版本，Windows 用户请使用 Docker 或 WSL。

```sh
tar -xzf jec_<版本>_linux_amd64.tar.gz
./jec serve -data-dir /var/lib/jec
```

`jec <命令> -h` 列出命令的全部参数。其他命令包括 `jec version`（版本信息）、`jec healthcheck`（健康检查）、`jec reset-password`（重置密码）与 `jec service`（见下文）。

### systemd 服务

在使用 systemd 的 Linux 上，可用 `jec service install` 将 JEC 安装为服务：

```sh
tar -xzf jec_<版本>_linux_amd64.tar.gz
sudo ./jec service install
journalctl -u jec | grep setup_token
```

该命令将二进制文件复制到 `/usr/local/bin/jec`，添加系统用户 `jec`，写入 `/etc/systemd/system/jec.service`，然后启用并启动服务，数据目录为 `/var/lib/jec`。最后一条命令输出初始化令牌。`jec service unit` 只输出单元文件而不做任何安装；仓库中的 [deploy/systemd/jec.service](../../deploy/systemd/jec.service) 是同一单元文件，可用于手动安装。

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-bin` | `/usr/local/bin/jec` | 二进制文件的复制位置，服务从此处运行 |
| `-data-dir` | `/var/lib/jec` | 数据目录 |
| `-user` | `jec` | 服务的运行用户，不存在时自动添加；设为 `root` 可写入任意位置 |
| `-start` | `true` | `-start=false` 只启用服务而不启动 |

设置写在 `/var/lib/jec/config.yaml` 中，参见[配置参考](../operations/configuration.zh-CN.md)。对服务的其他修改（如环境变量）请通过 `sudo systemctl edit jec` 进行，这些修改与单元文件分开保存，重新安装时不会丢失。`sudo jec service uninstall` 停止服务并删除单元文件，保留数据、用户与二进制文件。

服务运行在沙箱中：不能写入 `/usr`、`/boot` 与 `/etc`，其他位置按文件权限决定 `jec` 用户能否写入。需要访问数据目录以外位置的功能，须通过 `sudo systemctl edit jec` 在 `[Service]` 下授予访问权限：

| 功能 | 所需权限 |
|---|---|
| 位于 `/etc` 下的本地目录或网站目录，如 `/etc/nginx/ssl` | `ReadWritePaths=/etc/nginx/ssl`，并授予 `jec` 对该目录的写权限 |
| 位于其他位置的本地目录或网站目录 | 授予 `jec` 对该目录的写权限 |
| Docker 容器部署目标 | `SupplementaryGroups=docker` |
| 本机证书目录（certbot、acme.sh） | 私钥通常仅 root 可读：按[从 certbot 或 acme.sh 迁移](migration.zh-CN.md#保留原客户端)把属组改为 `jec` 并加组读权限，或以 `-user root` 安装 |
| 写入后重载服务的命令，如 `systemctl reload nginx` | 如下的 polkit 规则，或以 `-user root` 安装 |

允许 `jec` 重载 nginx 的 polkit 规则，写入 `/etc/polkit-1/rules.d/50-jec.rules`：

```js
polkit.addRule(function (action, subject) {
  if (action.id == "org.freedesktop.systemd1.manage-units" &&
      action.lookup("unit") == "nginx.service" &&
      action.lookup("verb") == "reload" &&
      subject.user == "jec") {
    return polkit.Result.YES;
  }
});
```

## 升级

- **Docker Compose**：拉取新镜像并重新创建容器。数据库迁移在启动时自动完成。

  ```sh
  docker compose pull
  docker compose up -d
  ```

- **systemd 服务**：使用新版本的二进制文件再次执行 `sudo ./jec service install`，即可替换二进制文件并重启服务。
- **其他二进制安装**：停止 JEC，替换二进制文件后重新启动。

设置页“关于”中的“检查更新”会询问 GitHub 是否有新版本；JEC 默认不会自行联网检查。开启“自动检查更新”后，JEC 每天检查一次，有新版本时经通知渠道提醒（每个版本一次）。

迁移 SQLite 数据库前，JEC 会先将其复制到 `backups/`，以便退回原版本，参见[升级前的备份](../operations/backup.zh-CN.md#升级前的备份)。发布文件的签名校验方法见[校验发布文件](../operations/releases.zh-CN.md)。
