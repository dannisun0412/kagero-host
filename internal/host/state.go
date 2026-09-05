package host

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tailscale/tailcat"
	"golang.org/x/crypto/ssh"
)

const SSHPort = 2222
const Version = "0.1.7"

// Secret material is stored only in macOS Keychain. JSON contains public metadata.
type Secrets struct {
	Tailcat *tailcat.PrivateKey
	SSHSeed []byte
}
type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
	NodeKey   string `json:"nodeKey"`
	AddedAt   int64  `json:"addedAt"`
}
type State struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Devices []Device `json:"devices"`
}
type Store struct {
	mu     sync.Mutex
	Dir    string
	State  State
	Secret Secrets
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("配置目录必须是仅当前用户可访问的目录（0700）")
	}
	s := &Store{Dir: dir}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil {
		if err := json.Unmarshal(data, &s.State); err != nil {
			return nil, fmt.Errorf("读取配置：%w", err)
		}
		data, err = readSecret(dir)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &s.Secret); err != nil {
			return nil, errors.New("钥匙串中的主机身份无法读取")
		}
	} else if os.IsNotExist(err) {
		// Recover the same identity if saving metadata was interrupted on first install.
		if saved, e := readSecret(dir); e == nil {
			if json.Unmarshal(saved, &s.Secret) != nil {
				return nil, errors.New("无法恢复主机身份")
			}
		} else if errors.Is(e, errSecretMissing) {
			_, private, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				return nil, e
			}
			s.Secret = Secrets{Tailcat: tailcat.NewPrivateKey(), SSHSeed: private.Seed()}
		} else {
			return nil, e
		}
		id := sha256.Sum256(s.Secret.SSHSeed)
		s.State.ID = fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
		s.State.Name, err = os.Hostname()
		if err != nil {
			return nil, err
		}
		s.State.Devices = []Device{}
		if err := s.saveSecret(); err != nil {
			return nil, err
		}
		if err := s.saveState(s.State); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if len(s.Secret.SSHSeed) != ed25519.SeedSize || s.Secret.Tailcat == nil || s.Secret.Tailcat.Private.IsZero() {
		return nil, errors.New("主机身份损坏；请恢复钥匙串后重试")
	}
	return s, nil
}
func (s *Store) saveSecret() error {
	data, err := json.Marshal(s.Secret)
	if err != nil {
		return err
	}
	return writeSecret(s.Dir, data)
}
func (s *Store) saveState(state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(s.Dir, "state.json"), data, 0600)
}
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".kagero-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (s *Store) Signer() (ssh.Signer, error) {
	return ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(s.Secret.SSHSeed))
}
func deviceID(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return hex.EncodeToString(sum[:16])
}
func (s *Store) authorize(public ssh.PublicKey) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := deviceID(public)
	for _, d := range s.State.Devices {
		if d.ID == id {
			return id, true
		}
	}
	return "", false
}
func (s *Store) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Device{}, s.State.Devices...)
}
func (s *Store) enroll(name, node string, public ssh.PublicKey) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := Device{ID: deviceID(public), Name: name, NodeKey: node, PublicKey: string(ssh.MarshalAuthorizedKey(public)), AddedAt: time.Now().Unix()}
	next := s.State
	next.Devices = append([]Device{}, s.State.Devices...)
	found := false
	for i := range next.Devices {
		if next.Devices[i].ID == d.ID {
			next.Devices[i] = d
			found = true
		}
	}
	if !found {
		if len(next.Devices) >= 32 {
			return d, errors.New("已配对设备达到 32 台，请先移除不用的设备")
		}
		next.Devices = append(next.Devices, d)
	}
	if err := s.saveState(next); err != nil {
		return d, err
	}
	s.State.Devices = next.Devices
	return d, nil
}
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.State
	next.Devices = []Device{}
	for _, d := range s.State.Devices {
		if d.ID != id {
			next.Devices = append(next.Devices, d)
		}
	}
	if len(next.Devices) == len(s.State.Devices) {
		return errors.New("未找到该设备")
	}
	if err := s.saveState(next); err != nil {
		return err
	}
	s.State.Devices = next.Devices
	return nil
}
