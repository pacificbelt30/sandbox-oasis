package releaseinfo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Installer checks the requested release installer hash and verifies its signed
// provenance before returning a private, non-executable file.
func Installer(ctx context.Context, tag, directory string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: releaseRedirect}
	return signedInstaller(ctx, client, tag, directory)
}

func releaseRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.User != nil {
		return fmt.Errorf("invalid release redirect")
	}
	switch req.URL.Hostname() {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return nil
	default:
		return fmt.Errorf("unrecognized release asset host")
	}
}

func installer(ctx context.Context, client *http.Client, tag, directory string) (string, error) {
	if !ValidTag(tag) {
		return "", fmt.Errorf("invalid release tag")
	}
	base := "https://github.com/" + Repository + "/releases/download/" + tag + "/"
	fetch := func(name string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+name, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("release asset download failed")
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("release asset download failed (HTTP %d)", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil || len(data) > 1<<20 {
			return nil, fmt.Errorf("release asset exceeds supported size")
		}
		return data, nil
	}
	checksums, err := fetch("SHA256SUMS")
	if err != nil {
		return "", err
	}
	var expected string
	for _, line := range strings.Split(string(checksums), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "install.sh" {
			if expected != "" {
				return "", fmt.Errorf("duplicate installer checksum")
			}
			expected = parts[0]
		}
	}
	if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("missing or invalid installer checksum")
	}
	data, err := fetch("install.sh")
	if err != nil {
		return "", err
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != strings.ToLower(expected) {
		return "", fmt.Errorf("release installer checksum mismatch")
	}
	path := filepath.Join(directory, "install.sh")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func signedInstaller(ctx context.Context, client *http.Client, tag, directory string) (string, error) {
	path, err := installer(ctx, client, tag, directory)
	if err != nil {
		return "", err
	}
	// Keep the unsigned helper private for checksum fixture tests only.
	verified := false
	defer func() {
		if !verified {
			_ = os.Remove(path)
		}
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/"+Repository+"/releases/download/"+tag+"/provenance.json", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("release attestation unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("release attestation unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return "", fmt.Errorf("release attestation exceeds limit")
	}
	bundle := filepath.Join(directory, "provenance.json")
	if err = os.WriteFile(bundle, data, 0600); err != nil {
		return "", err
	}
	if err = VerifyAttestation(ctx, path, bundle, tag); err != nil {
		return "", err
	}
	verified = true
	return path, nil
}
