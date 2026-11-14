package xray

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/model"
	"github.com/cedar2025/xboard-node/internal/nlog"
	"github.com/cedar2025/xboard-node/internal/speedtest"
)

// fallbackEligible reports whether a node can carry a VLESS fallback to the
// built-in speedtest site. xray only supports fallbacks with xver:1 for
// VLESS over tcp with decryption "none", so those are hard requirements.
func fallbackEligible(cfg config.KernelConfig, nc *model.NodeSpec) bool {
	if nc == nil || !cfg.Fallback.IsEnabled() {
		return false
	}
	if !strings.EqualFold(nc.Protocol, "vless") {
		return false
	}
	if nc.Network != "" && !strings.EqualFold(nc.Network, "tcp") {
		return false
	}
	return nc.Decryption == "" || strings.EqualFold(nc.Decryption, "none")
}

// speedtestSocketName derives a stable abstract Unix socket name from the
// process ID plus the config dir and port, so concurrent nodes never collide.
func speedtestSocketName(configDir string, port int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d", configDir, port)))
	return fmt.Sprintf("@xboard-node-%d-%s", os.Getpid(), hex.EncodeToString(sum[:])[:12])
}

// ensureSpeedtest starts (or reuses) the speedtest server backing the VLESS
// fallback and returns the socket address to use as the fallback dest. A
// running server is reused only when both its socket name and config match
// what is wanted now (the socket name derives from the node port); otherwise
// the old service is stopped before re-binding.
func (x *Xray) ensureSpeedtest(nc *model.NodeSpec) (string, error) {
	x.speedtestMu.Lock()
	defer x.speedtestMu.Unlock()

	socket := speedtestSocketName(x.cfg.ConfigDir, nc.ServerPort)
	want := speedtest.Config{
		Title:         x.cfg.Fallback.Title,
		MaxTransferMB: x.cfg.Fallback.MaxTransferMB,
		MaxConcurrent: x.cfg.Fallback.MaxConcurrent,
	}
	if s := x.speedtest; s != nil && s.Socket() == socket && s.Config() == want {
		return s.Socket(), nil
	}

	x.stopSpeedtestLocked() // stop the old service before re-binding
	s, err := speedtest.Start(want, socket)
	if err != nil {
		return "", err
	}
	x.speedtest = s

	nlog.Core().Info("xray: speedtest fallback started", "socket", socket)
	return s.Socket(), nil
}

// stopSpeedtest shuts down the speedtest server if one is running.
func (x *Xray) stopSpeedtest() {
	x.speedtestMu.Lock()
	defer x.speedtestMu.Unlock()
	x.stopSpeedtestLocked()
}

// stopSpeedtestLocked shuts down the speedtest server if one is running.
// Callers must hold speedtestMu.
func (x *Xray) stopSpeedtestLocked() {
	s := x.speedtest
	x.speedtest = nil
	if s == nil {
		return
	}
	if err := s.Close(); err != nil {
		nlog.Core().Warn("xray: speedtest fallback close failed", "error", err)
		return
	}
	nlog.Core().Info("xray: speedtest fallback stopped", "socket", s.Socket())
}
