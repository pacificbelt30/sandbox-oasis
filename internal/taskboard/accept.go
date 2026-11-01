package taskboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func acceptanceID(a Acceptance) string { a.ID = ""; b, _ := json.Marshal(a); return digestText(b) }
func acceptanceTree(ctx context.Context, t Task, r Review, a Acceptance) (string, error) {
	patch, e := SelectedPatch(r, a.Selection)
	if e != nil {
		return "", e
	}
	repo := Repository{Root: t.Spec.Project, GitDir: t.GitDir, Common: t.Common}
	p, cleanup, e := temporaryIndex()
	if e != nil {
		return "", e
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + p}
	if _, e = repo.run(ctx, env, nil, "read-tree", a.Target); e != nil {
		return "", e
	}
	if _, e = repo.run(ctx, env, bytes.NewReader(patch), "apply", "--cached", "--check", "--binary", "--whitespace=nowarn", "-"); e != nil {
		return "", fmt.Errorf("selected changes conflict with target: %w", e)
	}
	if _, e = repo.run(ctx, env, bytes.NewReader(patch), "apply", "--cached", "--binary", "--whitespace=nowarn", "-"); e != nil {
		return "", e
	}
	tree, e := repo.run(ctx, env, nil, "write-tree")
	return strings.TrimSpace(string(tree)), e
}
func PlanAcceptance(ctx context.Context, t Task, r Review, target, result string, selection Selection) (Acceptance, error) {
	var a Acceptance
	if r.Task != t.ID {
		return a, fmt.Errorf("review belongs to another task")
	}
	if !validBranch(result) || target == result {
		return a, fmt.Errorf("choose a new result branch distinct from the base branch")
	}
	repo := Repository{Root: t.Spec.Project, GitDir: t.GitDir, Common: t.Common}
	oid, e := repo.branch(ctx, target)
	if e != nil {
		return a, e
	}
	if _, e = repo.branch(ctx, result); e == nil {
		return a, fmt.Errorf("result branch already exists")
	}
	a = Acceptance{Review: r.ID, TargetBranch: target, Target: oid, ResultBranch: result, Selection: selection, Created: time.Now().UTC().Truncate(time.Second)}
	tree, e := acceptanceTree(ctx, t, r, a)
	if e != nil {
		return a, e
	}
	env := []string{"GIT_AUTHOR_NAME=Oaiso", "GIT_AUTHOR_EMAIL=oaiso@localhost", "GIT_COMMITTER_NAME=Oaiso", "GIT_COMMITTER_EMAIL=oaiso@localhost", "GIT_AUTHOR_DATE=" + a.Created.Format(time.RFC3339), "GIT_COMMITTER_DATE=" + a.Created.Format(time.RFC3339)}
	msg := fmt.Sprintf("Apply task %s: %s\n\nReviewed result %s\n", t.ID, t.Spec.Title, r.ID)
	out, e := repo.run(ctx, env, strings.NewReader(msg), "commit-tree", "--no-gpg-sign", tree, "-p", a.Target)
	if e != nil {
		return a, e
	}
	a.Commit = strings.TrimSpace(string(out))
	if !oidPattern.MatchString(a.Commit) {
		return a, fmt.Errorf("invalid acceptance commit")
	}
	a.ID = acceptanceID(a)
	return a, nil
}
func ApplyAcceptance(ctx context.Context, t Task, r Review, a Acceptance) error {
	if a.Review != r.ID || r.Task != t.ID || a.ID != acceptanceID(a) || !oidPattern.MatchString(a.Target) || !oidPattern.MatchString(a.Commit) || !validBranch(a.TargetBranch) || !validBranch(a.ResultBranch) {
		return fmt.Errorf("invalid acceptance plan")
	}
	current, e := Capture(ctx, t)
	if e != nil {
		return e
	}
	if current.ID != r.ID {
		return fmt.Errorf("task workspace changed; refresh the review")
	}
	repo := Repository{Root: t.Spec.Project, GitDir: t.GitDir, Common: t.Common}
	existing, e := repo.branch(ctx, a.ResultBranch)
	if e == nil {
		if existing == a.Commit {
			return nil
		}
		return fmt.Errorf("result branch was created by another operation")
	}
	tree, e := acceptanceTree(ctx, t, r, a)
	if e != nil {
		return e
	}
	actual, e := repo.run(ctx, nil, nil, "show", "-s", "--format=%T %P", a.Commit)
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(actual)) != tree+" "+a.Target {
		return fmt.Errorf("acceptance commit differs from selected tree or parent")
	}
	transaction := fmt.Sprintf("start\nverify refs/heads/%s %s\ncreate refs/heads/%s %s\nprepare\ncommit\n", a.TargetBranch, a.Target, a.ResultBranch, a.Commit)
	_, e = repo.run(ctx, nil, strings.NewReader(transaction), "update-ref", "--stdin")
	if e != nil {
		return fmt.Errorf("target moved or result branch exists; review a new acceptance plan: %w", e)
	}
	return nil
}
