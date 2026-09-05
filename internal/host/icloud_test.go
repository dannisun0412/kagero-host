package host

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudInvitationBoundAndIndependent(t *testing.T) {
	p, qr, request, signer := testPairing(t)
	s := &Server{store: p.store, pair: p, signer: signer, address: qr.Address, username: "test-user", active: map[net.Conn]string{}, seats: make(chan struct{}, 32)}
	hostKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	req := CloudInviteRequest{ID: "12345678-1234-1234-1234-123456789abc", HostID: strings.ToUpper(p.store.State.ID), HostKey: hostKey, PublicKey: request.PublicKey, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	cloud, err := s.cloudInvitation(req)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Accepts(qr.Token) || cloud.Token == qr.Token {
		t.Fatal("cloud replaced QR")
	}
	again, err := s.cloudInvitation(req)
	if err != nil || again.Token != cloud.Token {
		t.Fatal("retry changed grant")
	}
	_, _, otherRequest, _ := testPairing(t)
	if _, err := s.pairingFor(cloud.Token).Complete(cloud.Token, otherRequest, "test-user"); err == nil {
		t.Fatal("another phone consumed cloud grant")
	}
	changed := req
	changed.PublicKey = otherRequest.PublicKey
	if _, err := s.cloudInvitation(changed); err == nil {
		t.Fatal("request identity replacement accepted")
	}
	wrongHost := req
	wrongHost.HostKey = otherRequest.PublicKey
	if _, err := s.cloudInvitation(wrongHost); err == nil {
		t.Fatal("changed host accepted")
	}
	expired := req
	expired.ExpiresAt = time.Now().Add(-time.Second).Unix()
	if _, err := s.cloudInvitation(expired); err == nil {
		t.Fatal("expired request accepted")
	}
	future := req
	future.ExpiresAt = time.Now().Add(time.Hour).Unix()
	if _, err := s.cloudInvitation(future); err == nil {
		t.Fatal("unbounded request accepted")
	}

	// Exercise the actual SSH entry point: the cloud credential cannot run commands.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer s.closeAll()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: "kagero-pair", Auth: []ssh.AuthMethod{ssh.Password(cloud.Token)}, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Run("echo forbidden"); err == nil {
		t.Fatal("cloud grant executed shell")
	}
	session.Close()
	data, _ := json.Marshal(request)
	session, err = client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.Output("kagero-pair-v1 " + base64.RawURLEncoding.EncodeToString(data))
	session.Close()
	var reply PairReply
	if err != nil || json.Unmarshal(output, &reply) != nil || reply.DeviceID == "" {
		t.Fatal("cloud enrollment failed", err)
	}
	if err := p.store.Revoke(reply.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pairingFor(cloud.Token).Complete(cloud.Token, request, "test-user"); err != nil {
		t.Fatal(err)
	}
	enrolledKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(request.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.store.authorize(enrolledKey); ok {
		t.Fatal("replay undid revocation")
	}
}

func TestCloudInvitationPoolBounded(t *testing.T) {
	p, qr, request, signer := testPairing(t)
	s := &Server{store: p.store, pair: p, signer: signer, address: qr.Address}
	req := CloudInviteRequest{HostID: p.store.State.ID, HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), PublicKey: request.PublicKey, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	for i := 0; i < 32; i++ {
		req.ID = fmt.Sprintf("%08x-1234-1234-1234-123456789abc", len(s.cloud.items)+1)
		if _, err := s.cloudInvitation(req); err != nil {
			t.Fatal(err)
		}
	}
	req.ID = fmt.Sprintf("%08x-1234-1234-1234-123456789abc", len(s.cloud.items)+1)
	if _, err := s.cloudInvitation(req); err == nil {
		t.Fatal("unbounded pool")
	}
	for _, p := range s.cloud.items {
		p.invitation.ExpiresAt = time.Now().Add(-time.Second).Unix()
	}
	if _, err := s.cloudInvitation(req); err != nil || len(s.cloud.items) != 1 {
		t.Fatal("expired grants retained")
	}
}

func TestCloudClearKeepsQR(t *testing.T) {
	p, qr, phone, signer := testPairing(t)
	s := &Server{store: p.store, pair: p, signer: signer, address: qr.Address}
	req := CloudInviteRequest{ID: "12345678-1234-1234-1234-123456789abc", HostID: p.store.State.ID, HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), PublicKey: phone.PublicKey, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	invitation, err := s.cloudInvitation(req)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.cloudControl(mux)
	reply := httptest.NewRecorder()
	mux.ServeHTTP(reply, httptest.NewRequest("POST", "/icloud/clear", nil))
	if reply.Code != http.StatusOK || s.pairingFor(invitation.Token) != nil {
		t.Fatal("cloud invitation survived disabling")
	}
	if s.pairingFor(qr.Token) != p {
		t.Fatal("disabling cloud removed QR pairing")
	}
}
