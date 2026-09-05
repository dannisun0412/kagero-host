# Kagero Host

Kagero 的 macOS 电脑端伴侣：安装、生成二维码，在 App「添加服务器 → 扫码连接」配对。无需系统 VPN、系统 SSH 服务、账号密码或管理员权限。基于 [Tailcat](https://github.com/tailscale/tailcat)，为独立项目，不代表 Tailscale 官方产品或背书。

## Homebrew 安装

已发布 [0.1.0 测试版](https://github.com/dannisun0412/kagero-host/releases/tag/v0.1.0)，安装源为 [dannisun0412/homebrew-tap](https://github.com/dannisun0412/homebrew-tap)。在终端运行：

```sh
brew install dannisun0412/tap/kagero-host
kagero-host setup
```

需要 macOS 13 Ventura 或更新版本，以及支持扫码配对的 Kagero App。首次运行请在电脑已登录的桌面终端中执行。安装不会自行启动远程访问；执行 `setup` 后启动后台服务并显示二维码，之后登录电脑时自动启动。

0.1.0 为首个测试版本，提供 Apple Silicon 和 Intel 构建，暂未进行 Developer ID 签名和 Apple 公证。Apple Silicon 已完成本地安装与 App 模拟器扫码配对验收；Intel 和较旧 macOS 的完整配对仍需对应设备验证。

## 本地构建与安装

需要 macOS、Xcode Command Line Tools、Go 1.27.1、Node.js / npm。

```sh
cd kagero-host
python3 scripts/package.py
npm install -g ./dist/kagero-host-0.1.0.tgz
kagero-host setup
```

`setup` 自动安装当前用户的 LaunchAgent，启动后台服务，并在终端与图片预览中显示二维码。初始化会在前台准备 macOS 钥匙串身份；首次访问或本地未正式签名的程序升级时可能需要允许访问。自动化验收应在 macOS GUI 登录会话运行初始化，SSH/headless 会话可能无法新增钥匙串记录。关闭终端后服务继续运行，下次登录自动启动。电脑休眠、断网或用户退出登录期间不可保证连接。

再次显示二维码：`kagero-host pair`。二维码 5 分钟有效，每次生成都会替换上一张，只配对一台设备；同一设备可以重试丢失的配对响应。

0.1.1 起二维码图片按整数像素生成，通常不超过 480 px。大二维码默认使用图片预览，避免刷满或超出终端；只有窗口放得下的小二维码才自动打印。需要终端码时运行 `kagero-host --terminal-qr pair`，仍会检查窗口空间，防止换行破坏二维码。`--no-open` 保留为不打开图片预览的选项。

```sh
kagero-host status
kagero-host devices
kagero-host revoke <设备编号>
kagero-host stop
kagero-host uninstall
kagero-host licenses
```

`uninstall` 移除自动启动，保留设备配置和钥匙串身份。App 删除服务器只删除手机上的连接；撤销访问需要在 App 明确选择「撤销此手机的访问」，或使用电脑端 `revoke`。

## Homebrew 与发布

打包脚本同时生成二进制压缩包与仅用于本地验收的 `dist/kagero-host.rb`。发布到 GitHub 前使用下面的准备脚本生成 HTTPS 下载地址与实际校验和，不能直接上传包含 `file://` 地址的本地 formula。npm 包暂未公开发布。

Homebrew 发布准备使用 `python3 scripts/prepare-brew.py --repository OWNER/kagero-host`，默认要求两个架构的压缩包均存在。首版若仅发布 Apple Silicon，可显式加 `--arch arm64`，生成的 formula 会拒绝 Intel 安装。脚本核对压缩包中的实际 CPU 架构、执行权限和许可证，生成 `dist/homebrew-release/<版本>/` 下的 Release 文件、SHA256SUMS、tap 仓库文件及 PUBLISH.md；不会上传、创建仓库或公开发布。`OWNER` 必须换成真实 GitHub 用户或组织。

通过 Homebrew 升级后，再执行 `kagero-host setup` 更新正在使用的后台程序；仅 `brew upgrade` 不会替换已经复制到私有目录的服务程序。升级保留电脑身份和手机授权记录。

`package.py --arch amd64` 可构建 Intel Mac；默认当前架构。依次构建两个架构时 npm 包会包含两种可执行文件。首版只支持 macOS；不在 Linux 上使用文件代替钥匙串静默降级。

源码镜像由 Kagero 主项目维护。普通源码推送不会发布；推送与 Go/manifest 版本一致的 `vMAJOR.MINOR.PATCH` tag 后，GitHub Actions 在两种 Mac 架构上测试、构建，发布测试版并更新 Homebrew。维护者需要为本仓库配置仅能写入 `homebrew-tap` 的 `TAP_DEPLOY_KEY` secret。已发布版本不覆盖，发布说明来自 `RELEASE.md`。

## 隔离安装验收

```sh
npm install --prefix /private/tmp/kagero-host-install ./dist/kagero-host-0.1.0.tgz
/private/tmp/kagero-host-install/node_modules/.bin/kagero-host --state-dir /private/tmp/kagero-host-acceptance --no-open setup
```

`--state-dir` 隔离配置、钥匙串账户和 LaunchAgent 标签。之后命令使用相同参数。不会改动其他 Tailcat 密钥、系统 SSH 或现有 tmux 会话。

## 协议和权限

- 连接使用固定版本 Tailcat 的用户态 WireGuard 通道；仅提供内部 2222 端口，不监听公网 TCP 端口。
- QR 中携带短期配对令牌、Tailcat 地址、电脑 ID 和 SSH 公钥。App 确认电脑后，将主机密钥固定为二维码中的值；不接受网络返回的其他密钥。
- `kagero-pair` 账号只能提交版本化的配对请求，不允许 shell、PTY 命令执行或 SFTP。配对成功后只接受登记过的 Ed25519 公钥，访问权限等于运行服务的当前 macOS 用户。
- 主机私钥保存在 macOS Keychain。`state.json` 只存电脑名称与公钥等元数据。二维码含临时凭据，生成到私有配置目录，不写入后台日志。
- 公共 DERP 服务目前免费且限速。代码许可证不保证公共中继的无限容量或可用性。

## 开源许可

Kagero Host 自有封装代码为 MIT；Tailcat 为 BSD-3-Clause。打包时按目标实际依赖收集 Go 标准库、Tailcat、SSH/SFTP、QR 和其他依赖的版权、LICENSE、NOTICE；随 npm 包、brew 压缩包以及可执行文件 `licenses` 一起提供。以 `go.mod` / `go.sum` 固定版本，升级后重新生成声明。
