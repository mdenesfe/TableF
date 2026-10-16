// Package tunnel forwards a local TCP port to a database behind an SSH server.
package tunnel

import (
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

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/mdenesfe/TableF/internal/driver"
)

// Timeouts; variables so tests can shorten them.
var (
	// dialTimeout bounds the TCP connect to the SSH server.
	dialTimeout = 10 * time.Second
	// handshakeTimeout bounds the SSH handshake and authentication, so a
	// server that accepts TCP but never answers can't hang a connect.
	handshakeTimeout = 15 * time.Second
	// keepaliveInterval is how often an idle tunnel checks the SSH server is
	// still there; a failed check closes the tunnel.
	keepaliveInterval = 30 * time.Second
	keepaliveTimeout  = 15 * time.Second
)

// UnknownHostError is returned when the SSH server's host key is in neither
// ~/.ssh/known_hosts nor the app's known_hosts file. The caller can show the
// fingerprint to the user and, once confirmed, record it with TrustHostKey.
type UnknownHostError struct {
	Host        string // host:port as dialed
	Fingerprint string // SHA256:… as printed by ssh-keygen -l
	KeyType     string // e.g. ssh-ed25519
	Line        string // the known_hosts line that would trust this key
}

// Error reads "unknown SSH host key for <host:port> (<key type> <fingerprint>)".
func (e *UnknownHostError) Error() string {
	return fmt.Sprintf("unknown SSH host key for %s (%s %s)", e.Host, e.KeyType, e.Fingerprint)
}

// Tunnel listens on 127.0.0.1 and forwards every connection through SSH.
type Tunnel struct {
	client *ssh.Client
	ln     net.Listener
	once   sync.Once
	done   chan struct{}
	// Port is the local port the database driver should connect to.
	Port int
}

// Open dials the SSH server described by cfg and forwards to cfg.Host:cfg.Port.
// Host keys are checked against ~/.ssh/known_hosts and appKnownHosts: an
// unknown key fails with *UnknownHostError (see TrustHostKey) and a key that
// conflicts with a recorded one is always rejected.
func Open(cfg driver.Config, appKnownHosts string) (*Tunnel, error) {
	addr, err := sshAddr(cfg)
	if err != nil {
		return nil, err
	}
	auth, cleanup, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	// The agent is only needed while authenticating.
	defer cleanup()
	user := cfg.SSHUser
	if user == "" {
		user = os.Getenv("USER")
	}
	var unknown *UnknownHostError
	check := hostKeyCallback(appKnownHosts)
	client, err := dial(addr, &ssh.ClientConfig{
		User: user,
		Auth: auth,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			err := check(hostname, remote, key)
			errors.As(err, &unknown)
			return err
		},
	})
	if unknown != nil {
		// Returned unwrapped so its message is exactly what the UI expects.
		return nil, unknown
	}
	if err != nil {
		return nil, fmt.Errorf("ssh: %w", err)
	}

	dbPort := cfg.Port
	if dbPort == 0 {
		dbPort = driver.DefaultPort(cfg.Driver)
	}
	dbHost := cfg.Host
	if dbHost == "" {
		dbHost = "127.0.0.1"
	}
	remote := net.JoinHostPort(dbHost, strconv.Itoa(dbPort))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return nil, err
	}
	t := &Tunnel{client: client, ln: ln, done: make(chan struct{}), Port: ln.Addr().(*net.TCPAddr).Port}
	go t.serve(remote)
	go t.keepalive()
	return t, nil
}

func sshAddr(cfg driver.Config) (string, error) {
	if cfg.SSHHost == "" {
		return "", errors.New("ssh: host is required")
	}
	port := cfg.SSHPort
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(cfg.SSHHost, strconv.Itoa(port)), nil
}

// dial connects and runs the SSH handshake under a deadline; ssh.Dial's own
// Timeout only covers the TCP connect.
func dial(addr string, conf *ssh.ClientConfig) (*ssh.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, conf)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

func (t *Tunnel) serve(remote string) {
	for {
		local, err := t.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			upstream, err := t.client.Dial("tcp", remote)
			if err != nil {
				local.Close()
				return
			}
			go pipe(local, upstream)
			pipe(upstream, local)
		}()
	}
}

// keepalive pings the server with keepalive@openssh.com (which any server
// answers, if only with a failure reply) and closes the tunnel when it stops
// answering, so a dead link fails fast instead of hanging queries.
func (t *Tunnel) keepalive() {
	tick := time.NewTicker(keepaliveInterval)
	defer tick.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-tick.C:
		}
		res := make(chan error, 1)
		go func() {
			_, _, err := t.client.SendRequest("keepalive@openssh.com", true, nil)
			res <- err
		}()
		select {
		case <-t.done:
			return
		case err := <-res:
			if err != nil {
				t.Close()
				return
			}
		case <-time.After(keepaliveTimeout):
			t.Close()
			return
		}
	}
}

