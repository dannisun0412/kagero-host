package host

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestRealSSHEnrollmentIsolationAndRevocation(t *testing.T) {
	p, invitation, request, signer := testPairing(t)
	s := &Server{store: p.store, pair: p, signer: signer, username: "test-user", active: map[net.Conn]string{}, seats: make(chan struct{}, 32)}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close(); s.closeAll() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	dial := func(user string, auth ssh.AuthMethod) (*ssh.Client, error) {
		return ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 3 * time.Second})
	}
	if c, err := dial("test-user", ssh.PublicKeys(signer)); err == nil {
		c.Close()
		t.Fatal("unpaired public key accepted")
	}
	if c, err := dial("test-user", ssh.Password(invitation.Token)); err == nil {
		c.Close()
		t.Fatal("pair token granted normal account")
	}
	pair, err := dial("kagero-pair", ssh.Password(invitation.Token))
	if err != nil {
		t.Fatal(err)
	}
	defer pair.Close()
	file := filepath.Join(t.TempDir(), "should-not-exist")
	session, err := pair.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Run("touch " + file); err == nil {
		t.Fatal("pairing account got command execution")
	}
	session.Close()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("pairing account executed command")
	}
	if fs, err := sftp.NewClient(pair); err == nil {
		fs.Close()
		t.Fatal("pairing account got SFTP")
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	session, err = pair.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := session.Output("kagero-pair-v1 " + base64.RawURLEncoding.EncodeToString(data))
	session.Close()
	if err != nil {
		t.Fatal(err)
	}
	var reply PairReply
	if json.Unmarshal(out, &reply) != nil || reply.DeviceID == "" {
		t.Fatal("missing enrollment receipt")
	}
	client, err := dial("test-user", ssh.PublicKeys(signer))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	session, err = client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err = session.Output("kagero-host:info:v1")
	session.Close()
	if err != nil || !strings.Contains(string(out), p.store.State.ID) {
		t.Fatal("authenticated host info failed", err)
	}
	fs, err := sftp.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "unicode.bin")
	remote, err := fs.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("中文🙂abc\x00", 8000)
	if _, err = io.WriteString(remote, payload); err != nil {
		t.Fatal(err)
	}
	if err = remote.Close(); err != nil {
		t.Fatal(err)
	}
	remote, err = fs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	received, err := io.ReadAll(remote)
	remote.Close()
	fs.Close()
	if err != nil || string(received) != payload {
		t.Fatal("SFTP round-trip mismatch", err)
	}
	// A separate control connection revokes the live file/terminal connection.
	control, err := dial("test-user", ssh.PublicKeys(signer))
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	session, err = control.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err = session.Output("kagero-host:revoke-self:v1")
	session.Close()
	if err != nil || !strings.Contains(string(out), `"revoked":true`) {
		t.Fatal("revocation receipt failed", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- client.Wait() }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("existing connection not closed after revocation")
	}
	if c, err := dial("test-user", ssh.PublicKeys(signer)); err == nil {
		c.Close()
		t.Fatal("revoked device connected again")
	}
	if session, err := control.NewSession(); err == nil {
		if err := session.Run("kagero-host:info:v1"); err == nil {
			t.Fatal("revoked connection opened new session")
		}
		session.Close()
	}
}
