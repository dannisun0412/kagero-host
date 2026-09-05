package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func openCloud() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	target := filepath.Join(base, "KageroHost", "KageroCloud.app")
	candidates := []string{filepath.Join(filepath.Dir(executable), "KageroCloud.app"), filepath.Join(filepath.Dir(executable), "..", "libexec", "KageroCloud.app"), target}
	var source string
	for _, path := range candidates {
		if info, e := os.Stat(path); e == nil && info.IsDir() {
			source = filepath.Clean(path)
			break
		}
	}
	if source == "" {
		return fmt.Errorf("此安装包尚未包含 iCloud 组件，请安装启用 iCloud 的签名版本")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if source != target {
		// Verify the bundle before copying it to the stable login-item location.
		if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", source).Run(); err != nil {
			return fmt.Errorf("iCloud 组件签名验证失败")
		}
		if _, err := os.Stat(target); err == nil {
			old, oldErr := cloudBundleDigest(target)
			next, nextErr := cloudBundleDigest(source)
			if oldErr == nil && nextErr == nil && old == next {
				return exec.CommandContext(ctx, "/usr/bin/open", target).Run()
			}
			if exec.CommandContext(ctx, "/usr/bin/pgrep", "-x", "KageroCloud").Run() == nil {
				return fmt.Errorf("请先从菜单栏退出 Kagero iCloud，再运行 kagero-host icloud 更新组件；Host 终端连接不受影响")
			}
		}
		stage, err := os.MkdirTemp(filepath.Dir(target), ".icloud-install-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		staged := filepath.Join(stage, "KageroCloud.app")
		if err := exec.CommandContext(ctx, "/usr/bin/ditto", source, staged).Run(); err != nil {
			return fmt.Errorf("无法准备 iCloud 组件：%w", err)
		}
		if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", staged).Run(); err != nil {
			return fmt.Errorf("iCloud 组件副本验证失败")
		}
		backup := filepath.Join(stage, "previous.app")
		hadOld := false
		if _, err := os.Stat(target); err == nil {
			if err := os.Rename(target, backup); err != nil {
				return err
			}
			hadOld = true
		}
		if err := os.Rename(staged, target); err != nil {
			if hadOld {
				if restore := os.Rename(backup, target); restore != nil {
					return fmt.Errorf("安装失败且恢复失败：%v / %v", err, restore)
				}
			}
			return err
		}
	}
	return exec.CommandContext(ctx, "/usr/bin/open", target).Run()
}

// Stream a deterministic digest of the whole bundle, including the profile and signature.
func cloudBundleDigest(root string) ([32]byte, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%d:%s:%s\n", len(rel), rel, entry.Type())
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink in iCloud bundle")
		}
		if entry.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(h, f)
		return err
	})
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest, err
}
