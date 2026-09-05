package host

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// Invitations remain in memory and expire; cloud enrollment cannot replace a QR.
type cloudInvitations struct {
	mu    sync.Mutex
	items map[string]*Pairing
}
type CloudInviteRequest struct {
	ID        string `json:"id"`
	HostID    string `json:"hostID"`
	HostKey   string `json:"hostKey"`
	PublicKey string `json:"publicKey"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (s *Server) cloudInvitation(req CloudInviteRequest) (Invitation, error) {
	s.cloud.mu.Lock()
	defer s.cloud.mu.Unlock()
	now := time.Now()
	hostKey := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(s.signer.PublicKey())))
	if !validCloudID(req.ID) || !strings.EqualFold(req.HostID, s.store.State.ID) || req.HostKey != hostKey || req.ExpiresAt <= now.Unix() || req.ExpiresAt > now.Add(5*time.Minute).Unix() {
		return Invitation{}, errors.New("iCloud 请求已过期或电脑身份不匹配")
	}
	req.ID = strings.ToLower(req.ID)
	pub, _, options, rest, err := gossh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil || len(req.PublicKey) > 256 || len(options) != 0 || len(rest) != 0 || pub.Type() != gossh.KeyAlgoED25519 {
		return Invitation{}, errors.New("iCloud 设备公钥无效")
	}
	if s.cloud.items == nil {
		s.cloud.items = map[string]*Pairing{}
	}
	for id, p := range s.cloud.items {
		if p.invitation.ExpiresAt <= now.Unix() {
			delete(s.cloud.items, id)
		}
	}
	if p := s.cloud.items[req.ID]; p != nil {
		if p.expectedKey != deviceID(pub) {
			return Invitation{}, errors.New("iCloud 请求身份已改变")
		}
		return p.invitation, nil
	}
	if len(s.cloud.items) >= 32 {
		return Invitation{}, errors.New("待配对请求过多，请稍后再试")
	}
	address, endpoints := s.tailcatAddress(), s.endpoints()
	if address == "" && len(endpoints) == 0 {
		return Invitation{}, errors.New("电脑暂无可用连接入口")
	}
	p := &Pairing{store: s.store, now: time.Now, expectedKey: deviceID(pub)}
	i, err := p.New(address, hostKey)
	if err != nil {
		return Invitation{}, err
	}
	i.ExpiresAt = req.ExpiresAt
	i.Endpoints = endpoints[:min(4, len(endpoints))]
	if address == "" {
		i.Version = 2
	}
	p.invitation = i
	s.cloud.items[req.ID] = p
	return i, nil
}

func (s *Server) pairingFor(token string) *Pairing {
	if s.pair != nil && s.pair.Accepts(token) {
		return s.pair
	}
	s.cloud.mu.Lock()
	defer s.cloud.mu.Unlock()
	for _, p := range s.cloud.items {
		if p.Accepts(token) {
			return p
		}
	}
	return nil
}

func (s *Server) cloudControl(mux *http.ServeMux) {
	mux.HandleFunc("POST /icloud/clear", func(w http.ResponseWriter, r *http.Request) {
		s.cloud.mu.Lock()
		s.cloud.items = nil
		s.cloud.mu.Unlock()
		writeJSON(w, map[string]bool{"cleared": true})
	})
	// These routes are available only on the 0600 Unix socket, never over SSH/TCP.
	mux.HandleFunc("GET /icloud/host", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"id": s.store.State.ID, "name": s.store.State.Name,
			"hostKey": strings.TrimSpace(string(gossh.MarshalAuthorizedKey(s.signer.PublicKey()))), "updatedAt": time.Now().Unix(), "endpoints": s.endpoints(), "address": s.tailcatAddress(), "publicUDP": s.publicUDP()})
	})
	mux.HandleFunc("POST /icloud/invitation", func(w http.ResponseWriter, r *http.Request) {
		var req CloudInviteRequest
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
			http.Error(w, "请求无效", 400)
			return
		}
		i, err := s.cloudInvitation(req)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]string{"invitation": i.URL()})
	})
}

func validCloudID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	data, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil && len(data) == 16
}
