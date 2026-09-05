package host

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestEndpointValidation(t *testing.T) {
	for _, input := range []string{"home.example.com:2223", "192.168.1.2:2223", "[2001:db8::12]:2223"} {
		if _, err := ParseEndpoint(input); err != nil {
			t.Fatal(input, err)
		}
	}
	for _, input := range []string{"https://home.example.com:22", "user@home.example.com:22", "home.example.com:0", "[::1]:22", "[fe80::1]:22", "127.0.0.1:22", "0.0.0.0:22", "0.1.2.3:22", "999.1.1.1:22", "[::ffff:127.0.0.1]:22", "home.example.com:22/path", "x..example:22"} {
		if _, err := ParseEndpoint(input); err == nil {
			t.Fatal("accepted", input)
		}
	}
}

func TestDirectConfigAtomicFailureAndDisablePreservesSessions(t *testing.T) {
	p, invitation, request, signer := testPairing(t)
	if _, err := p.Complete(invitation.Token, request, "test-user"); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: p.store, pair: p, signer: signer, username: "test-user", active: map[net.Conn]string{}, seats: make(chan struct{}, 32)}
	d, err := newDirectListener(t.TempDir(), s.handle)
	if err != nil {
		t.Fatal(err)
	}
	s.direct = d
	t.Cleanup(func() { d.Close(); s.closeAll() })
	// Inject a loopback listener so this acceptance test never exposes a port on LAN.
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d.listen = func(string, string) (net.Listener, error) { return l, nil }
	if err := d.configure(DirectConfig{Port: l.Addr().(*net.TCPAddr).Port}, false); err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: "test-user", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	c := DirectConfig{Port: d.config.Port, Public: []Endpoint{{"home.example.com", 4433}}}
	if err := d.configure(c, true); err != nil {
		t.Fatal(err)
	}
	if endpoints := d.endpoints(); len(endpoints) == 0 || endpoints[0] != c.Public[0] {
		t.Fatal("public domain missing")
	}
	bad := c
	bad.Port = 0
	if err := d.configure(bad, true); err == nil {
		t.Fatal("bad port accepted")
	}
	data, err := os.ReadFile(filepath.Join(d.dir, "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted DirectConfig
	if json.Unmarshal(data, &persisted) != nil || persisted.Port != c.Port {
		t.Fatal("failed update changed persisted config")
	}
	// Inject a bind failure without opening a wildcard port on the user's LAN.
	d.listen = func(string, string) (net.Listener, error) { return nil, os.ErrPermission }
	conflict := c
	conflict.Port = c.Port%65535 + 1
	if err := d.configure(conflict, true); err == nil {
		t.Fatal("bind failure was ignored")
	}
	if d.listener != l || d.config.Port != c.Port {
		t.Fatal("bind failure discarded the existing listener")
	}
	c.Disabled = true
	if err := d.configure(c, true); err != nil {
		t.Fatal(err)
	}
	if len(d.endpoints()) != 0 {
		t.Fatal("disabled listener still advertised")
	}
	if conn, err := net.DialTimeout("tcp", l.Addr().String(), time.Second); err == nil {
		conn.Close()
		t.Fatal("disabled listener accepts new TCP connections")
	}
	session, err := client.NewSession()
	if err != nil {
		t.Fatal("disabled listener closed an active session", err)
	}
	defer session.Close()
	if _, err := session.Output("kagero-host:info:v1"); err != nil {
		t.Fatal("existing authenticated connection no longer works", err)
	}
	restored, err := newDirectListener(d.dir, s.handle)
	if err != nil || !restored.config.Disabled || restored.config.Public[0] != c.Public[0] {
		t.Fatal("config did not survive restart", err)
	}
}

func TestCollectMultipleInterfaceAddresses(t *testing.T) {
	endpoints := collectEndpoints([]Endpoint{{"home.example.com", 2223}}, []string{"192.168.1.2", "192.168.1.2", "10.0.0.2"}, []string{"2001:db8::1", "2001:db8::2"}, 2223)
	if len(endpoints) != 5 {
		t.Fatalf("missing interface or duplicate: %+v", endpoints)
	}
	found := map[string]bool{}
	for _, e := range endpoints {
		found[e.Host] = true
	}
	if !found["10.0.0.2"] || !found["2001:db8::2"] {
		t.Fatal("dropped secondary interface")
	}
}

func TestPublicUDPEndpointsPreserveMappingAndRejectPrivateHints(t *testing.T) {
	got := publicUDPEndpoints([]string{"192.168.1.104:2223", "100.64.1.2:40000", "127.0.0.1:20000", "[fd00::2]:12345", "[fe80::2]:12345", "home.example.com:2223", "203.0.113.9:38238", "203.0.113.9:38238", "[2001:db8::123]:42600"})
	if len(got) != 2 || got[0] != (Endpoint{"203.0.113.9", 38238}) || got[1] != (Endpoint{"2001:db8::123", 42600}) {
		t.Fatalf("public UDP mapping lost or confused with TCP hints: %+v", got)
	}
	s := &Server{}
	if s.publicUDP() != nil {
		t.Fatal("unstarted tunnel advertised public mappings")
	}
}
