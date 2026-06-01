// internal/ssh/conn.go
package ssh

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Conn wraps an active SSH connection to one target.
// Transplanted from jelastic-gateway/cmd/gateway-config/sshconn.go.
type Conn struct {
	client  *gossh.Client
	Target  config.Target
	pathSep string // "/" for Unix, `\` for Windows — detected once after Dial
}

// Dial opens ONE TCP connection. All Run/Upload/Download calls reuse it.
// Auth order: Windows OpenSSH agent (named pipe) → Unix SSH agent ($SSH_AUTH_SOCK)
// → $HOME/.ssh/id_{ed25519,rsa,ecdsa,dsa}.
func Dial(t config.Target) (*Conn, error) {
	port := 22
	if t.Port != "" {
		if n, err := strconv.Atoi(t.Port); err == nil {
			port = n
		}
	}
	methods, diag := collectAuthMethods()
	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH auth for %s@%s:%d — %s", t.User, t.Host, port, diag)
	}
	cfg := &gossh.ClientConfig{
		User:            t.User,
		Auth:            methods,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), // pragmatic for managed infra
	}
	addr := fmt.Sprintf("%s:%d", t.Host, port)
	client, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	c := &Conn{client: client, Target: t}
	c.pathSep = detectPathSep(client)
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

func (c *Conn) Close() error { return c.client.Close() }

// collectAuthMethods returns available SSH auth methods and a diagnostic string.
// The diagnostic is non-empty only when no methods were found.
func collectAuthMethods() ([]gossh.AuthMethod, string) {
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

	if len(methods) == 0 {
		issues = append(issues, "no key files found in ~/.ssh/ (tried id_ed25519, id_rsa, id_ecdsa, id_dsa)")
		return nil, strings.Join(issues, "; ")
	}
	return methods, ""
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
