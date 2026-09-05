package main

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"app.kagero/host/internal/host"
	"github.com/fsnotify/fsnotify"
)

func label(dir string) string {
	return fmt.Sprintf("app.kagero.host.%x", sha256.Sum256([]byte(dir)))[:36]
}
func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }
func plistPath(dir string) string {
	base, _ := os.UserHomeDir()
	return filepath.Join(base, "Library", "LaunchAgents", label(dir)+".plist")
}
func escaped(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func launchctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("自动启动配置失败：%s", strings.TrimSpace(string(out)))
	}
	return nil
}
func stop(dir string) error {
	// A missing job is already stopped. Never stop other Kagero/Tailcat instances.
	if exec.Command("/bin/launchctl", "print", domain()+"/"+label(dir)).Run() != nil {
		return nil
	}
	return launchctl("bootout", domain()+"/"+label(dir))
}
func setup(dir string) error {
	if os.Getuid() == 0 {
		return fmt.Errorf("请使用当前用户安装，不要使用 sudo")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("配置目录需要 0700 权限")
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	target := filepath.Join(bin, "kagero-host")
	if err := stop(dir); err != nil {
		return err
	}
	if executable != target {
		data, err := os.ReadFile(executable)
		if err != nil {
			return err
		}
		if err := host.AtomicWrite(target, data, 0700); err != nil {
			return err
		}
	}
	if err := host.AtomicWrite(filepath.Join(dir, "THIRD-PARTY-NOTICES.txt"), []byte(host.Notices), 0600); err != nil {
		return err
	}
	// Initialize using the final executable path, in the foreground. A local,
	// unsigned upgrade may require Keychain consent; do not hide that in a daemon.
	prepareCtx, cancelPrepare := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancelPrepare()
	prepare := exec.CommandContext(prepareCtx, target, "--state-dir", dir, "prepare")
	prepare.Stdin, prepare.Stdout, prepare.Stderr = os.Stdin, os.Stderr, os.Stderr
	if err := prepare.Run(); err != nil {
		return fmt.Errorf("主机身份尚未准备好。请允许 macOS 钥匙串访问后重新运行 setup")
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>--state-dir</string><string>%s</string><string>serve</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>30</integer><key>Umask</key><integer>63</integer>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>
<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>
</dict></plist>`, escaped(label(dir)), escaped(target), escaped(dir), escaped(filepath.Join(dir, "host.log")), escaped(filepath.Join(dir, "host.log")))
	path := plistPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := host.AtomicWrite(path, []byte(plist), 0600); err != nil {
		return err
	}
	watch, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watch.Close()
	if err := watch.Add(dir); err != nil {
		return err
	}
	if err := launchctl("bootstrap", domain(), path); err != nil {
		return err
	}
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	for {
		var status map[string]any
		if _, err := os.Stat(filepath.Join(dir, "control.sock")); err == nil {
			if err := host.Control(dir, "GET", "/status", nil, &status); err == nil {
				return nil
			}
		}
		select {
		case _, ok := <-watch.Events:
			if !ok {
				return fmt.Errorf("启动监听已关闭")
			}
		case err := <-watch.Errors:
			return err
		case <-deadline.C:
			return fmt.Errorf("服务尚未就绪，请检查 %s；解锁钥匙串后执行 kagero-host pair", filepath.Join(dir, "host.log"))
		}
	}
}
