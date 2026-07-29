package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
	xknownhosts "golang.org/x/crypto/ssh/knownhosts"
)

// tunnelParams carries the connection definition for a single SSH forward.
// None of these values may appear in logs or UI errors.
type TunnelParams struct {
	Host       string
	Port       int
	Username   string
	Password   string
	AuthMethod string
	PrivateKey string
	Passphrase string
	DBHost     string
	DBPort     int
}

// SSHTunnel is a loopback-only local forward through an SSH bastion. Host
// keys are verified strictly against the user's known_hosts; unknown hosts
// are rejected, never auto-accepted.
type SSHTunnel struct {
	params TunnelParams

	// knownHostsPath is injectable for tests; defaults to ~/.ssh/known_hosts
	// (and known_hosts2 when present).
	knownHostsPath string

	mu       sync.Mutex
	listener net.Listener
	client   *ssh.Client
	cancel   context.CancelFunc
	closed   bool
}

func NewSSHTunnel() *SSHTunnel {
	return &SSHTunnel{}
}

// Configure binds the connection parameters to the tunnel before Start.
func (t *SSHTunnel) Configure(p TunnelParams) {
	t.params = p
}

// Start connects to the bastion, verifies the host key strictly and begins
// accepting loopback connections to forward toward dbHost:dbPort.
func (t *SSHTunnel) Start(ctx context.Context) error {
	p := t.params
	if p.Host == "" {
		return errors.New("SSH tunnel is enabled but no bastion host is configured")
	}
	if p.Username == "" {
		return errors.New("SSH tunnel is enabled but no SSH username is configured")
	}
	port := p.Port
	if port == 0 {
		port = 22
	}
	dbPort := p.DBPort
	if dbPort == 0 {
		dbPort = 3306
	}

	auth, err := t.authMethods()
	if err != nil {
		return err
	}

	callback, err := t.hostKeyCallback()
	if err != nil {
		return err
	}

	config := &ssh.ClientConfig{
		User:            p.Username,
		Auth:            auth,
		HostKeyCallback: callback,
		Timeout:         15 * time.Second,
	}

	address := net.JoinHostPort(p.Host, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return fmt.Errorf("connect to SSH bastion %s: %w", address, knownhostsHint(err))
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return fmt.Errorf("reserve local tunnel port: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.client = client
	t.listener = listener
	t.cancel = cancel
	t.closed = false
	t.mu.Unlock()

	go t.serve(runCtx, client, listener, net.JoinHostPort(p.DBHost, strconv.Itoa(dbPort)))
	return nil
}

// Addr returns the loopback address the tunnel listens on.
func (t *SSHTunnel) Addr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.listener == nil {
		return ""
	}
	return t.listener.Addr().String()
}

// Close stops accepting new connections and tears down the SSH client.
func (t *SSHTunnel) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	cancel := t.cancel
	listener := t.listener
	client := t.client
	t.cancel = nil
	t.listener = nil
	t.client = nil
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		listener.Close()
	}
	if client != nil {
		return client.Close()
	}
	return nil
}

func (t *SSHTunnel) serve(ctx context.Context, client *ssh.Client, listener net.Listener, remoteAddr string) {
	var wg sync.WaitGroup
	defer wg.Wait()

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.forward(client, conn, remoteAddr)
		}()
	}
}

func (t *SSHTunnel) forward(client *ssh.Client, local net.Conn, remoteAddr string) {
	remote, err := client.Dial("tcp", remoteAddr)
	if err != nil {
		local.Close()
		return
	}
	var once sync.Once
	kill := func() {
		local.Close()
		remote.Close()
	}
	go func() {
		io.Copy(remote, local)
		once.Do(kill)
	}()
	go func() {
		io.Copy(local, remote)
		once.Do(kill)
	}()
}

// authMethods builds SSH auth from the connection definition without ever
// incorporating key material into error values.
func (t *SSHTunnel) authMethods() ([]ssh.AuthMethod, error) {
	switch t.params.AuthMethod {
	case "publicKey":
		path := t.params.PrivateKey
		if path == "" {
			return nil, errors.New("public-key authentication selected but no private key file is configured")
		}
		if strings.HasPrefix(path, "~") {
			path = filepath.Join(xdg.Home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), string(filepath.Separator)))
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read SSH private key: %w", pathErr(err))
		}
		var signer ssh.Signer
		if t.params.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(t.params.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, errors.New("parse SSH private key: invalid key material or passphrase")
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case "password", "":
		if t.params.Password == "" {
			return nil, errors.New("password authentication selected but no SSH password is configured")
		}
		return []ssh.AuthMethod{ssh.Password(t.params.Password)}, nil
	default:
		return nil, fmt.Errorf("unsupported SSH auth method: %s", t.params.AuthMethod)
	}
}

// hostKeyCallback builds a strict known_hosts callback. When no known_hosts
// file exists the tunnel refuses to start rather than falling back to
// trust-on-first-use.
func (t *SSHTunnel) hostKeyCallback() (ssh.HostKeyCallback, error) {
	var files []string
	if t.knownHostsPath != "" {
		files = []string{t.knownHostsPath}
	} else {
		for _, name := range []string{"known_hosts", "known_hosts2"} {
			candidate := filepath.Join(xdg.Home, ".ssh", name)
			if _, err := os.Stat(candidate); err == nil {
				files = append(files, candidate)
			}
		}
	}
	if len(files) == 0 {
		return nil, errors.New("no ~/.ssh/known_hosts file found: add the bastion's host key first (for example with `ssh-keyscan -H <host> >> ~/.ssh/known_hosts` or by connecting once with the OpenSSH client)")
	}
	callback, err := knownhosts.NewDB(files...)
	if err != nil {
		return nil, fmt.Errorf("load SSH known_hosts: %w", err)
	}
	return callback.HostKeyCallback(), nil
}

// knownhostsHint amends host-key verification failures with actionable
// guidance while keeping strict failure behavior.
func knownhostsHint(err error) error {
	var keyErr *xknownhosts.KeyError
	if errors.As(err, &keyErr) {
		if len(keyErr.Want) == 0 {
			return fmt.Errorf("%w: the bastion's host key is not present in known_hosts; add it explicitly (for example with `ssh-keyscan -H <host> >> ~/.ssh/known_hosts`) — unknown hosts are never auto-accepted", err)
		}
		return fmt.Errorf("%w: the bastion's host key CHANGED compared to known_hosts; refusing to connect (possible MITM)", err)
	}
	return err
}

func pathErr(err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return fmt.Errorf("%s: %s", pathError.Op, pathError.Err)
	}
	return err
}
