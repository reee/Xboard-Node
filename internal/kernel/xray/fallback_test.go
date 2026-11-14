package xray

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/model"
)

func boolPtr(v bool) *bool { return &v }

func TestFallbackEligible(t *testing.T) {
	enabled := config.KernelConfig{}
	disabled := config.KernelConfig{Fallback: config.FallbackConfig{Enabled: boolPtr(false)}}

	tests := []struct {
		name string
		cfg  config.KernelConfig
		nc   *model.NodeSpec
		want bool
	}{
		{"vless tcp default", enabled, &model.NodeSpec{Protocol: "vless", Network: "tcp"}, true},
		{"vless empty network", enabled, &model.NodeSpec{Protocol: "vless"}, true},
		{"vless ws", enabled, &model.NodeSpec{Protocol: "vless", Network: "ws"}, false},
		{"vmess tcp", enabled, &model.NodeSpec{Protocol: "vmess", Network: "tcp"}, false},
		{"disabled", disabled, &model.NodeSpec{Protocol: "vless"}, false},
		{"decryption non-none", enabled, &model.NodeSpec{Protocol: "vless", Decryption: "auto"}, false},
		{"decryption none", enabled, &model.NodeSpec{Protocol: "vless", Decryption: "none"}, true},
		{"nil spec", enabled, nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fallbackEligible(tc.cfg, tc.nc); got != tc.want {
				t.Errorf("fallbackEligible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSpeedtestSocketName(t *testing.T) {
	a := speedtestSocketName("/etc/xboard-node", 443)
	b := speedtestSocketName("/etc/xboard-node", 8443)
	c := speedtestSocketName("/var/lib/xboard-node", 443)

	if !strings.HasPrefix(a, "@xboard-node-") {
		t.Errorf("socket name %q missing prefix", a)
	}
	if a == b || a == c || b == c {
		t.Errorf("socket names must differ per configDir/port: %q %q %q", a, b, c)
	}
	if len(a) > 64 {
		t.Errorf("socket name too long (%d chars): %q", len(a), a)
	}
}

func TestEnsureSpeedtestServes(t *testing.T) {
	x := New(config.KernelConfig{ConfigDir: t.TempDir()})

	// Port is only a socket-name hash input; no TCP port is bound.
	dest, err := x.ensureSpeedtest(&model.NodeSpec{Protocol: "vless", ServerPort: 12345})
	if err != nil {
		t.Fatalf("ensureSpeedtest: %v", err)
	}
	if !strings.HasPrefix(dest, "@xboard-node-") {
		t.Errorf("unexpected dest: %q", dest)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", dest)
			},
		},
	}
	resp, err := client.Get("http://unix/api/ping")
	if err != nil {
		t.Fatalf("GET /api/ping over %s: %v", dest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("body missing ok:true: %s", body)
	}

	x.stopSpeedtest()

	conn, err := net.Dial("unix", dest)
	if err == nil {
		conn.Close()
		t.Error("dial should fail after stopSpeedtest")
	}
}

// fullFallbackCfg returns fallback settings that already match the values the
// speedtest server normalises to, so ensureSpeedtest's desired-config check
// sees no spurious difference.
func fullFallbackCfg() config.FallbackConfig {
	return config.FallbackConfig{Title: "Speed Test", MaxTransferMB: 256, MaxConcurrent: 32}
}

func TestEnsureSpeedtestReuse(t *testing.T) {
	x := New(config.KernelConfig{ConfigDir: t.TempDir(), Fallback: fullFallbackCfg()})
	defer x.stopSpeedtest()

	nc := &model.NodeSpec{Protocol: "vless", ServerPort: 12345}
	dest1, err := x.ensureSpeedtest(nc)
	if err != nil {
		t.Fatalf("ensureSpeedtest: %v", err)
	}
	first := x.speedtest

	dest2, err := x.ensureSpeedtest(nc)
	if err != nil {
		t.Fatalf("ensureSpeedtest (second call): %v", err)
	}
	if x.speedtest != first {
		t.Error("server must be reused when socket and config are unchanged")
	}
	if dest2 != dest1 {
		t.Errorf("dest changed: %q -> %q", dest1, dest2)
	}
}

func TestEnsureSpeedtestPortChange(t *testing.T) {
	configDir := t.TempDir()
	x := New(config.KernelConfig{ConfigDir: configDir, Fallback: fullFallbackCfg()})
	defer x.stopSpeedtest()

	if _, err := x.ensureSpeedtest(&model.NodeSpec{Protocol: "vless", ServerPort: 12345}); err != nil {
		t.Fatalf("ensureSpeedtest: %v", err)
	}
	first := x.speedtest

	if _, err := x.ensureSpeedtest(&model.NodeSpec{Protocol: "vless", ServerPort: 23456}); err != nil {
		t.Fatalf("ensureSpeedtest (new port): %v", err)
	}
	if x.speedtest == first {
		t.Error("server must be re-bound when the node port changes")
	}
	if want := speedtestSocketName(configDir, 23456); x.speedtest.Socket() != want {
		t.Errorf("socket = %q, want %q", x.speedtest.Socket(), want)
	}
}

func TestEnsureSpeedtestConfigChange(t *testing.T) {
	x := New(config.KernelConfig{ConfigDir: t.TempDir(), Fallback: fullFallbackCfg()})
	defer x.stopSpeedtest()

	nc := &model.NodeSpec{Protocol: "vless", ServerPort: 12345}
	if _, err := x.ensureSpeedtest(nc); err != nil {
		t.Fatalf("ensureSpeedtest: %v", err)
	}
	first := x.speedtest

	x.cfg.Fallback.Title = "Node Speed Test"
	if _, err := x.ensureSpeedtest(nc); err != nil {
		t.Fatalf("ensureSpeedtest (new title): %v", err)
	}
	if x.speedtest == first {
		t.Error("server must be re-bound when the fallback config changes")
	}
	if got := x.speedtest.Config().Title; got != "Node Speed Test" {
		t.Errorf("re-bound title = %q, want %q", got, "Node Speed Test")
	}
}
