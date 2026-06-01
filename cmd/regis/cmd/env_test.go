// cmd/regis/cmd/env_test.go
package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/cmd/regis/cmd"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// minimalRegisYml returns a regis.yml with the given vars referenced in cue shell fields.
func minimalRegisYml(targetName string, vars ...string) string {
	shellParts := ""
	for _, v := range vars {
		shellParts += "      - name: use-" + v + "\n"
		shellParts += "        shell: echo ${" + v + "}\n"
	}
	return `targets:
  - name: ` + targetName + `
    host: 192.0.2.1
    user: deploy
    dir: /srv/app
scenarios:
  deploy:
    cues:
` + shellParts
}

func runEnvCommand(t *testing.T, dir, target string, extraArgs ...string) (string, error) {
	t.Helper()
	root := cmd.NewRootCommand("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	args := []string{"env", "--file", filepath.Join(dir, "regis.yml")}
	if target != "" {
		args = append(args, "--target", target)
	}
	args = append(args, extraArgs...)
	root.SetArgs(args)

	err := root.Execute()
	return buf.String(), err
}

func TestEnvCommand_ShowsLoadedFiles(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "regis.yml", minimalRegisYml("prod", "NODE_HOST"))
	writeFile(t, dir, ".env.local", "NODE_HOST=local.example.com\n")
	writeFile(t, dir, ".env.prod", "NODE_HOST=prod.example.com\n")

	out, err := runEnvCommand(t, dir, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, ".env.local") {
		t.Errorf("expected output to contain .env.local, got:\n%s", out)
	}
	if !strings.Contains(out, ".env.prod") {
		t.Errorf("expected output to contain .env.prod, got:\n%s", out)
	}
}

func TestEnvCommand_SourceColumn_Shell(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "regis.yml", minimalRegisYml("prod", "JELASTIC_TOKEN"))
	writeFile(t, dir, ".env.local", "")
	writeFile(t, dir, ".env.prod", "")

	t.Setenv("JELASTIC_TOKEN", "tok-from-shell")

	out, err := runEnvCommand(t, dir, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "shell") {
		t.Errorf("expected 'shell' source in output, got:\n%s", out)
	}
}

func TestEnvCommand_SourceColumn_Target(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "regis.yml", minimalRegisYml("prod", "NODE_HOST"))
	writeFile(t, dir, ".env.local", "")
	writeFile(t, dir, ".env.prod", "NODE_HOST=prod.example.com\n")

	// Ensure it's not in shell env for this test
	os.Unsetenv("NODE_HOST")

	out, err := runEnvCommand(t, dir, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, ".env.prod") {
		t.Errorf("expected '.env.prod' source in output, got:\n%s", out)
	}
}

func TestEnvCommand_SourceColumn_Global(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "regis.yml", minimalRegisYml("prod", "NODE_USER"))
	writeFile(t, dir, ".env.local", "NODE_USER=deploy\n")
	// no .env.prod — target env file absent

	os.Unsetenv("NODE_USER")

	out, err := runEnvCommand(t, dir, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, ".env.local") {
		t.Errorf("expected '.env.local' source in output, got:\n%s", out)
	}
}

func TestEnvCommand_SourceColumn_Unset(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "regis.yml", minimalRegisYml("prod", "MISSING_VAR"))
	writeFile(t, dir, ".env.local", "")
	// no .env.prod

	os.Unsetenv("MISSING_VAR")

	out, err := runEnvCommand(t, dir, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "(unset)") {
		t.Errorf("expected '(unset)' in output, got:\n%s", out)
	}
}

func TestEnvCommand_SecretMasking(t *testing.T) {
	dir := t.TempDir()

	secretVars := []string{"DEPLOY_TOKEN", "API_KEY", "DB_PASSWORD", "APP_SECRET", "USER_PASS", "DB_CRED"}
	writeFile(t, dir, "regis.yml", minimalRegisYml("prod", secretVars...))

	envContent := "DEPLOY_TOKEN=tok123\nAPI_KEY=key456\nDB_PASSWORD=pass789\nAPP_SECRET=sec000\nUSER_PASS=p@ss!\nDB_CRED=cred!\n"
	writeFile(t, dir, ".env.prod", envContent)
	writeFile(t, dir, ".env.local", "")

	out, err := runEnvCommand(t, dir, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput: %s", err, out)
	}

	// Secret values must be masked
	for _, plain := range []string{"tok123", "key456", "pass789", "sec000", "p@ss!", "cred!"} {
		if strings.Contains(out, plain) {
			t.Errorf("expected value %q to be masked, got:\n%s", plain, out)
		}
	}

	// Masked marker should appear
	if !strings.Contains(out, "***") {
		t.Errorf("expected '***' masking in output, got:\n%s", out)
	}
}

func TestEnvCommand_TargetFlag(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "regis.yml", `targets:
  - name: prod
    host: 192.0.2.1
    user: deploy
    dir: /srv/app
  - name: staging
    host: 192.0.2.2
    user: deploy
    dir: /srv/app
scenarios:
  deploy:
    cues:
      - name: use-host
        shell: echo ${NODE_HOST}
`)
	writeFile(t, dir, ".env.local", "")
	writeFile(t, dir, ".env.prod", "NODE_HOST=prod.example.com\n")
	writeFile(t, dir, ".env.staging", "NODE_HOST=staging.example.com\n")

	os.Unsetenv("NODE_HOST")

	out, err := runEnvCommand(t, dir, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput: %s", err, out)
	}

	if strings.Contains(out, "staging.example.com") {
		t.Errorf("--target prod should not show staging value, got:\n%s", out)
	}
	if !strings.Contains(out, "prod.example.com") {
		t.Errorf("--target prod should show prod value, got:\n%s", out)
	}
}
