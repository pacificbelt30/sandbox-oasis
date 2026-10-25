package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
)

const proxyTransportLabel = "io.codex-dock.proxy-transport"

func proxyTransportID(a proxyRunArgs) string {
	if a.upstreamProxy == "" && a.caFile == "" && a.brokerFile == "" && len(a.allowedModels) == 0 && a.adminSecretFile == "" && a.usageStateDir == "" {
		return ""
	}
	bundle, _ := os.ReadFile(a.caFile)
	broker, _ := os.ReadFile(a.brokerFile)
	bundle = append(bundle, broker...)
	if a.usageStateDir != "" {
		bundle = append(bundle, []byte("\x00usage-state="+a.usageStateDir)...)
	}
	if a.adminSecretFile != "" {
		bundle = append(bundle, []byte("\x00admin-secret-file="+a.adminSecretFile)...)
	}
	if len(a.allowedModels) != 0 {
		models, _ := json.Marshal(a.allowedModels)
		bundle = append(bundle, models...)
	}
	sum := sha256.Sum256(append([]byte(a.upstreamProxy+"\x00"), bundle...))
	return hex.EncodeToString(sum[:])
}
