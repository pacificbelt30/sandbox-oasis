package managedworkspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

func Excluded(p string) string {
	for _, part := range strings.Split(p, "/") {
		n := strings.ToLower(part)
		switch n {
		case ".git":
			return "Git metadata"
		case "node_modules", ".venv", "venv", "__pycache__", ".cache", "lost+found":
			return "dependency/cache directory"
		case ".ssh", ".aws", ".azure", ".codex", ".claude", ".config", "auth.json", "credentials.json", ".npmrc", ".netrc", ".pypirc":
			return "credential path"
		}
		if strings.HasPrefix(n, ".env") || strings.HasSuffix(n, ".pem") || strings.HasSuffix(n, ".key") || strings.HasSuffix(n, ".p12") || strings.HasSuffix(n, ".pfx") {
			return "credential path"
		}
	}
	return ""
}
func (m Manifest) Validate(limit int64) error {
	if len(m.Files) > MaxFiles || !ID.MatchString(m.ID) || limit < 0 || limit > MaxSeedBytes {
		return fmt.Errorf("invalid workspace manifest limits")
	}
	var total int64
	previous := ""
	for _, f := range m.Files {
		if err := FilePath(f.Path); err != nil {
			return err
		}
		if f.Path <= previous || Excluded(f.Path) != "" || f.Bytes < 0 || f.Bytes > MaxFileBytes || f.Mode != 0644 && f.Mode != 0755 || !ID.MatchString(f.SHA256) {
			return fmt.Errorf("invalid, excluded or duplicate workspace file")
		}
		previous = f.Path
		total += f.Bytes
		if total > limit {
			return fmt.Errorf("workspace seed exceeds selected capacity")
		}
	}
	// A file cannot also be an ancestor of another selected file.
	files := map[string]bool{}
	for _, f := range m.Files {
		files[f.Path] = true
	}
	for _, f := range m.Files {
		for parent := path.Dir(f.Path); parent != "."; parent = path.Dir(parent) {
			if files[parent] {
				return fmt.Errorf("workspace file/directory collision")
			}
		}
	}
	if total != m.Bytes || m.Digest() != m.ID {
		return fmt.Errorf("workspace manifest hash or size changed")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(data) > MaxManifestBytes {
		return fmt.Errorf("workspace manifest exceeds 1 MiB")
	}
	return nil
}
func (m Manifest) Digest() string {
	data, _ := json.Marshal(struct {
		Files []File `json:"files"`
		Bytes int64  `json:"bytes"`
	}{m.Files, m.Bytes})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
