package host

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
	"tailscale.com/types/key"
)

type Invitation struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	HostKey   string `json:"hostKey"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (i Invitation) URL() string {
	data, _ := json.Marshal(i)
	return "kagero://pair?data=" + base64.RawURLEncoding.EncodeToString(data)
}

type PairRequest struct {
	Version   int    `json:"version"`
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
	NodeKey   string `json:"nodeKey"`
}
type PairReply struct {
	Version  int    `json:"version"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	DeviceID string `json:"deviceID"`
	Error    string `json:"error,omitempty"`
}
type Pairing struct {
	mu          sync.Mutex
	invitation  Invitation
	consumedKey string
	reply       PairReply
	store       *Store
	now         func() time.Time
}

func (p *Pairing) New(address, hostKey string) (Invitation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Invitation{}, err
	}
	p.invitation = Invitation{Version: 1, ID: p.store.State.ID, Name: p.store.State.Name, Address: address, Port: SSHPort, HostKey: hostKey, Token: base64.RawURLEncoding.EncodeToString(b), ExpiresAt: p.now().Add(5 * time.Minute).Unix()}
	p.consumedKey = ""
	p.reply = PairReply{}
	return p.invitation, nil
}
func (p *Pairing) validLocked(token string) bool {
	return p.invitation.ExpiresAt > p.now().Unix() && len(token) == 43 && subtle.ConstantTimeCompare([]byte(token), []byte(p.invitation.Token)) == 1
}
func (p *Pairing) Accepts(token string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.validLocked(token)
}
func (p *Pairing) Complete(token string, req PairRequest, username string) (PairReply, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.validLocked(token) {
		return PairReply{}, errors.New("配对码已过期，请在电脑上重新生成二维码")
	}
	if req.Version != 1 || len(req.Name) == 0 || len(req.Name) > 128 || strings.IndexFunc(req.Name, unicode.IsControl) >= 0 || len(req.PublicKey) > 256 {
		return PairReply{}, errors.New("配对信息无效")
	}
	pub, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil || len(options) != 0 || len(rest) != 0 || pub.Type() != ssh.KeyAlgoED25519 {
		return PairReply{}, errors.New("设备公钥无效")
	}
	var node key.NodePublic
	if node.UnmarshalText([]byte(req.NodeKey)) != nil || node.IsZero() {
		return PairReply{}, errors.New("设备网络身份无效")
	}
	id := deviceID(pub)
	if p.consumedKey != "" {
		if id == p.consumedKey {
			return p.reply, nil
		}
		return PairReply{}, errors.New("二维码已被使用，请重新生成")
	}
	d, err := p.store.enroll(req.Name, req.NodeKey, pub)
	if err != nil {
		return PairReply{}, errors.New("电脑无法保存配对，请检查配置目录")
	}
	p.reply = PairReply{Version: 1, ID: p.store.State.ID, Name: p.store.State.Name, Username: username, DeviceID: d.ID}
	p.consumedKey = id
	return p.reply, nil
}
