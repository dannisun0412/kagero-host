package host

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"tailscale.com/types/key"
)

func testPairing(t *testing.T) (*Pairing, Invitation, PairRequest, ssh.Signer) {
	t.Helper()
	s := &Store{Dir: t.TempDir(), State: State{ID: "195907fc-aebd-407b-b00a-522788cc307b", Name: "Test Mac", Devices: []Device{}}}
	p := &Pairing{store: s, now: func() time.Time { return time.Unix(1800000000, 0) }}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	i, err := p.New("tc-test", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))))
	if err != nil {
		t.Fatal(err)
	}
	r := PairRequest{Version: 1, Name: "iPhone", PublicKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey())), NodeKey: key.NewNode().Public().String()}
	return p, i, r, signer
}
func TestPairOnceRetryAndRevoke(t *testing.T) {
	p, i, r, signer := testPairing(t)
	reply, err := p.Complete(i.Token, r, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.store.authorize(signer.PublicKey()); !ok {
		t.Fatal("key not authorized")
	}
	for range 3 {
		again, err := p.Complete(i.Token, r, "fixture")
		if err != nil || !reflect.DeepEqual(again, reply) {
			t.Fatal("lost reply not idempotent", err)
		}
	}
	_, _, other, _ := testPairing(t)
	if _, err := p.Complete(i.Token, other, "fixture"); err == nil {
		t.Fatal("used token allowed another key")
	}
	data, err := os.ReadFile(filepath.Join(p.store.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), i.Token) || strings.Contains(string(data), "PRIVATE") {
		t.Fatal("secret leaked into state")
	}
	var state State
	if json.Unmarshal(data, &state) != nil || len(state.Devices) != 1 {
		t.Fatal("invalid saved state")
	}
	if err := p.store.Revoke(reply.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.store.authorize(signer.PublicKey()); ok {
		t.Fatal("revoked key authorized")
	}
}
func TestExpiredRenewedAndMalformedPairing(t *testing.T) {
	p, i, r, _ := testPairing(t)
	if _, err := p.Complete(strings.Repeat("a", 43), r, "fixture"); err == nil {
		t.Fatal("wrong token accepted")
	}
	p.now = func() time.Time { return time.Unix(i.ExpiresAt, 0) }
	if p.Accepts(i.Token) {
		t.Fatal("expired token accepted")
	}
	if _, err := p.Complete(i.Token, r, "fixture"); err == nil {
		t.Fatal("expired pairing accepted")
	}
	new, err := p.New("tc-test", i.HostKey)
	if err != nil {
		t.Fatal(err)
	}
	if p.Accepts(i.Token) {
		t.Fatal("previous QR still accepted")
	}
	bad := r
	bad.PublicKey = "command=\"id\" " + r.PublicKey
	if _, err := p.Complete(new.Token, bad, "fixture"); err == nil {
		t.Fatal("authorized key options accepted")
	}
	bad = r
	bad.Name = "phone\nmalicious"
	if _, err := p.Complete(new.Token, bad, "fixture"); err == nil {
		t.Fatal("control character accepted")
	}
	bad = r
	bad.NodeKey = "not-a-key"
	if _, err := p.Complete(new.Token, bad, "fixture"); err == nil {
		t.Fatal("invalid node key accepted")
	}
	if _, err := p.Complete(new.Token, r, "fixture"); err != nil {
		t.Fatal("bad request consumed QR", err)
	}
}
func TestConcurrentEnrollmentHasOneWinner(t *testing.T) {
	p, i, _, _ := testPairing(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for range 12 {
		_, _, r, _ := testPairing(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Complete(i.Token, r, "fixture")
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 || len(p.store.Devices()) != 1 {
		t.Fatal("expected exactly one enrolled device", success)
	}
}
func TestFailedPersistenceDoesNotConsumeToken(t *testing.T) {
	p, i, r, _ := testPairing(t)
	p.store.Dir = filepath.Join(p.store.Dir, "missing")
	if _, err := p.Complete(i.Token, r, "fixture"); err == nil {
		t.Fatal("missing directory saved successfully")
	}
	if p.consumedKey != "" || len(p.store.Devices()) != 0 {
		t.Fatal("failed save consumed token or changed authorization")
	}
}
