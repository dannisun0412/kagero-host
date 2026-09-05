package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"app.kagero/host/internal/host"
	"github.com/skip2/go-qrcode"
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
		return pair(*dir, *jsonOutput, *noOpen)
	case "pair":
		return pair(*dir, *jsonOutput, *noOpen)
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
		return fmt.Errorf("支持：setup、pair、status、devices、revoke、stop、uninstall、version、licenses")
	}
}
func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
func pair(dir string, jsonOutput, noOpen bool) error {
	var invitation host.Invitation
	if err := host.Control(dir, "POST", "/pair", nil, &invitation); err != nil {
		return err
	}
	code, err := qrcode.New(invitation.URL(), qrcode.Medium)
	if err != nil {
		return err
	}
	png, err := code.PNG(720)
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
	fmt.Print(code.ToSmallString(false))
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
