package runtime

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
)

// testSSHServer is a minimal in-process SSH server that accepts direct-tcpip
// channels and pipes them to a real dial of the requested address. The DB
// endpoint in tests is another local listener, so no external service is
// needed.
type testSSHServer struct {
	listener  net.Listener
	hostKey   ssh.Signer
	serverCfg *ssh.ServerConfig
	wg        sync.WaitGroup
}

func startTestSSHServer(t *testing.T, password string) *testSSHServer {
	t.Helper()

	hostKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &testSSHServer{listener: ln, hostKey: signer, serverCfg: cfg}
	go srv.acceptLoop()
	t.Cleanup(func() {
		ln.Close()
		srv.wg.Wait()
	})
	return srv
}

func (s *testSSHServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.serverCfg)
			if err != nil {
				return
			}
			defer sshConn.Close()
			go ssh.DiscardRequests(reqs)
			for ch := range chans {
				if ch.ChannelType() != "direct-tcpip" {
					ch.Reject(ssh.UnknownChannelType, "only direct-tcpip supported")
					continue
				}
				channel, reqs, err := ch.Accept()
				if err != nil {
					continue
				}
				go ssh.DiscardRequests(reqs)
				go func() {
					defer channel.Close()
					target, err := dialDirectTCPIP(ch.ExtraData())
					if err != nil {
						return
					}
					defer target.Close()
					var once sync.Once
					kill := func() {
						channel.Close()
						target.Close()
					}
					go func() { io.Copy(channel, target); once.Do(kill) }()
					io.Copy(target, channel)
					once.Do(kill)
				}()
			}
		}()
	}
}

// dialDirectTCPIP parses the direct-tcpip payload per RFC 4254 §7.2 and dials
// the requested destination on the bastion side.
func dialDirectTCPIP(payload []byte) (net.Conn, error) {
	var msg struct {
		DestAddr   string
		DestPort   uint32
		OriginAddr string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(payload, &msg); err != nil {
		return nil, fmt.Errorf("parse direct-tcpip payload: %w", err)
	}
	return net.DialTimeout("tcp", net.JoinHostPort(msg.DestAddr, fmt.Sprint(msg.DestPort)), 5*time.Second)
}

// startEchoDB starts a local TCP echo server acting as the "database".
func startEchoDB(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return ln.Addr().String(), port
}

func writeKnownHosts(t *testing.T, hostPort string, key ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{hostPort}, key)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTunnelLoopbackEndToEnd(t *testing.T) {
	_, dbPort := startEchoDB(t)
	srv := startTestSSHServer(t, "hunter2")
	host, portStr, _ := net.SplitHostPort(srv.listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	khPath := writeKnownHosts(t, net.JoinHostPort(host, portStr), srv.hostKey.PublicKey())

	tun := NewSSHTunnel()
	tun.knownHostsPath = khPath
	tun.Configure(TunnelParams{
		Host:       host,
		Port:       port,
		Username:   "tester",
		Password:   "hunter2",
		AuthMethod: "password",
		DBHost:     "127.0.0.1",
		DBPort:     dbPort,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tun.Start(ctx); err != nil {
		t.Fatalf("tunnel start: %v", err)
	}
	defer tun.Close()

	if !strings.HasPrefix(tun.Addr(), "127.0.0.1:") {
		t.Fatalf("tunnel must bind to loopback, got %q", tun.Addr())
	}

	conn, err := net.DialTimeout("tcp", tun.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()
	msg := []byte("ping-phpmyadmin")
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read through tunnel: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Fatalf("echo mismatch: got %q", buf)
	}
}

func TestTunnelRejectsUnknownHostKey(t *testing.T) {
	srv := startTestSSHServer(t, "hunter2")
	_, dbPort := startEchoDB(t)
	host, portStr, _ := net.SplitHostPort(srv.listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	// Create a second, different host key and only publish that one.
	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherKey)
	khPath := writeKnownHosts(t, net.JoinHostPort(host, portStr), otherSigner.PublicKey())

	tun := NewSSHTunnel()
	tun.knownHostsPath = khPath
	tun.Configure(TunnelParams{
		Host:       host,
		Port:       port,
		Username:   "tester",
		Password:   "hunter2",
		AuthMethod: "password",
		DBPort:     dbPort,
	})
	err := tun.Start(context.Background())
	if err == nil {
		t.Fatal("tunnel must reject an unknown/mismatched host key")
	}
	if !strings.Contains(err.Error(), "CHANGED") && !strings.Contains(err.Error(), "known_hosts") {
		t.Fatalf("expected host-key verification error, got %v", err)
	}
}

func TestTunnelRefusesWithoutKnownHosts(t *testing.T) {
	srv := startTestSSHServer(t, "hunter2")
	host, portStr, _ := net.SplitHostPort(srv.listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	tun := NewSSHTunnel()
	tun.knownHostsPath = filepath.Join(t.TempDir(), "does-not-exist")
	tun.Configure(TunnelParams{
		Host:       host,
		Port:       port,
		Username:   "tester",
		Password:   "hunter2",
		AuthMethod: "password",
	})
	err := tun.Start(context.Background())
	if err == nil {
		t.Fatal("tunnel must refuse to start without a known_hosts entry")
	}
	if !strings.Contains(err.Error(), "known_hosts") {
		t.Fatalf("expected actionable known_hosts error, got %v", err)
	}
}

func TestTunnelRejectsBadPassword(t *testing.T) {
	srv := startTestSSHServer(t, "hunter2")
	host, portStr, _ := net.SplitHostPort(srv.listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	khPath := writeKnownHosts(t, net.JoinHostPort(host, portStr), srv.hostKey.PublicKey())

	tun := NewSSHTunnel()
	tun.knownHostsPath = khPath
	tun.Configure(TunnelParams{
		Host:       host,
		Port:       port,
		Username:   "tester",
		Password:   "wrong-password",
		AuthMethod: "password",
	})
	if err := tun.Start(context.Background()); err == nil {
		t.Fatal("tunnel must fail with a bad password")
	}
}
