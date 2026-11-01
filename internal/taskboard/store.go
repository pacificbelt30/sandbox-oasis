// Package taskboard manages durable host-side tasks and reviewed results.
package taskboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pacificbelt30/codex-dock/internal/privatefile"
)

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var oidPattern = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

type Spec struct {
	Title       string `json:"title"`
	Project     string `json:"project"`
	Prompt      string `json:"prompt"`
	Agent       string `json:"agent"`
	Image       string `json:"image,omitempty"`
	Policy      string `json:"policy,omitempty"`
	NoInternet  bool   `json:"no_internet,omitempty"`
	TestCommand string `json:"test_command,omitempty"`
}
type Attempt struct {
	Released     bool      `json:"released,omitempty"`
	TestReleased bool      `json:"test_released,omitempty"`
	Number       int       `json:"number"`
	Name         string    `json:"name"`
	Workspace    string    `json:"workspace"`
	GitDir       string    `json:"git_dir,omitempty"`
	Worker       string    `json:"worker,omitempty"`
	State        string    `json:"state"`
	ExitCode     *int      `json:"exit_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	Started      time.Time `json:"started"`
	TestName     string    `json:"test_name,omitempty"`
	TestWorker   string    `json:"test_worker,omitempty"`
	TestState    string    `json:"test_state,omitempty"`
	TestExit     *int      `json:"test_exit,omitempty"`
	TestTree     string    `json:"test_tree,omitempty"`
}
type Task struct {
	Common         string    `json:"common"`
	ID             string    `json:"id"`
	Spec           Spec      `json:"spec"`
	GitDir         string    `json:"git_dir"`
	Base           string    `json:"base"`
	Branch         string    `json:"branch"`
	State          string    `json:"state"`
	Created        time.Time `json:"created"`
	Updated        time.Time `json:"updated"`
	Attempts       []Attempt `json:"attempts"`
	AcceptedBranch string    `json:"accepted_branch,omitempty"`
	AcceptedCommit string    `json:"accepted_commit,omitempty"`
}
type boardFile struct {
	Version int    `json:"version"`
	Tasks   []Task `json:"tasks"`
}
type Store struct{ Root string }

func DefaultStore() (Store, error) {
	p, e := os.UserConfigDir()
	if e != nil {
		return Store{}, e
	}
	return Store{filepath.Join(p, "codex-dock", "tasks")}, nil
}
func (s Store) path() string { return filepath.Join(s.Root, "board.json") }
func (s Store) read() (boardFile, []byte, error) {
	var b boardFile
	if !filepath.IsAbs(s.Root) || filepath.Clean(s.Root) != s.Root {
		return b, nil, fmt.Errorf("task store requires a clean absolute directory")
	}
	data, e := privatefile.Read(s.path())
	if os.IsNotExist(e) {
		return boardFile{Version: 1, Tasks: []Task{}}, nil, nil
	}
	if e != nil {
		return b, nil, e
	}
	if e = json.Unmarshal(data, &b); e != nil {
		return b, nil, e
	}
	if b.Version != 1 || len(b.Tasks) > 512 {
		return b, nil, fmt.Errorf("unsupported or oversized task board")
	}
	seen := map[string]bool{}
	for _, t := range b.Tasks {
		if !idPattern.MatchString(t.ID) || seen[t.ID] || len(t.Attempts) > 16 || !oidPattern.MatchString(t.Base) {
			return b, nil, fmt.Errorf("invalid task record")
		}
		seen[t.ID] = true
	}
	return b, data, nil
}
func (s Store) List() ([]Task, error) { b, _, e := s.read(); return b.Tasks, e }
func (s Store) Get(id string) (Task, error) {
	if !idPattern.MatchString(id) {
		return Task{}, fmt.Errorf("full task ID required")
	}
	a, e := s.List()
	if e != nil {
		return Task{}, e
	}
	for _, t := range a {
		if t.ID == id {
			return t, nil
		}
	}
	return Task{}, fmt.Errorf("task not found")
}
func (s Store) change(fn func(*boardFile) error) error {
	for tries := 0; tries < 8; tries++ {
		_, raw, e := s.read()
		if e != nil {
			return e
		}
		e = privatefile.Update(s.path(), privatefile.Revision(raw), func(data []byte) ([]byte, error) {
			b := boardFile{Version: 1, Tasks: []Task{}}
			if len(data) > 0 {
				if e := json.Unmarshal(data, &b); e != nil {
					return nil, e
				}
			}
			if e := fn(&b); e != nil {
				return nil, e
			}
			if len(b.Tasks) > 512 {
				return nil, fmt.Errorf("task board holds at most 512 tasks")
			}
			return json.Marshal(b)
		})
		if e == nil || !strings.Contains(e.Error(), "changed; reload") {
			return e
		}
	}
	return fmt.Errorf("task board is busy; retry")
}
func (s Store) Update(id string, fn func(*Task) error) (Task, error) {
	var out Task
	e := s.change(func(b *boardFile) error {
		for i := range b.Tasks {
			if b.Tasks[i].ID == id {
				if e := fn(&b.Tasks[i]); e != nil {
					return e
				}
				b.Tasks[i].Updated = time.Now().UTC()
				out = b.Tasks[i]
				return nil
			}
		}
		return fmt.Errorf("task not found")
	})
	return out, e
}
func validText(s string, max int, multiline bool) bool {
	if strings.TrimSpace(s) == "" || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		allowedControl := multiline && (r == '\n' || r == '\t' || r == '\r')
		if r == 0 || r < 32 && !allowedControl {
			return false
		}
	}
	return true
}
func (s Store) Create(spec Spec, repo Repository) (Task, error) {
	if !validText(spec.Title, 200, false) || !validText(spec.Prompt, 32768, true) || len(spec.TestCommand) > 4096 {
		return Task{}, fmt.Errorf("task needs a title (200 bytes) and prompt (32 KiB maximum)")
	}
	if spec.Agent != "codex" && spec.Agent != "claude" && spec.Agent != "shell" {
		return Task{}, fmt.Errorf("agent must be codex, claude or shell")
	}
	if len(spec.Image) > 512 || len(spec.Policy) > 128 || strings.ContainsAny(spec.Image+spec.Policy+spec.TestCommand, "\x00\r\n") {
		return Task{}, fmt.Errorf("invalid task launch settings")
	}
	if spec.Project != repo.Root || !oidPattern.MatchString(repo.Head) {
		return Task{}, fmt.Errorf("project does not match inspected repository")
	}
	if e := privateDirectory(s.Root); e != nil {
		return Task{}, e
	}
	canonical, e := filepath.EvalSymlinks(s.Root)
	if e != nil || canonical != s.Root {
		return Task{}, fmt.Errorf("task store must use its canonical absolute path")
	}
	rel, e := filepath.Rel(spec.Project, s.Root)
	if e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return Task{}, fmt.Errorf("task store must be outside the shared project")
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return Task{}, e
	}
	now := time.Now().UTC()
	t := Task{ID: hex.EncodeToString(random[:]), Spec: spec, GitDir: repo.GitDir, Common: repo.Common, Base: repo.Head, Branch: repo.Branch, State: "queued", Created: now, Updated: now, Attempts: []Attempt{}}
	e = s.change(func(b *boardFile) error { b.Tasks = append(b.Tasks, t); return nil })
	return t, e
}
func Current(t Task) (Attempt, bool) {
	if len(t.Attempts) == 0 {
		return Attempt{}, false
	}
	return t.Attempts[len(t.Attempts)-1], true
}
