// Package testoracle launches only the module-owned synthetic loopback fixture.
// It is imported by tests, never by the production service.
package testoracle

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type Oracle struct{ URL string }

const Token = "isolated-test-publisher-token-never-use-live"

func Start(t *testing.T, publication string) Oracle {
	t.Helper()
	if os.Getenv("CONTENT_TEST_ACTIVITYPUB") != "1" {
		t.Skip("requires opt-in synthetic Node contract oracle")
	}
	module := os.Getenv("CONTENT_TEST_ACTIVITYPUB_MODULE")
	node := os.Getenv("CONTENT_TEST_NODE")
	if module == "" || node == "" {
		t.Fatal("set explicit CONTENT_TEST_ACTIVITYPUB_MODULE and CONTENT_TEST_NODE; no installation or download is performed")
	}
	version, err := exec.Command(node, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "v24.") {
		t.Fatal("contract oracle requires existing Node 24")
	}
	// Fail visibly on contract drift rather than silently accepting changed fixtures.
	pinsBytes, err := os.ReadFile(filepath.Join("..", "..", "publishing", "testdata", "activitypub-v2-pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pins map[string]string
	if json.Unmarshal(pinsBytes, &pins) != nil {
		t.Fatal("invalid contract pins")
	}
	for path, expected := range pins {
		b, err := os.ReadFile(filepath.Join(module, path))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != expected {
			t.Fatal("AP contract changed; review fixture pin", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, node, filepath.Join(module, "test", "contract-server.fixture.ts"))
	cmd.Dir = module
	cmd.Env = []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "CONTENT_ACTIVITYPUB_TEST_SERVER=1", "AP_TEST_PUBLICATION_ID=" + publication, "AP_TEST_ORIGIN=https://example.invalid"}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(t.TempDir(), "oracle.stderr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stderr.Close() })
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		t.Fatal("oracle start failed", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cancel()
			<-done
		}
	})
	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Text()
		} else {
			line <- ""
		}
	}()
	var raw string
	select {
	case raw = <-line:
	case <-time.After(30 * time.Second):
		t.Fatal("oracle startup timeout")
	}
	var info struct {
		URL         string `json:"url"`
		Publication string `json:"publication_id"`
	}
	if json.Unmarshal([]byte(raw), &info) != nil || info.Publication != publication {
		t.Fatal("invalid synthetic oracle startup")
	}
	u, err := url.Parse(info.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" {
		t.Fatal("oracle must bind loopback")
	}
	return Oracle{URL: info.URL}
}
