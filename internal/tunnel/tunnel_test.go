package tunnel

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/mdenesfe/TableF/internal/driver"
)

// startSSHServer runs a password-auth SSH server that supports direct-tcpip.
func startSSHServer(t *testing.T, password string) (host string, port int) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	conf := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if string(p) == password {
				return nil, nil
			}
			return nil, io.EOF
		},
	}
	conf.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, conf)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "direct-tcpip" {
						nc.Reject(ssh.UnknownChannelType, "")
						continue
					}
					// payload: host string, port uint32, origin host, origin port
					p := nc.ExtraData()
					n := binary.BigEndian.Uint32(p)
					target := net.JoinHostPort(string(p[4:4+n]), strconv.Itoa(int(binary.BigEndian.Uint32(p[4+n:]))))
					up, err := net.Dial("tcp", target)
					if err != nil {
						nc.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					ch, creqs, _ := nc.Accept()
					go ssh.DiscardRequests(creqs)
					go func() { io.Copy(ch, up); ch.Close() }()
					go func() { io.Copy(up, ch); up.Close() }()
				}
			}()
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func startEcho(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestTunnelForwards(t *testing.T) {
	sshHost, sshPort := startSSHServer(t, "secret")
	echoPort := startEcho(t)
	known := filepath.Join(t.TempDir(), "known_hosts")

	cfg := driver.Config{
		Driver: "postgres", Host: "127.0.0.1", Port: echoPort,
		SSHEnabled: true, SSHHost: sshHost, SSHPort: sshPort, SSHUser: "dev", SSHAuth: "password", SSHPassword: "secret",
	}
	// An unknown host key is never trusted silently.
	_, err := Open(cfg, known)
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *UnknownHostError, got %v", err)
	}
	want := fmt.Sprintf("unknown SSH host key for %s (ssh-ed25519 %s)", net.JoinHostPort(sshHost, strconv.Itoa(sshPort)), unknown.Fingerprint)
	if err.Error() != want || !strings.HasPrefix(unknown.Fingerprint, "SHA256:") {
		t.Fatalf("message %q, want %q", err.Error(), want)
	}
	if _, err := os.Stat(known); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("known_hosts written before the user trusted the key: %v", err)
	}

	fp, err := TrustHostKey(cfg, known)
	if err != nil || fp != unknown.Fingerprint {
		t.Fatalf("TrustHostKey = %q, %v", fp, err)
	}
	// Trusting again doesn't duplicate the line.
	if _, err := TrustHostKey(cfg, known); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(known); strings.Count(string(b), "\n") != 1 {
		t.Fatalf("known_hosts:\n%s", b)
	}

	tun, err := Open(cfg, known)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()

	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(tun.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("got %q, %v", line, err)
	}

	// Host key was recorded; a second connection must still verify against it.
	tun2, err := Open(cfg, known)
	if err != nil {
		t.Fatalf("reconnect with recorded host key: %v", err)
	}
	tun2.Close()
}

func TestTunnelChangedHostKey(t *testing.T) {
	sshHost, sshPort := startSSHServer(t, "secret")
	known := filepath.Join(t.TempDir(), "known_hosts")
	cfg := driver.Config{
		Driver: "postgres", Port: 5432,
		SSHEnabled: true, SSHHost: sshHost, SSHPort: sshPort, SSHUser: "dev", SSHAuth: "password", SSHPassword: "secret",
	}
	// Record some other key for this host.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(other)
	host := knownhosts.Normalize(net.JoinHostPort(sshHost, strconv.Itoa(sshPort)))
	if err := os.WriteFile(known, []byte(knownhosts.Line([]string{host}, signer.PublicKey())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(cfg, known); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("Open: want changed-key error, got %v", err)
	}
	if _, err := TrustHostKey(cfg, known); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("TrustHostKey: want changed-key error, got %v", err)
	}
}

func TestHandshakeTimeout(t *testing.T) {
	old := handshakeTimeout
	handshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { handshakeTimeout = old })
	// Accepts TCP but never speaks SSH.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		<-done
		c.Close()
	}()
	a := ln.Addr().(*net.TCPAddr)
	cfg := driver.Config{Driver: "postgres", SSHEnabled: true, SSHHost: "127.0.0.1", SSHPort: a.Port, SSHAuth: "password"}
	start := time.Now()
	if _, err := Open(cfg, filepath.Join(t.TempDir(), "kh")); err == nil {
		t.Fatal("expected a timeout")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("handshake took %v", d)
	}
}

func TestTunnelWrongPassword(t *testing.T) {
	sshHost, sshPort := startSSHServer(t, "secret")
	cfg := driver.Config{
		Driver: "postgres", Port: 5432,
		SSHEnabled: true, SSHHost: sshHost, SSHPort: sshPort, SSHUser: "dev", SSHAuth: "password", SSHPassword: "nope",
	}
	known := filepath.Join(t.TempDir(), "kh")
	if _, err := TrustHostKey(cfg, known); err != nil {
		t.Fatal(err)
	}
	_, err := Open(cfg, known)
	if err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("expected auth failure, got %v", err)
	}
}
