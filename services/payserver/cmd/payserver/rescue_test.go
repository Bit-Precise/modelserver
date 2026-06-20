package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRescue_HappyPath compiles the binary and runs `admin rescue` to
// confirm the subcommand parses args + prints a cookie line. The encoded
// token's verifiability is already tested in internal/server.
func TestRescue_HappyPath(t *testing.T) {
	bin := t.TempDir() + "/payserver-test"
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	c := exec.Command(bin, "admin", "rescue", "--email", "test@example.com", "--ttl", "1h")
	c.Env = append(os.Environ(), "PAYSERVER_OIDC_SESSION_SECRET=test-secret-32-bytes-padded-okay!")
	out, err := c.Output()
	if err != nil {
		t.Fatalf("rescue exec: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "payserver_admin_session=") {
		t.Errorf("output missing cookie line:\n%s", s)
	}
}
