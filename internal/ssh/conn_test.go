// internal/ssh/conn_test.go
package ssh_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
)

// Integration tests. Set TEST_SSH_HOST=user@host:port to run.
// Optional TEST_SSH_DIR=/tmp/regis-test for remote working dir.

func testTarget(t *testing.T) config.Target {
	t.Helper()
	raw := os.Getenv("TEST_SSH_HOST")
	if raw == "" {
		t.Skip("TEST_SSH_HOST not set — skipping SSH integration tests")
	}
	parts := strings.Split(raw, "@")
	if len(parts) != 2 {
		t.Fatalf("TEST_SSH_HOST must be user@host or user@host:port, got %q", raw)
	}
	user := parts[0]
	host := parts[1]
	port := 22
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		p, err := strconv.Atoi(host[idx+1:])
		if err == nil {
			port = p
		}
		host = host[:idx]
	}
	dir := os.Getenv("TEST_SSH_DIR")
	if dir == "" {
		dir = "/tmp/regis-test"
	}
	return config.Target{Name: "test", Host: host, User: user, Port: strconv.Itoa(port), Dir: dir}
}

func TestDial(t *testing.T) {
	tgt := testTarget(t)
	conn, err := regssh.Dial(tgt)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	fmt.Println("connected to", tgt.Host)
}

func TestRun_echo(t *testing.T) {
	tgt := testTarget(t)
	conn, err := regssh.Dial(tgt)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	stdout, stderr, code, err := conn.Run("echo hello")
	if err != nil {
		t.Fatalf("Run: %v (stderr: %s)", err, stderr)
	}
	if code != 0 {
		t.Errorf("exit code: %d", code)
	}
	if strings.TrimSpace(stdout) != "hello" {
		t.Errorf("stdout: %q", stdout)
	}
}

func TestRun_nonzeroExit(t *testing.T) {
	tgt := testTarget(t)
	conn, err := regssh.Dial(tgt)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, _, code, err := conn.Run("exit 42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 42 {
		t.Errorf("want exit 42, got %d", code)
	}
}

func TestUploadBytes_and_Download(t *testing.T) {
	tgt := testTarget(t)
	conn, err := regssh.Dial(tgt)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.Run("mkdir -p " + tgt.Dir)
	data := []byte("hello regis\n")
	remote := tgt.Dir + "/regis-upload-test.txt"

	if err := conn.UploadBytes(data, remote, 0644, false); err != nil {
		t.Fatalf("UploadBytes: %v", err)
	}
	got, err := conn.Download(remote)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
	conn.Run("rm " + remote)
}

func TestMD5(t *testing.T) {
	tgt := testTarget(t)
	conn, err := regssh.Dial(tgt)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Run("mkdir -p " + tgt.Dir)

	data := []byte("checksum content")
	remote := tgt.Dir + "/regis-md5.bin"
	conn.UploadBytes(data, remote, 0644, false)

	md5, err := conn.MD5(remote)
	if err != nil {
		t.Fatalf("MD5: %v", err)
	}
	if len(md5) != 32 {
		t.Errorf("unexpected MD5 length: %q", md5)
	}
	conn.Run("rm " + remote)
}
