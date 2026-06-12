// internal/ssh/conn.go
package ssh

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// DialTimeout bounds the entire SSH handshake (TCP connect + key exchange +
// authentication). Without this, servers that request password auth can cause
// an indefinite hang because golang.org/x/crypto/ssh.Dial only applies
// ClientConfig.Timeout to the TCP dial, not the auth phase.
var DialTimeout = 30 * time.Second

// Conn wraps an active SSH connection to one target.
// Transplanted from jelastic-gateway/cmd/gateway-config/sshconn.go.
type Conn struct {
	client  *gossh.Client
	Target  config.Target
	pathSep string // "/" for Unix, `\` for Windows — detected once after Dial
	home    string // remote $HOME — used to expand ~ in SFTP paths (SFTP does not expand ~)
}

// Dial opens ONE TCP connection. All Run/Upload/Download calls reuse it.
// Auth order: Windows OpenSSH agent (named pipe) → Unix SSH agent ($SSH_AUTH_SOCK)
// → $HOME/.ssh/id_{ed25519,rsa,ecdsa,dsa} → target.password (if set).
func Dial(t config.Target) (*Conn, error) {
	port := 22
	if t.Port != "" {
		if n, err := strconv.Atoi(t.Port); err == nil {
			port = n
		}
	}
	methods, diag := collectAuthMethods(t)
	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH auth for %s@%s:%d — %s", t.User, t.Host, port, diag)
	}
	cfg := &gossh.ClientConfig{
		User:            t.User,
		Auth:            methods,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), // pragmatic for managed infra
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(port))

	// Dial TCP and set a deadline covering the full SSH handshake + auth.
	// gossh.Dial only applies ClientConfig.Timeout to the TCP connect, leaving
	// the auth phase unbounded — a server prompting for password hangs forever.
	netConn, err := net.DialTimeout("tcp", addr, DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	if err := netConn.SetDeadline(time.Now().Add(DialTimeout)); err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := gossh.NewClientConn(netConn, addr, cfg)
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ssh dial %s: %w", addr, authHint(err, t.Password != ""))
	}
	netConn.SetDeadline(time.Time{}) // auth done; clear so normal I/O is unbounded

	client := gossh.NewClient(sshConn, chans, reqs)
	c := &Conn{client: client, Target: t}
	c.pathSep = detectPathSep(client)
	c.home = detectHome(client)
	return c, nil
}

// PathSep returns the remote path separator — "/" on Unix, `\` on Windows.
// Detected once after Dial by probing uname; cached for the session lifetime.
func (c *Conn) PathSep() string { return c.pathSep }

// detectPathSep runs "uname -s" on the remote host.
// Success → Unix ("/"). Failure (command not found) → Windows ("\").
func detectPathSep(client *gossh.Client) string {
	sess, err := client.NewSession()
	if err != nil {
		return "/"
	}
	defer sess.Close()
	if err := sess.Run("uname -s"); err != nil {
		return `\`
	}
	return "/"
}

// detectHome fetches the remote $HOME directory via "echo $HOME".
// Used to expand ~ in paths before SFTP calls — SFTP does not expand ~.
func detectHome(client *gossh.Client) string {
	sess, err := client.NewSession()
	if err != nil {
		return ""
	}
	defer sess.Close()
	var buf bytes.Buffer
	sess.Stdout = &buf
	if err := sess.Run("echo $HOME"); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

// ExpandHome replaces a leading ~ with the cached remote home directory.
// Returns an error when the path starts with ~/ but the remote $HOME is unknown,
// so callers can surface a clear message rather than a cryptic SFTP error.
func (c *Conn) ExpandHome(p string) (string, error) {
	if !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	if c.home == "" {
		return "", fmt.Errorf("path %q uses ~ but remote $HOME could not be detected — use an absolute path in app_dir", p)
	}
	return c.home + p[1:], nil
}

func (c *Conn) Close() error { return c.client.Close() }

// collectAuthMethods returns available SSH auth methods and a diagnostic string.
// The diagnostic is non-empty only when no methods were found.
// Auth order: SSH agent → ~/.ssh/ key files → target.password (if set).
func collectAuthMethods(t config.Target) ([]gossh.AuthMethod, string) {
	var methods []gossh.AuthMethod
	var issues []string

	if a := openSSHAgent(); a != nil {
		methods = append(methods, gossh.PublicKeysCallback(a.Signers))
	} else {
		issues = append(issues, agentDiag())
	}

	home, err := os.UserHomeDir()
	if err == nil {
		for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa", "id_dsa"} {
			data, err := os.ReadFile(filepath.Join(home, ".ssh", name))
			if err != nil {
				continue
			}
			signer, err := gossh.ParsePrivateKey(data)
			if err != nil {
				continue
			}
			methods = append(methods, gossh.PublicKeys(signer))
		}
	}

	if t.Password != "" {
		methods = append(methods, gossh.Password(t.Password))
	}

	if len(methods) == 0 {
		issues = append(issues, "no key files found in ~/.ssh/ (tried id_ed25519, id_rsa, id_ecdsa, id_dsa) and no password: configured")
		return nil, strings.Join(issues, "; ")
	}
	return methods, ""
}

// authHint wraps an SSH authentication error with a hint when all methods were
// exhausted. If no password was configured, it suggests setting one.
func authHint(err error, hasPassword bool) error {
	msg := err.Error()
	if strings.Contains(msg, "unable to authenticate") || strings.Contains(msg, "no supported methods remain") {
		if !hasPassword {
			return fmt.Errorf("%w — if the server requires password auth, set password: ${APP_PASSWORD} on the target and provide APP_PASSWORD via .env or shell", err)
		}
		return fmt.Errorf("%w — public key and password both rejected by server", err)
	}
	return err
}

// agentDiag returns a human-readable hint about why the SSH agent is unavailable.
func agentDiag() string {
	if runtime.GOOS == "windows" {
		return `SSH agent not available — start the "OpenSSH Authentication Agent" Windows service`
	}
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return "SSH agent not running (SSH_AUTH_SOCK not set) — run: eval $(ssh-agent) && ssh-add"
	}
	return fmt.Sprintf("SSH agent socket not responding (%s) — is the agent still running?", sock)
}

func openSSHAgent() agent.ExtendedAgent {
	if runtime.GOOS == "windows" {
		conn, err := npipeConn() // platform file: conn_windows.go / conn_notwindows.go
		if err == nil {
			return agent.NewClient(conn)
		}
		return nil
	}
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	return agent.NewClient(conn)
}
