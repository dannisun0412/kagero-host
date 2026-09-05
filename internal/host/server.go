package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	ssh "github.com/tailscale/gliderssh"
	"github.com/tailscale/tailcat"
	gossh "golang.org/x/crypto/ssh"
	// Register the mapper hook; magicsock alone only links its interface.
	_ "tailscale.com/feature/portmapper"
	"tailscale.com/wgengine/filter"
)

type authContextKey struct{}
type authIdentity struct{ device, token string }
type Server struct {
	store      *Store
	pair       *Pairing
	cloud      cloudInvitations
	tunnel     *tailcat.Server
	direct     *directListener
	address    string
	relayError string
	signer     gossh.Signer
	username   string
	mu         sync.Mutex
	active     map[net.Conn]string
	closed     bool
	seats      chan struct{}
}

func Run(ctx context.Context, dir string) error {
	store, err := OpenStore(dir)
	if err != nil {
		return err
	}
	// A per-profile lock prevents concurrent daemons from replacing the control socket.
	lock, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("此配置的 Kagero Host 已在运行")
	}
	signer, err := store.Signer()
	if err != nil {
		return err
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	s := &Server{store: store, signer: signer, username: u.Username, active: map[net.Conn]string{}, seats: make(chan struct{}, 32)}
	s.pair = &Pairing{store: store, now: time.Now}
	s.tunnel = &tailcat.Server{Key: store.Secret.Tailcat.Private, PresharedKey: store.Secret.Tailcat.Public.PresharedKey,
		Logf: func(string, ...any) {}, ServedTCPPorts: []filter.PortRange{{First: SSHPort, Last: SSHPort}}}
	if len(store.Secret.Tailcat.Public.Region) > 0 {
		s.tunnel.Region = store.Secret.Tailcat.Public.Region[0]
	}
	s.tunnel.OnTCP = func(port uint16) func(net.Conn) {
		if port == SSHPort {
			return s.handle
		}
		return nil
	}
	// Direct access and the local control API must work even when DERP discovery fails.
	s.direct, err = newDirectListener(dir, s.handle)
	if err != nil {
		return err
	}
	defer s.direct.Close()
	defer s.closeAll()
	if err := s.direct.configure(s.direct.config, false); err != nil {
		fmt.Fprintln(os.Stderr, "直连入口未启动；可通过 kagero-host direct 设置其他端口。")
	}
	relayCtx, cancelRelay := context.WithCancel(ctx)
	relayDone := make(chan struct{})
	defer func() { cancelRelay(); <-relayDone }()
	go func() {
		defer close(relayDone)
		ci := store.Secret.Tailcat.Public
		if len(ci.Region) == 0 {
			if ci.RegionID == 0 {
				ci.RegionID = -1
			}
			if err := ci.Expand(relayCtx, tailcat.ExpandForServer); err != nil {
				s.mu.Lock()
				s.relayError = "Tailcat 中继发现失败；直连仍可使用"
				s.mu.Unlock()
				return
			}
		}
		if relayCtx.Err() != nil {
			return
		}
		s.tunnel.Region = ci.Region[0]
		if err := s.tunnel.Start(); err != nil {
			s.mu.Lock()
			s.relayError = "Tailcat 启动失败；直连仍可使用"
			s.mu.Unlock()
			return
		}
		defer s.tunnel.Close()
		address := string(s.tunnel.TailcatAddr())
		if updated, err := tailcat.ParseAddr(tailcat.Addr(address)); err == nil {
			store.Secret.Tailcat.Public = updated
			if err := store.saveSecret(); err != nil {
				s.mu.Lock()
				s.relayError = "无法保存中继信息；本次连接仍可使用"
				s.mu.Unlock()
			}
		}
		s.mu.Lock()
		s.address = address
		s.mu.Unlock()
		<-relayCtx.Done()
	}()
	socket := filepath.Join(dir, "control.sock")
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer l.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	mux := http.NewServeMux()
	s.cloudControl(mux)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		relayReady, relayError := s.address != "", s.relayError
		s.mu.Unlock()
		writeJSON(w, map[string]any{"version": Version, "id": store.State.ID, "name": store.State.Name, "devices": len(store.Devices()), "running": true, "direct": s.direct.status(), "relayReady": relayReady, "relayError": relayError})
	})
	mux.HandleFunc("GET /devices", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, store.Devices()) })
	mux.HandleFunc("POST /pair", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		address := s.address
		s.mu.Unlock()
		endpoints := s.direct.endpoints()
		if address == "" && len(endpoints) == 0 {
			http.Error(w, "暂无可用入口，请检查 status 或配置直连入口", 503)
			return
		}
		i, err := s.pair.New(address, strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey()))))
		i.Endpoints = endpoints
		if address == "" {
			i.Version = 2
		}
		if err != nil {
			http.Error(w, "无法创建配对码", 500)
			return
		}
		writeJSON(w, i)
	})
	mux.HandleFunc("GET /direct", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.direct.status()) })
	mux.HandleFunc("POST /direct", func(w http.ResponseWriter, r *http.Request) {
		var c DirectConfig
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&c) != nil {
			http.Error(w, "直连配置无效", 400)
			return
		}
		if err := s.direct.configure(c, true); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, s.direct.status())
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.ID) != 32 {
			http.Error(w, "设备编号无效", 400)
			return
		}
		if err := store.Revoke(req.ID); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.closeDevice(req.ID)
		writeJSON(w, map[string]bool{"revoked": true})
	})
	control := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
	defer control.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			control.Close()
			s.closeAll()
		case <-done:
		}
	}()
	fmt.Println("Kagero Host 已启动。基于 Tailcat · BSD-3-Clause。")
	err = control.Serve(l)
	if ctx.Err() != nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) handle(conn net.Conn) {
	select {
	case s.seats <- struct{}{}:
	default:
		conn.Close()
		return
	}
	defer func() { <-s.seats }()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return
	}
	s.active[conn] = ""
	s.mu.Unlock()
	defer func() { conn.Close(); s.mu.Lock(); delete(s.active, conn); s.mu.Unlock() }()
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	sessionSeats := make(chan struct{}, 16)
	srv := &ssh.Server{HostSigners: []ssh.Signer{s.signer}, Version: "KageroHost_" + Version, HandshakeTimeout: 12 * time.Second,
		ChannelHandlers: map[string]ssh.ChannelHandler{"session": func(srv *ssh.Server, transport *gossh.ServerConn, channel gossh.NewChannel, ctx ssh.Context) {
			select {
			case sessionSeats <- struct{}{}:
				defer func() { <-sessionSeats }()
				ssh.DefaultSessionHandler(srv, transport, channel, ctx)
			default:
				_ = channel.Reject(gossh.ResourceShortage, "too many sessions")
			}
		}}, RequestHandlers: map[string]ssh.RequestHandler{},
		PasswordHandler: func(ctx ssh.Context, password string) bool {
			if ctx.User() != "kagero-pair" || s.pairingFor(password) == nil {
				return false
			}
			ctx.SetValue(authContextKey{}, authIdentity{token: password})
			return true
		},
		PublicKeyHandler: func(ctx ssh.Context, public ssh.PublicKey) error {
			if ctx.User() != s.username {
				return errors.New("unknown account")
			}
			id, ok := s.store.authorize(public)
			if !ok {
				return errors.New("unpaired device")
			}
			ctx.SetValue(authContextKey{}, authIdentity{device: id})
			s.mu.Lock()
			s.active[conn] = id
			s.mu.Unlock()
			return nil
		},
	}
	// Check authorization again after the handshake, including revocations racing authentication.
	authorized := func(session ssh.Session) (authIdentity, bool) {
		a, ok := session.Context().Value(authContextKey{}).(authIdentity)
		if !ok {
			return a, false
		}
		if a.token != "" {
			return a, s.pairingFor(a.token) != nil
		}
		if session.PublicKey() == nil {
			return a, false
		}
		_, ok = s.store.authorize(session.PublicKey())
		return a, ok
	}
	srv.Handler = func(session ssh.Session) {
		a, ok := authorized(session)
		if !ok {
			session.Exit(1)
			return
		}
		if a.token != "" {
			s.handlePair(session, a.token)
			return
		}
		_ = conn.SetDeadline(time.Time{})
		if session.RawCommand() == "kagero-host:info:v1" {
			_ = json.NewEncoder(session).Encode(map[string]any{"id": s.store.State.ID, "name": s.store.State.Name, "version": Version, "deviceID": a.device, "endpoints": s.endpoints(), "address": s.tailcatAddress()})
			session.Exit(0)
			return
		}
		if session.RawCommand() == "kagero-host:revoke-self:v1" {
			if err := s.store.Revoke(a.device); err != nil {
				session.Exit(1)
				return
			}
			_ = json.NewEncoder(session).Encode(map[string]bool{"revoked": true})
			_ = session.Exit(0)
			// Keep the control connection until its client receives the acknowledgment.
			// Authorization is already revoked; no new session can start on it.
			s.closeDeviceExcept(a.device, conn)
			return
		}
		runSession(session)
	}
	srv.SubsystemHandlers = map[string]ssh.SubsystemHandler{"sftp": func(session ssh.Session) {
		a, ok := authorized(session)
		if !ok || a.token != "" {
			session.Exit(1)
			return
		}
		_ = conn.SetDeadline(time.Time{})
		u, err := user.Current()
		if err != nil {
			session.Exit(1)
			return
		}
		fs, err := sftp.NewServer(session, sftp.WithServerWorkingDirectory(u.HomeDir))
		if err != nil {
			session.Exit(1)
			return
		}
		defer fs.Close()
		if err := fs.Serve(); err != nil && !errors.Is(err, io.EOF) {
			session.Exit(1)
		}
	}}
	srv.HandleConn(conn)
}
func (s *Server) handlePair(session ssh.Session, token string) {
	const prefix = "kagero-pair-v1 "
	command := session.RawCommand()
	if !strings.HasPrefix(command, prefix) || len(command) > 4096 {
		session.Exit(1)
		return
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(command, prefix))
	if err != nil {
		session.Exit(1)
		return
	}
	var req PairRequest
	if json.Unmarshal(data, &req) != nil {
		session.Exit(1)
		return
	}
	p := s.pairingFor(token)
	if p == nil {
		session.Exit(1)
		return
	}
	reply, err := p.Complete(token, req, s.username)
	if err != nil {
		_ = json.NewEncoder(session).Encode(PairReply{Error: err.Error()})
		session.Exit(0)
		return
	}
	reply.Endpoints = s.endpoints()
	_ = json.NewEncoder(session).Encode(reply)
	session.Exit(0)
}
func (s *Server) closeDevice(id string) {
	s.closeDeviceExcept(id, nil)
}
func (s *Server) closeDeviceExcept(id string, except net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn, device := range s.active {
		if device == id && conn != except {
			conn.Close()
		}
	}
}
func (s *Server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for c := range s.active {
		c.Close()
	}
}

func (s *Server) endpoints() []Endpoint {
	if s.direct == nil {
		return nil
	}
	return s.direct.endpoints()
}

func (s *Server) tailcatAddress() string { s.mu.Lock(); defer s.mu.Unlock(); return s.address }
