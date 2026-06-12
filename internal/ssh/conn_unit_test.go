// internal/ssh/conn_unit_test.go
package ssh_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
	gossh "golang.org/x/crypto/ssh"
)

// writeTestKey generates an ECDSA key and writes it to home/.ssh/id_ecdsa.
func writeTestKey(t *testing.T, home string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ecdsa"), data, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

// startPasswordOnlyServer starts an in-process SSH server that accepts the
// handshake but only allows password authentication (always rejected).
// Returns the server's listen address.
func startPasswordOnlyServer(t *testing.T) string {
	t.Helper()
	hostKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("host key: %v", err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	cfg := &gossh.ServerConfig{
		PasswordCallback: func(_ gossh.ConnMetadata, _ []byte) (*gossh.Permissions, error) {
			return nil, errors.New("wrong password")
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				gossh.NewServerConn(c, cfg) //nolint — error expected; client has no password
				c.Close()
			}(c)
		}
	}()
	return ln.Addr().String()
}

// startAcceptingPasswordServer starts a server that accepts the correct password.
func startAcceptingPasswordServer(t *testing.T, wantPassword string) string {
	t.Helper()
	hostKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("host key: %v", err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	cfg := &gossh.ServerConfig{
		PasswordCallback: func(_ gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
			if string(pw) == wantPassword {
				return &gossh.Permissions{}, nil
			}
			return nil, errors.New("wrong password")
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sc, chans, reqs, err := gossh.NewServerConn(c, cfg)
				if err != nil {
					c.Close()
					return
				}
				go gossh.DiscardRequests(reqs)
				go func() {
					for ch := range chans {
						ch.Reject(gossh.UnknownChannelType, "not supported")
					}
				}()
				sc.Wait() //nolint
			}(c)
		}
	}()
	return ln.Addr().String()
}

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "") // disable agent
	return home
}

func targetFor(addr string) config.Target {
	host, port, _ := net.SplitHostPort(addr)
	return config.Target{Name: "test", Host: host, User: "user", Port: port, Dir: "/tmp"}
}

func shortTimeout(t *testing.T) {
	t.Helper()
	old := regssh.DialTimeout
	regssh.DialTimeout = 3 * time.Second
	t.Cleanup(func() { regssh.DialTimeout = old })
}

// TestDial_passwordAuth_succeeds: server only accepts password; target has
// matching password → Dial succeeds.
func TestDial_passwordAuth_succeeds(t *testing.T) {
	shortTimeout(t)
	addr := startAcceptingPasswordServer(t, "s3cr3t")
	home := isolatedHome(t)
	writeTestKey(t, home) // key will be rejected; password fallback wins

	tgt := targetFor(addr)
	tgt.Password = "s3cr3t"

	conn, err := regssh.Dial(tgt)
	if err != nil {
		t.Fatalf("expected Dial to succeed with correct password, got: %v", err)
	}
	conn.Close()
}

// TestDial_passwordAuth_wrongPassword: server only accepts password; target
// has wrong password → Dial fails with a clear error mentioning both were rejected.
func TestDial_passwordAuth_wrongPassword(t *testing.T) {
	shortTimeout(t)
	addr := startPasswordOnlyServer(t) // always rejects
	home := isolatedHome(t)
	writeTestKey(t, home)

	tgt := targetFor(addr)
	tgt.Password = "wrongpassword"

	start := time.Now()
	_, err := regssh.Dial(tgt)
	if err == nil {
		t.Fatal("expected Dial to fail with wrong password")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("Dial hung for %v — should have failed quickly", time.Since(start))
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("expected error to mention rejection, got: %v", err)
	}
}

// TestDial_passwordAuth_noPassword: server only accepts password; no password
// configured → Dial fails fast with a hint to set password: ${APP_PASSWORD}.
func TestDial_passwordAuth_noPassword(t *testing.T) {
	shortTimeout(t)
	addr := startPasswordOnlyServer(t)
	home := isolatedHome(t)
	writeTestKey(t, home)

	tgt := targetFor(addr) // no Password set

	start := time.Now()
	_, err := regssh.Dial(tgt)
	if err == nil {
		t.Fatal("expected Dial to fail for password-only server with no password configured")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("Dial hung for %v — should have failed quickly", time.Since(start))
	}
	if !strings.Contains(err.Error(), "APP_PASSWORD") {
		t.Errorf("expected error to mention APP_PASSWORD, got: %v", err)
	}
}

// TestExpandHome_resolvesTilde: known home, ~/path → absolute path.
func TestExpandHome_resolvesTilde(t *testing.T) {
	c := regssh.ConnWithHome("/home/uid373087")
	got, err := c.ExpandHome("~/sites/app/config.php")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/home/uid373087/sites/app/config.php"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestExpandHome_emptyHome_tildeReturnsError: home unknown, ~/path → clear error
// that tells the user to use an absolute path in app_dir.
func TestExpandHome_emptyHome_tildeReturnsError(t *testing.T) {
	c := regssh.ConnWithHome("") // simulate $HOME detection failure

	_, err := c.ExpandHome("~/sites/app/config.php")
	if err == nil {
		t.Fatal("expected error when home is empty and path uses ~")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("error should mention absolute path, got: %v", err)
	}

	// The error must propagate through UploadBytes and Download before any SSH calls.
	if err := c.UploadBytes([]byte("data"), "~/sites/app/config.php", 0644, false); err == nil {
		t.Fatal("UploadBytes: expected error when home is empty and path uses ~")
	} else if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("UploadBytes error should mention absolute path, got: %v", err)
	}

	if _, err := c.Download("~/sites/app/config.php"); err == nil {
		t.Fatal("Download: expected error when home is empty and path uses ~")
	} else if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("Download error should mention absolute path, got: %v", err)
	}
}

// TestExpandHome_absolutePath: absolute path passes through unchanged, even with empty home.
func TestExpandHome_absolutePath(t *testing.T) {
	c := regssh.ConnWithHome("")
	got, err := c.ExpandHome("/srv/app/config.php")
	if err != nil {
		t.Fatalf("unexpected error for absolute path: %v", err)
	}
	if got != "/srv/app/config.php" {
		t.Errorf("got %q, want %q", got, "/srv/app/config.php")
	}
}

// TestExpandHome_noTilde: path without ~ passes through unchanged.
func TestExpandHome_noTilde(t *testing.T) {
	c := regssh.ConnWithHome("/home/user")
	got, err := c.ExpandHome("relative/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "relative/path" {
		t.Errorf("got %q, want %q", got, "relative/path")
	}
}

// TestDial_unresponsiveServer: server accepts TCP but never sends SSH banner
// → Dial times out instead of hanging forever.
func TestDial_unresponsiveServer(t *testing.T) {
	old := regssh.DialTimeout
	regssh.DialTimeout = 400 * time.Millisecond
	t.Cleanup(func() { regssh.DialTimeout = old })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		if c != nil {
			time.Sleep(10 * time.Second) // simulate hang
			c.Close()
		}
	}()

	home := isolatedHome(t)
	writeTestKey(t, home)

	tgt := targetFor(ln.Addr().String())

	start := time.Now()
	_, err = regssh.Dial(tgt)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected Dial to fail for unresponsive server")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Dial hung for %v — should have timed out in ~400ms", elapsed)
	}
}