func pipe(dst, src net.Conn) {
	defer dst.Close()
	defer src.Close()
	_, _ = io.Copy(dst, src)
}

// Close stops listening and closes the SSH connection.
func (t *Tunnel) Close() {
	t.once.Do(func() {
		close(t.done)
		t.ln.Close()
		t.client.Close()
	})
}

// TrustHostKey connects to the SSH server of cfg only to read its host key
// (no authentication happens), appends the key to appKnownHosts and returns
// its SHA256 fingerprint. A host whose recorded key differs is refused, as
// in Open; an already trusted key is not recorded twice.
func TrustHostKey(cfg driver.Config, appKnownHosts string) (string, error) {
	addr, err := sshAddr(cfg)
	if err != nil {
		return "", err
	}
	check := hostKeyCallback(appKnownHosts)
	var (
		fingerprint string
		line        string
		checkErr    error
	)
	errGotKey := errors.New("host key read")
	_, err = dial(addr, &ssh.ClientConfig{
		User: "tablef",
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			fingerprint = ssh.FingerprintSHA256(key)
			var unknown *UnknownHostError
			switch err := check(hostname, remote, key); {
			case err == nil: // already trusted
			case errors.As(err, &unknown):
				line = unknown.Line
			default:
				checkErr = err
			}
			// Stop here: the key is all this call needs.
			return errGotKey
		},
	})
	if checkErr != nil {
		return "", checkErr
	}
	if fingerprint == "" {
		return "", fmt.Errorf("ssh: %w", err)
	}
	if line != "" {
		if err := appendKnownHost(appKnownHosts, line); err != nil {
			return "", err
		}
	}
	return fingerprint, nil
}

func appendKnownHost(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// authMethods returns the SSH auth methods for cfg and a cleanup func that
// releases what they hold (the agent socket) once authentication is done.
func authMethods(cfg driver.Config) ([]ssh.AuthMethod, func(), error) {
	noop := func() {}
	switch cfg.SSHAuth {
	case "password":
		pw := cfg.SSHPassword
		return []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				answers := make([]string, len(qs))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}, noop, nil
	case "key":
		signer, err := loadKey(cfg.SSHKeyFile, cfg.SSHPassphrase)
		if err != nil {
			return nil, noop, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, noop, nil
	default: // agent, falling back to the default identity files
		var methods []ssh.AuthMethod
		cleanup := noop
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			if conn, err := net.Dial("unix", sock); err == nil {
				methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
				cleanup = func() { conn.Close() }
			}
		}
		var signers []ssh.Signer
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			if s, err := loadKey("~/.ssh/"+name, ""); err == nil {
				signers = append(signers, s)
			}
		}
		if len(signers) > 0 {
			methods = append(methods, ssh.PublicKeys(signers...))
		}
		if len(methods) == 0 {
			return nil, noop, errors.New("ssh: no agent (SSH_AUTH_SOCK) and no key in ~/.ssh")
		}
		return methods, cleanup, nil
	}
}

func loadKey(path, passphrase string) (ssh.Signer, error) {
	path = expandHome(path)
	if path == "" {
		return nil, errors.New("ssh: private key file is required")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ssh: %w", err)
	}
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
	}
	signer, err := ssh.ParsePrivateKey(pem)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return nil, errors.New("ssh: private key is encrypted, enter its passphrase")
	}
	return signer, err
}

// hostKeyCallback checks keys against ~/.ssh/known_hosts and appKnownHosts.
// It never records anything: an unknown key yields *UnknownHostError.
func hostKeyCallback(appKnownHosts string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		var files []string
		for _, f := range []string{expandHome("~/.ssh/known_hosts"), appKnownHosts} {
			if _, err := os.Stat(f); err == nil {
				files = append(files, f)
			}
		}
		if len(files) > 0 {
			check, err := knownhosts.New(files...)
			if err != nil {
				return err
			}
			err = check(hostname, remote, key)
			var keyErr *knownhosts.KeyError
			switch {
			case err == nil:
				return nil
			case errors.As(err, &keyErr) && len(keyErr.Want) > 0:
				return fmt.Errorf("ssh: host key for %s changed, refusing to connect", hostname)
			case !errors.As(err, &keyErr):
				return err
			}
		}
		return &UnknownHostError{
			Host:        hostname,
			Fingerprint: ssh.FingerprintSHA256(key),
			KeyType:     key.Type(),
			Line:        knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key),
		}
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
