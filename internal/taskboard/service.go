package taskboard

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type RuntimeState struct {
	Found, Running       bool
	ID, Workspace, Image string
	ExitCode             int
}
type Runtime interface {
	Launch(context.Context, Task, Attempt, bool) (string, error)
	Inspect(context.Context, string) (RuntimeState, error)
	Stop(context.Context, string) error
}
type Service struct {
	Store   Store
	Runtime Runtime
}

func (s *Service) Create(ctx context.Context, spec Spec) (Task, error) {
	r, e := InspectRepository(ctx, spec.Project)
	if e != nil {
		return Task{}, e
	}
	spec.Project = r.Root
	return s.Store.Create(spec, r)
}
func (s *Service) Run(ctx context.Context, id string) (Task, error) {
	unlock, lockErr := s.Store.operation(ctx, id)
	if lockErr != nil {
		return Task{}, lockErr
	}
	defer unlock()
	t, e := s.Store.Get(id)
	if e != nil {
		return t, e
	}
	if t.State != "queued" && t.State != "failed" && t.State != "cancelled" {
		return t, fmt.Errorf("task must be queued, failed or cancelled; stop or reconcile the current attempt first")
	}
	if a, ok := Current(t); ok {
		v, e := s.Runtime.Inspect(ctx, a.Name)
		if e != nil {
			return t, e
		}
		if v.Found && v.Running {
			return t, fmt.Errorf("previous attempt is still running")
		}
		if a.TestName != "" {
			v, e = s.Runtime.Inspect(ctx, a.TestName)
			if e != nil {
				return t, e
			}
			if v.Found && v.Running {
				return t, fmt.Errorf("previous test is still running")
			}
		}
	}
	t, e = s.Store.Update(id, func(v *Task) error {
		if v.State != t.State || len(v.Attempts) != len(t.Attempts) {
			return fmt.Errorf("task changed; reload")
		}
		if len(v.Attempts) >= 16 {
			return fmt.Errorf("task attempt limit reached")
		}
		n := len(v.Attempts) + 1
		name := fmt.Sprintf("oaiso-task-%s-%d", id[:16], n)
		workspace := filepath.Join(s.Store.Root, "workspaces", name)
		v.Attempts = append(v.Attempts, Attempt{Number: n, Name: name, Workspace: workspace, State: "launching", Started: time.Now().UTC()})
		v.State = "launching"
		return nil
	})
	if e != nil {
		return t, e
	}
	a, _ := Current(t)
	gitdir, e := PrepareWorkspace(ctx, t, a)
	if e == nil {
		t, e = s.Store.Update(id, func(v *Task) error { v.Attempts[len(v.Attempts)-1].GitDir = gitdir; return nil })
	}
	if e == nil {
		a, _ = Current(t)
		var worker string
		worker, e = s.Runtime.Launch(ctx, t, a, false)
		if e == nil {
			t, e = s.Store.Update(id, func(v *Task) error {
				p := &v.Attempts[len(v.Attempts)-1]
				p.Worker = worker
				p.State = "running"
				v.State = "running"
				return nil
			})
		}
	}
	if e != nil {
		cause := e
		// A lost launch response is not evidence that no worker was created.
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		state, lookupErr := s.Runtime.Inspect(lookupCtx, a.Name)
		cancel()
		_, _ = s.Store.Update(id, func(v *Task) error {
			p := &v.Attempts[len(v.Attempts)-1]
			p.Error = shortError(cause)
			p.State = "failed"
			v.State = "failed"
			if lookupErr != nil {
				p.State = "unknown"
				v.State = "unknown"
			} else if state.Found {
				p.Worker = state.ID
				p.State = "running"
				v.State = "running"
			}
			return nil
		})
		return t, cause
	}
	return s.sync(ctx, id)
}
func shortError(e error) string {
	if e == nil {
		return ""
	}
	text := e.Error()
	if len(text) > 2000 {
		text = text[:2000]
	}
	return text
}
func (s *Service) Sync(ctx context.Context, id string) (Task, error) {
	unlock, e := s.Store.operation(ctx, id)
	if e != nil {
		return Task{}, e
	}
	defer unlock()
	return s.sync(ctx, id)
}
func (s *Service) sync(ctx context.Context, id string) (Task, error) {
	t, e := s.Store.Get(id)
	if e != nil {
		return t, e
	}
	a, ok := Current(t)
	if !ok {
		return t, nil
	}
	state, e := s.Runtime.Inspect(ctx, a.Name)
	if e != nil {
		return t, e
	}
	if state.Found && state.Workspace != a.Workspace {
		return t, fmt.Errorf("worker workspace does not match the recorded task")
	}
	newState := a.State
	if state.Found {
		if state.Running {
			newState = "running"
		} else if t.State == "cancelled" {
			newState = "cancelled"
		} else if state.ExitCode == 0 {
			newState = "review"
		} else {
			newState = "failed"
		}
	} else if a.State != "launching" {
		newState = "missing"
	}
	test := RuntimeState{}
	if a.TestName != "" {
		test, e = s.Runtime.Inspect(ctx, a.TestName)
		if e != nil {
			return t, e
		}
		if test.Found && test.Workspace != a.Workspace {
			return t, fmt.Errorf("test worker workspace differs")
		}
	}

	// A naturally exited process still owns a Kata reservation and token until
	// the normal stop path releases them. Preserve its snapshot and workspace.
	released, testReleased := a.Released, a.TestReleased
	if state.Found && state.Running {
		released = false
	}
	if state.Found && !state.Running && !released {
		if e = s.Runtime.Stop(ctx, state.ID); e != nil {
			return t, fmt.Errorf("finalize completed task: %w", e)
		}
		released = true
	}
	if test.Found && test.Running {
		testReleased = false
	}
	if test.Found && !test.Running && !testReleased {
		if e = s.Runtime.Stop(ctx, test.ID); e != nil {
			return t, fmt.Errorf("finalize completed verification: %w", e)
		}
		testReleased = true
	}
	return s.Store.Update(id, func(v *Task) error {
		p := &v.Attempts[len(v.Attempts)-1]
		if p.Number != a.Number {
			return fmt.Errorf("attempt changed; reload")
		}
		p.Released, p.TestReleased = released, testReleased
		p.State = newState
		if state.Found {
			p.Worker = state.ID
			if !state.Running {
				code := state.ExitCode
				p.ExitCode = &code
			}
		}
		if v.State != "accepted" {
			v.State = newState
		}
		if a.TestName != "" && test.Found {
			p.TestWorker = test.ID
			if test.Running {
				p.TestState = "running"
			} else {
				code := test.ExitCode
				p.TestExit = &code
				p.TestState = "passed"
				if code != 0 {
					p.TestState = "failed"
				}
			}
		}
		return nil
	})
}
func (s *Service) Stop(ctx context.Context, id string) (Task, error) {
	unlock, lockErr := s.Store.operation(ctx, id)
	if lockErr != nil {
		return Task{}, lockErr
	}
	defer unlock()
	t, e := s.Store.Get(id)
	if e != nil {
		return t, e
	}
	a, ok := Current(t)
	if ok {
		for _, name := range []string{a.Name, a.TestName} {
			if name == "" {
				continue
			}
			state, e := s.Runtime.Inspect(ctx, name)
			if e != nil {
				return t, e
			}
			if state.Found && state.Workspace != a.Workspace {
				return t, fmt.Errorf("task worker workspace differs")
			}
			if state.Found {
				if e = s.Runtime.Stop(ctx, state.ID); e != nil {
					return t, e
				}
			}
		}
	}
	return s.Store.Update(id, func(v *Task) error {
		if v.State != "accepted" {
			v.State = "cancelled"
		}
		if len(v.Attempts) > 0 {
			v.Attempts[len(v.Attempts)-1].State = "cancelled"
			v.Attempts[len(v.Attempts)-1].Released = true
			v.Attempts[len(v.Attempts)-1].TestReleased = true
		}
		return nil
	})
}
func (s *Service) idle(ctx context.Context, t Task) error {
	a, ok := Current(t)
	if !ok {
		return fmt.Errorf("task has no attempt")
	}
	for _, name := range []string{a.Name, a.TestName} {
		if name == "" {
			continue
		}
		st, e := s.Runtime.Inspect(ctx, name)
		if e != nil {
			return e
		}
		if st.Found && (st.Running || st.Workspace != a.Workspace) {
			return fmt.Errorf("stop task and test workers before reviewing or adopting changes")
		}
	}
	return nil
}
func (s *Service) Review(ctx context.Context, id string) (Review, error) {
	unlock, lockErr := s.Store.operation(ctx, id)
	if lockErr != nil {
		return Review{}, lockErr
	}
	defer unlock()
	t, e := s.sync(ctx, id)
	if e != nil {
		return Review{}, e
	}
	if e = s.idle(ctx, t); e != nil {
		return Review{}, e
	}
	r, e := Capture(ctx, t)
	if e != nil {
		return r, e
	}
	return r, s.Store.SaveReview(r)
}
func (s *Service) Test(ctx context.Context, id string) (Task, error) {
	unlock, lockErr := s.Store.operation(ctx, id)
	if lockErr != nil {
		return Task{}, lockErr
	}
	defer unlock()
	t, e := s.sync(ctx, id)
	if e != nil {
		return t, e
	}
	if strings.TrimSpace(t.Spec.TestCommand) == "" {
		return t, fmt.Errorf("task has no test command")
	}
	if e = s.idle(ctx, t); e != nil {
		return t, e
	}
	before, e := Capture(ctx, t)
	if e != nil {
		return t, e
	}
	t, e = s.Store.Update(id, func(v *Task) error {
		a := &v.Attempts[len(v.Attempts)-1]
		a.TestName = fmt.Sprintf("%s-test-%d", a.Name, time.Now().UnixNano())
		a.TestState = "launching"
		a.TestReleased = false
		a.TestExit = nil
		a.TestTree = before.ID
		return nil
	})
	if e != nil {
		return t, e
	}
	a, _ := Current(t)
	worker, e := s.Runtime.Launch(ctx, t, a, true)
	if e != nil {
		_, _ = s.Store.Update(id, func(v *Task) error {
			p := &v.Attempts[len(v.Attempts)-1]
			p.TestState = "unknown"
			p.Error = shortError(e)
			return nil
		})
		return t, e
	}
	return s.Store.Update(id, func(v *Task) error {
		p := &v.Attempts[len(v.Attempts)-1]
		p.TestWorker = worker
		p.TestState = "running"
		return nil
	})
}
func (s *Service) Plan(ctx context.Context, id, reviewID, target, result string, selection Selection) (Acceptance, error) {
	unlock, lockErr := s.Store.operation(ctx, id)
	if lockErr != nil {
		return Acceptance{}, lockErr
	}
	defer unlock()
	t, e := s.Store.Get(id)
	if e != nil {
		return Acceptance{}, e
	}
	if e = s.idle(ctx, t); e != nil {
		return Acceptance{}, e
	}
	r, e := s.Store.Review(reviewID)
	if e != nil {
		return Acceptance{}, e
	}
	current, e := Capture(ctx, t)
	if e != nil {
		return Acceptance{}, e
	}
	if current.ID != r.ID {
		return Acceptance{}, fmt.Errorf("workspace changed; refresh review")
	}
	return PlanAcceptance(ctx, t, r, target, result, selection)
}
func (s *Service) Accept(ctx context.Context, id string, a Acceptance) (Task, error) {
	unlock, lockErr := s.Store.operation(ctx, id)
	if lockErr != nil {
		return Task{}, lockErr
	}
	defer unlock()
	t, e := s.Store.Get(id)
	if e != nil {
		return t, e
	}
	if e = s.idle(ctx, t); e != nil {
		return t, e
	}
	r, e := s.Store.Review(a.Review)
	if e != nil {
		return t, e
	}
	if e = ApplyAcceptance(ctx, t, r, a); e != nil {
		return t, e
	}
	// Branch publication is idempotent. If this save fails, the same plan can
	// reconcile the published commit without producing another branch/commit.
	return s.Store.Update(id, func(v *Task) error {
		v.AcceptedBranch = a.ResultBranch
		v.AcceptedCommit = a.Commit
		v.State = "accepted"
		return nil
	})
}
