package host

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	ssh "github.com/tailscale/gliderssh"
)

func runSession(session ssh.Session) {
	u, err := user.Current()
	if err != nil {
		session.Exit(1)
		return
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	var cmd *exec.Cmd
	if session.RawCommand() == "" {
		cmd = exec.Command(shell, "-l")
	} else {
		cmd = exec.Command(shell, "-lc", session.RawCommand())
	}
	cmd.Dir = u.HomeDir
	cmd.Env = append(os.Environ(), "PATH="+os.Getenv("PATH")+":/opt/homebrew/bin:/usr/local/bin", "TERM=xterm-256color")
	for _, v := range session.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if k == "TERM" || k == "LANG" || strings.HasPrefix(k, "LC_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	request, windows, wantsPTY := session.Pty()
	var terminal *os.File
	var output sync.WaitGroup
	if wantsPTY {
		session.DisablePTYEmulation()
		terminal, err = pty.StartWithSize(cmd, &pty.Winsize{Rows: dimension(request.Window.Height), Cols: dimension(request.Window.Width)})
		if err != nil {
			session.Exit(1)
			return
		}
		defer terminal.Close()
		output.Add(1)
		go func() { defer output.Done(); _, _ = io.Copy(session, terminal) }()
		go func() { _, _ = io.Copy(terminal, session) }()
		go func() {
			for {
				select {
				case win, ok := <-windows:
					if !ok {
						return
					}
					_ = pty.Setsize(terminal, &pty.Winsize{Rows: dimension(win.Height), Cols: dimension(win.Width)})
				case <-session.Context().Done():
					return
				}
			}
		}()
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		stdin, e := cmd.StdinPipe()
		if e != nil {
			session.Exit(1)
			return
		}
		stdout, e := cmd.StdoutPipe()
		if e != nil {
			stdin.Close()
			session.Exit(1)
			return
		}
		stderr, e := cmd.StderrPipe()
		if e != nil {
			stdin.Close()
			stdout.Close()
			session.Exit(1)
			return
		}
		if err = cmd.Start(); err != nil {
			stdin.Close()
			stdout.Close()
			stderr.Close()
			session.Exit(1)
			return
		}
		go func() { defer stdin.Close(); _, _ = io.Copy(stdin, session) }()
		output.Add(2)
		go func() { defer output.Done(); _, _ = io.Copy(session, stdout) }()
		go func() { defer output.Done(); _, _ = io.Copy(session.Stderr(), stderr) }()
	}
	finished := make(chan struct{})
	go func() {
		select {
		case <-session.Context().Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
			_ = cmd.Process.Kill()
			if terminal != nil {
				terminal.Close()
			}
		case <-finished:
		}
	}()
	output.Wait()
	err = cmd.Wait()
	close(finished)
	code := 0
	if err != nil {
		code = 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
			if code < 0 {
				code = 1
			}
		}
	}
	_ = session.Exit(code)
}
func dimension(n int) uint16 {
	if n < 1 {
		return 1
	}
	if n > 1000 {
		return 1000
	}
	return uint16(n)
}
