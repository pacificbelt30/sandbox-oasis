package cmd

import (
	"os"

	"github.com/pacificbelt30/codex-dock/internal/kataguard"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/viper"
)

// Management reconnects only when needed. Keep the admin secret in the host
// configuration; worker labels contain connection URLs, never that secret.
func managementConfig() sandbox.ManagerConfig {
	return sandbox.ManagerConfig{Verbose: verbose, KataConfig: &kataguard.Config{Socket: viper.GetString("kata.node_socket")}, ProxyConnector: func(adminURL, containerURL string) (authproxy.Service, error) {
		if adminURL == "" {
			adminURL = viper.GetString("proxy.admin_url")
		}
		if adminURL == "" {
			adminURL = "http://127.0.0.1:18081"
		}
		if containerURL == "" {
			containerURL = viper.GetString("proxy.container_url")
		}
		if containerURL == "" {
			containerURL = "http://codex-auth-proxy:18080"
		}
		secret := viper.GetString("proxy.admin_secret")
		if value, ok := os.LookupEnv("CODEX_DOCK_PROXY_ADMIN_SECRET"); ok {
			secret = value
		}
		secret, err := automaticProxyAdminSecret(adminURL, secret, viper.GetString("proxy.admin_secret_file"))
		if err != nil {
			return nil, err
		}
		return authproxy.NewRemoteProxy(adminURL, containerURL, secret)
	}}
}
