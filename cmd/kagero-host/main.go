package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"app.kagero/host/internal/host"
	"github.com/skip2/go-qrcode"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Kagero Host:", err)
		os.Exit(1)
	}
}
func run() error {
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("kagero-host", flag.ContinueOnError)
	dir := flags.String("state-dir", filepath.Join(base, "KageroHost"), "独立配置目录（密钥仍保存在钥匙串）")
	jsonOutput := flags.Bool("json", false, "输出 JSON，pair 的输出包含一次性配对凭据")
	noOpen := flags.Bool("no-open", false, "不打开二维码预览")
	terminalQR := flags.Bool("terminal-qr", false, "在终端显示完整二维码（需要足够窗口空间）")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	*dir, err = filepath.Abs(*dir)
	if err != nil {
		return err
	}
	command := "setup"
	if flags.NArg() > 0 {
		command = flags.Arg(0)
	}
	switch command {
	case "prepare":
		_, err := host.OpenStore(*dir)
		return err
	case "version", "--version":
		fmt.Println(host.Version)
		return nil
	case "licenses":
		fmt.Print(host.Notices)
		return nil
	case "serve":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return host.Run(ctx, *dir)
	case "setup":
		if err := setup(*dir); err != nil {
			return err
		}
		return pair(*dir, *jsonOutput, *noOpen, *terminalQR)
	case "pair":
		return pair(*dir, *jsonOutput, *noOpen, *terminalQR)
	case "direct":
		return configureDirect(*dir, flags.Args()[1:])
	case "icloud-host":
		var value map[string]any
		if err := host.Control(*dir, "GET", "/icloud/host", nil, &value); err != nil {
			return err
		}
		return printJSON(value)
	case "icloud":
		if *dir != filepath.Join(base, "KageroHost") {
			return fmt.Errorf("iCloud 组件仅支持默认配置目录")
		}
		return openCloud()
	case "icloud-clear":
		var value map[string]bool
		if err := host.Control(*dir, "POST", "/icloud/clear", nil, &value); err != nil {
			return err
		}
		return printJSON(value)
	case "icloud-invite":
		// Called by the signed companion through pipes. Never write this to logs.
		var req host.CloudInviteRequest
		d := json.NewDecoder(io.LimitReader(os.Stdin, 2049))
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
			return fmt.Errorf("iCloud 请求无效")
		}
		var value map[string]string
		if err := host.Control(*dir, "POST", "/icloud/invitation", req, &value); err != nil {
			return err
		}
		return printJSON(value)
	case "status":
		var value map[string]any
		if err := host.Control(*dir, "GET", "/status", nil, &value); err != nil {
			return err
		}
		return printJSON(value)
	case "devices":
		var list []host.Device
		if err := host.Control(*dir, "GET", "/devices", nil, &list); err != nil {
			return err
		}
		if *jsonOutput {
			return printJSON(list)
		}
		if len(list) == 0 {
			fmt.Println("还没有配对设备。执行 kagero-host pair 显示二维码。")
		}
		for _, d := range list {
			fmt.Printf("%s  %s\n", d.ID, d.Name)
		}
		return nil
	case "revoke":
		if flags.NArg() != 2 {
			return fmt.Errorf("用法：kagero-host revoke <设备编号>（devices 可查看编号）")
		}
		var result map[string]bool
		if err := host.Control(*dir, "POST", "/revoke", map[string]string{"id": flags.Arg(1)}, &result); err != nil {
			return err
		}
		fmt.Println("已撤销该设备的访问权限并关闭其连接。")
		return nil
	case "stop":
		return stop(*dir)
	case "uninstall":
		if err := stop(*dir); err != nil {
			return err
		}
		if err := os.Remove(plistPath(*dir)); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("已移除自动启动。配对记录和钥匙串身份已保留。")
		return nil
	default:
		return fmt.Errorf("支持：setup、pair、icloud、direct、status、devices、revoke、stop、uninstall、version、licenses")
	}
}
func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
func pair(dir string, jsonOutput, noOpen, terminalQR bool) error {
	var invitation host.Invitation
	if err := host.Control(dir, "POST", "/pair", nil, &invitation); err != nil {
		return err
	}
	code, err := qrcode.New(invitation.URL(), qrcode.Medium)
	if err != nil {
		return err
	}
	png, err := code.PNG(qrImageSize(code))
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "pair.png")
	if err := host.AtomicWrite(path, png, 0600); err != nil {
		return err
	}
	if jsonOutput {
		return printJSON(map[string]any{"url": invitation.URL(), "png": path, "expiresAt": invitation.ExpiresAt})
	}
	fmt.Println("\nKagero Host · 扫码连接这台电脑\n基于 Tailcat 开源项目 · BSD-3-Clause")
	columns, rows, sizeErr := term.GetSize(int(os.Stdout.Fd()))
	if sizeErr == nil && qrFitsTerminal(code, columns, rows, terminalQR) {
		fmt.Print(code.ToSmallString(false))
	} else {
		fmt.Println("二维码已生成，使用图片预览扫码，避免终端换行或显示不全。")
	}
	fmt.Println("打开 Kagero → 添加服务器 → 扫码连接。二维码 5 分钟内有效，仅可配对一台设备。")
	fmt.Println("服务已在后台运行，关闭此终端不会断开；登录电脑后自动启动。")
	fmt.Println("再次配对：kagero-host pair    管理设备：kagero-host devices")
	fmt.Println("二维码图片：", path)
	if !noOpen {
		if err := exec.Command("/usr/bin/open", path).Run(); err != nil {
			return fmt.Errorf("二维码已生成，可手动打开 %s", path)
		}
	}
	return nil
}

// Keep whole pixels per QR module: scaling to an arbitrary size blurs edges.
func qrImageSize(code *qrcode.QRCode) int {
	modules := len(code.Bitmap())
	scale := 480 / modules
	if scale < 2 {
		scale = 2
	}
	return modules * scale
}

func qrFitsTerminal(code *qrcode.QRCode, columns, rows int, allowLarge bool) bool {
	modules := len(code.Bitmap())
	lines := (modules + 1) / 2
	return columns >= modules+2 && rows >= lines+10 && (allowLarge || lines <= 32)
}

// Global flags precede the command; direct has its own options.
func configureDirect(dir string, args []string) error {
	if len(args) == 0 {
		var value map[string]any
		if err := host.Control(dir, "GET", "/direct", nil, &value); err != nil {
			return err
		}
		return printJSON(value)
	}
	flags := flag.NewFlagSet("direct", flag.ContinueOnError)
	port := flags.Int("port", host.DirectPort, "Host 本地 TCP 监听端口")
	disabled := flags.Bool("disable", false, "关闭新直连入口，保留已有会话")
	var endpoints []host.Endpoint
	flags.Func("endpoint", "公网 DDNS 域名:端口，可重复两次；不会自动配置路由器或 DNS", func(value string) error {
		e, err := host.ParseEndpoint(value)
		if err != nil {
			return err
		}
		endpoints = append(endpoints, e)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("用法：kagero-host direct --port 2223 --endpoint home.example.com:2223")
	}
	var value map[string]any
	if err := host.Control(dir, "POST", "/direct", host.DirectConfig{Disabled: *disabled, Port: *port, Public: endpoints}, &value); err != nil {
		return err
	}
	fmt.Println("直连配置已保存并生效。公网 IPv4 需路由器 TCP 端口映射，IPv6 需防火墙放行；域名由现有 DDNS 更新。")
	fmt.Println("新手机运行 kagero-host pair 扫码；已配对手机重新连接后会更新入口，也可在 App 高级设置填写域名。")
	return printJSON(value)
}
