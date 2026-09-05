# Kagero Host

Kagero 的 macOS 电脑端伴侣：安装、生成二维码，在 App「添加服务器 → 扫码连接」配对。无需系统 VPN、系统 SSH 服务、账号密码或管理员权限。基于 [Tailcat](https://github.com/tailscale/tailcat)，为独立项目，不代表 Tailscale 官方产品或背书。

## Homebrew 安装

公开安装包见 [GitHub 测试版本](https://github.com/dannisun0412/kagero-host/releases)，安装源为 [dannisun0412/homebrew-tap](https://github.com/dannisun0412/homebrew-tap)。在终端运行：

```sh
brew install dannisun0412/tap/kagero-host
kagero-host setup
```

需要 macOS 13 Ventura 或更新版本，以及支持扫码配对的 Kagero App。首次运行请在电脑已登录的桌面终端中执行。安装不会自行启动远程访问；执行 `setup` 后启动后台服务并显示二维码，之后登录电脑时自动启动。

当前为测试版本，提供 Apple Silicon 和 Intel 构建，暂未进行 Developer ID 签名和 Apple 公证。Apple Silicon 已完成本地安装与 App 模拟器扫码配对验收；自动发布在两种架构的原生 runner 上运行 Go race tests 和可执行文件版本检查。Intel 和较旧 macOS 的完整 App 配对仍需对应设备验证。

## 本地构建与安装

需要 macOS、Xcode Command Line Tools、Go 1.27.1、Node.js / npm。

```sh
cd kagero-host
python3 scripts/package.py
KAGERO_HOST_VERSION="$(node -p 'require("./packaging/npm/package.json").version')"
npm install -g "./dist/kagero-host-${KAGERO_HOST_VERSION}.tgz"
kagero-host setup
```

`setup` 自动安装当前用户的 LaunchAgent，启动后台服务，并在终端与图片预览中显示二维码。初始化会在前台准备 macOS 钥匙串身份；首次访问或本地未正式签名的程序升级时可能需要允许访问。自动化验收应在 macOS GUI 登录会话运行初始化，SSH/headless 会话可能无法新增钥匙串记录。关闭终端后服务继续运行，下次登录自动启动。电脑休眠、断网或用户退出登录期间不可保证连接。

钥匙串弹窗要求的是 Mac 登录钥匙串密码。输入后选择「始终允许」以记住对当前程序的授权；「允许」仅授权本次访问。程序升级或钥匙串锁定时仍可能需要重新授权。重复执行 `setup` 时，如果后台程序与当前版本完全一致且服务正常，会直接生成新的配对码，不重启服务、不重新读取身份密钥；日常新增手机也可直接执行 `kagero-host pair`。

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

- 同时提供 Tailcat 用户态 WireGuard 内部 2222 端口及独立 SSH TCP 2223 入口（默认监听本机网络接口，可关闭）。不修改系统 SSH、路由器端口映射或防火墙；公网入站需用户配置。两个入口复用同一主机密钥、配对授权、撤销和连接上限。
- QR 中携带短期配对令牌、Tailcat 地址、电脑 ID 和 SSH 公钥。App 确认电脑后，将主机密钥固定为二维码中的值；不接受网络返回的其他密钥。
- `kagero-pair` 账号只能提交版本化的配对请求，不允许 shell、PTY 命令执行或 SFTP。配对成功后只接受登记过的 Ed25519 公钥，访问权限等于运行服务的当前 macOS 用户。
- 主机私钥保存在 macOS Keychain。`state.json` 只存电脑名称与公钥等元数据。二维码含临时凭据，生成到私有配置目录，不写入后台日志。
- 公共 DERP 服务目前免费且限速。代码许可证不保证公共中继的无限容量或可用性。
- Host 链接 Tailscale 的 NAT-PMP／PCP／UPnP 端口映射模块，在路由器允许时自动申请加密 UDP 隧道的映射，由底层维护映射生命周期。动态公网 IP 不需要写入配对二维码；不支持映射或 UDP 被代理/防火墙拦截时仍可能使用 DERP。不会为系统 SSH 的 TCP 22 端口创建映射。此能力已做发布编译参数检查，尚未在用户家庭路由器实测。

## 多路径连接（源码实现，待发版）

扫码仍是一次性授权。新二维码包含当前直连候选；没有可用 Tailcat 地址时生成 version 2 直连二维码，需要对应新版 App。旧 Host / version 1 二维码仍被新版 App 接受。独立 TCP 入口允许局域网及已放行的公网 IPv4/IPv6，不要求手机建立系统 VPN。

```sh
# 查看监听状态；不输出 Tailcat 连接凭据
kagero-host direct

# 本地监听 TCP 2223；公网域名及外部端口（外部端口可与内部不同）
kagero-host direct --port 2223 --endpoint home.example.com:2223

# 关闭新的 TCP 直连；已有连接和远端 tmux 会话保留
kagero-host direct --disable
```

`direct` 带参数时替换并保存整份直连配置：本地端口默认 2223，公网入口可重复 `--endpoint` 最多两次，不提供时清空此前公网入口。配置存入 `network.json`，不包含密钥，立即生效；监听失败保留此前有效配置。默认自动发布一条物理网卡 IPv4 和一条全局 IPv6 提示，不发布 utun/VPN 地址。多网卡电脑可手动指定合适入口。

域名由路由器或现有 DDNS 客户端维护 A/AAAA。Host 本版本不持有 DNS API token，不自动更新第三方 DNS，也不会自动开启路由器 TCP 端口。公网 IPv4 需将外部 TCP 端口映射到电脑的 TCP 2223；IPv6 需放行到电脑的对应端口。只使用 A 记录或只使用 AAAA 记录均可，客户端由 SwiftNIO 处理地址族连接。电脑应保持唤醒和网络在线。

Host 的中继发现与直连/本机控制接口分开启动；中继发现失败可继续直连，`status` 会显示 relayError。本次进程启动时中继初始化失败后不无限重试，排除网络问题后需重新启动 Host。SSH 密钥仍相同，不需要为直连重新授权设备。

旧手机配对记录在成功连接新版 Host 后通过已认证 SSH 学习新入口。如果旧 Tailcat 路径完全不可达，可在 App「已配对电脑 → 高级设置 → 直连入口」填写域名与端口，或重新扫码。地址变化不会改变电脑身份，所有入口均验证原 SSH 公钥。不能把未验证的 DNS/二维码地址当作主机身份。

验证：`go test -race ./internal/host ./cmd/kagero-host`。本地测试使用临时身份和 loopback 服务，不操作用户 Host、路由器或真实 tmux 会话。公网/VPN 延迟与手机画面需要两端更新后实测。

## 开源许可

Kagero Host 自有封装代码为 MIT；Tailcat 为 BSD-3-Clause。打包时按目标实际依赖收集 Go 标准库、Tailcat、SSH/SFTP、QR 和其他依赖的版权、LICENSE、NOTICE；随 npm 包、brew 压缩包以及可执行文件 `licenses` 一起提供。以 `go.mod` / `go.sum` 固定版本，升级后重新生成声明。

### Developer ID 签名与公证自动化

公开仓库的 `v*` 发布流程要求每个架构在打包前完成 Developer ID Application 签名和 Apple 公证；缺少凭据、签名错误、公证拒绝或等待超时都会阻止发布，不降级为未签名发布。私有主仓库仍由 `host-v*` 同步 Host 源码并触发公开仓库发布。

在 **dannisun0412/kagero-host** 的 Actions Secrets 中配置：

- `HOST_P12_BASE64`：包含 Developer ID Application 证书及私钥的加密 P12，Base64 编码。
- `HOST_P12_PASSWORD`：上述 P12 的密码。
- `HOST_SIGNING_IDENTITY`：该签名证书的 SHA-1 标识（`security find-identity -v -p codesigning`）。
- `HOST_NOTARY_KEY_BASE64`：公证用 App Store Connect 团队 API 私钥 P8 的 Base64 编码。
- `HOST_NOTARY_KEY_ID`、`HOST_NOTARY_ISSUER_ID`：该 API 密钥的 Key ID、Issuer ID。

构建任务创建临时钥匙串、验证公证凭据，结束时清理。密钥不放入源码、日志或构建产物。公证使用 `notarytool`，签名启用 hardened runtime、可信时间戳和固定标识 `app.kagero.host`。独立命令行 Mach-O 无法 stapling；Apple 保存对应签名的公证票据，brew/npm 包必须保留已公证二进制的原始字节。

本地签名验收可先将公证凭据存入 Keychain 的专用 profile，再运行：

```sh
HOST_SIGNING_IDENTITY='<证书 SHA-1>' HOST_NOTARY_PROFILE='kagero-host-notary' \
  python3 scripts/package.py --signed --arch arm64
```

不带 `--signed` 仅用于本地开发打包，不代表可公开分发的已签名、公证版本。已有 0.1.3 发布物仍未签名；必须在凭据配置完成后发布新版本并实际验收，不能仅依据脚本或证书创建成功宣称发布完成。

## iCloud 同账号发现（开发中）

原生 `apple/` Mac 菜单栏组件与 iOS 共享 CloudHostKit，使用 CloudKit private database 交换设备描述和手机公钥绑定的短期配对授权。终端仍走已验证主机身份的 SSH，iCloud 不转发终端数据。没有 iCloud 或账号不同的设备继续扫码。

包含组件的签名版本运行 `kagero-host icloud` 后启用共享；需要先运行 `setup`。组件仅支持默认配置目录、macOS 13+，旧安装包会提示升级。私钥不上传，账号改变后必须重新启用；关闭共享不撤销既有 SSH 配对。

开发编译 `swift test --package-path apple`、`python3 scripts/build-cloud.py --arch arm64`。默认仅生成 ad-hoc 编译包，不能用于真实 iCloud。正式打包增加 `--with-icloud`，需要匹配 `app.kagero.host.cloud` 的 Developer ID 描述文件（`HOST_CLOUD_PROFILE`）、容器 `iCloud.com.kageroai.terminalai`、CloudKit/Push 权限与 Production schema。CI 增加 `HOST_CLOUD_PROFILE_BASE64` Secret 和 `HOST_ICLOUD_ENABLED=true` 变量后才携带组件；此变量应在签名同步验收后启用。

当前完成本地构建和协议测试；尚未进行实际 iCloud 两端同步、登录项、界面及公网验收。不承诺所有网络都能直连，也不承诺 iOS 后台永久保持 SSH。
