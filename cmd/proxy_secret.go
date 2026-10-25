package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/viper"
)

// File-based admin authority avoids bearer secrets in argv/container metadata.
func resolveProxyAdminSecret(value, path string) (string, error) {
	if path == "" {
		return value, nil
	}
	if value != "" {
		return "", fmt.Errorf("choose admin secret or admin secret file, not both")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("opening admin secret file failed")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("reading admin secret metadata failed")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", fmt.Errorf("admin secret file must be an owner-only regular file (0600)")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return "", fmt.Errorf("invalid admin secret file")
	}
	value = strings.TrimSpace(string(data))
	if len(value) < 32 || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("admin secret file must contain at least 32 bytes without embedded line breaks")
	}
	return value, nil
}

func effectiveRunProxySecret() (string, error) {
	return automaticProxyAdminSecret(proxyAdminURL, runProxyAdminSecret, runProxyAdminSecretFile)
}

func proxyServerSecret() (string, string, error) {
	path := proxyAdminSecretFile
	if path == "" && proxyAdminSecret == "" {
		path = viper.GetString("proxy.admin_secret_file")
	}
	secret, err := resolveProxyAdminSecret(proxyAdminSecret, path)
	return secret, path, err
}

// Managed local proxies get a private admin authority even in legacy provider
// login mode. Never send this implicit authority to a custom/external endpoint.
func automaticProxyAdminSecret(adminURL, value, path string) (string, error) {
	if value != "" || path != "" {
		return resolveProxyAdminSecret(value, path)
	}
	if adminURL != "http://127.0.0.1:18081" && adminURL != "http://localhost:18081" {
		return "", nil
	}
	directory, err := defaultConfigDir()
	if err != nil {
		return "", err
	}
	path = filepath.Join(directory, "proxy-admin-secret")
	if _, err = os.Lstat(path); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return resolveProxyAdminSecret("", path)
}
func prepareManagedProxyAdminSecret() (string, string, error) {
	directory, err := defaultConfigDir()
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(directory, "proxy-admin-secret")
	if err = prepareLoginAdminSecret(path); err != nil {
		return "", "", err
	}
	value, err := resolveProxyAdminSecret("", path)
	return value, path, err
}
