// Package instanceux contains the deterministic presentation and action model
// shared by the instance CLI and terminal UI.
package instanceux

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type Predicate struct {
	Field  string
	Value  string
	Negate bool
}

type Query struct {
	Raw        string
	Predicates []Predicate
}

var queryFields = map[string]struct{}{
	"name": {}, "status": {}, "branch": {}, "task": {}, "image": {}, "id": {}, "policy": {},
}

func ParseQuery(raw string) (Query, error) {
	tokens, err := splitQuery(raw)
	if err != nil {
		return Query{}, err
	}
	query := Query{Raw: raw, Predicates: make([]Predicate, 0, len(tokens))}
	for _, token := range tokens {
		predicate := Predicate{}
		if strings.HasPrefix(token, "!") {
			predicate.Negate = true
			token = strings.TrimPrefix(token, "!")
		}
		field, value, hasField := strings.Cut(token, ":")
		if hasField {
			field = strings.ToLower(field)
			if _, ok := queryFields[field]; !ok {
				return Query{}, fmt.Errorf("unknown instance filter field %q", field)
			}
			predicate.Field, predicate.Value = field, value
		} else {
			predicate.Value = token
		}
		predicate.Value = strings.ToLower(strings.TrimSpace(predicate.Value))
		if predicate.Value == "" {
			return Query{}, fmt.Errorf("empty instance filter value")
		}
		query.Predicates = append(query.Predicates, predicate)
	}
	return query, nil
}

func splitQuery(raw string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range raw {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		current.WriteRune(r)
	}
	if escaped {
		return nil, fmt.Errorf("instance filter ends with an escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("instance filter has an unterminated quote")
	}
	flush()
	return tokens, nil
}

func (q Query) Match(worker sandbox.Worker) bool {
	for _, predicate := range q.Predicates {
		match := predicateMatch(predicate, worker)
		if predicate.Negate {
			match = !match
		}
		if !match {
			return false
		}
	}
	return true
}

func predicateMatch(predicate Predicate, worker sandbox.Worker) bool {
	contains := func(value string) bool { return strings.Contains(strings.ToLower(value), predicate.Value) }
	switch predicate.Field {
	case "name":
		return contains(worker.Name)
	case "status":
		return contains(worker.Status)
	case "branch":
		return contains(worker.Branch)
	case "task":
		return contains(worker.Task)
	case "image":
		return contains(worker.Image)
	case "id":
		return contains(worker.ID)
	case "policy":
		return contains(worker.AgentPolicy)
	default:
		return contains(worker.Name) || contains(worker.Status) || contains(worker.Branch) || contains(worker.Task) || contains(worker.Image) || contains(worker.AgentPolicy)
	}
}

func Filter(workers []sandbox.Worker, query Query) []sandbox.Worker {
	out := make([]sandbox.Worker, 0, len(workers))
	for _, worker := range workers {
		if query.Match(worker) {
			out = append(out, worker)
		}
	}
	return out
}

type SortKey string

const (
	SortName   SortKey = "name"
	SortStatus SortKey = "status"
	SortNewest SortKey = "newest"
	SortOldest SortKey = "oldest"
	SortBranch SortKey = "branch"
	SortPolicy SortKey = "policy"
)

func SortKeys() []SortKey {
	return []SortKey{SortName, SortStatus, SortNewest, SortOldest, SortBranch, SortPolicy}
}

func ParseSortKey(value string) (SortKey, error) {
	key := SortKey(strings.ToLower(strings.TrimSpace(value)))
	for _, candidate := range SortKeys() {
		if key == candidate {
			return key, nil
		}
	}
	return "", fmt.Errorf("unknown instance sort %q", value)
}

func Sort(workers []sandbox.Worker, key SortKey) []sandbox.Worker {
	out := append([]sandbox.Worker(nil), workers...)
	if key == "" {
		key = SortName
	}
	lessText := func(a, b string) bool {
		a, b = strings.ToLower(a), strings.ToLower(b)
		if a == b {
			return false
		}
		return a < b
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch key {
		case SortStatus:
			ar, br := statusRank(a.Status), statusRank(b.Status)
			if ar != br {
				return ar < br
			}
		case SortNewest:
			if a.StartedAt != nil && b.StartedAt != nil && !a.StartedAt.Equal(*b.StartedAt) {
				return a.StartedAt.After(*b.StartedAt)
			}
			if a.StartedAt != nil && b.StartedAt == nil {
				return true
			}
		case SortOldest:
			if a.StartedAt != nil && b.StartedAt != nil && !a.StartedAt.Equal(*b.StartedAt) {
				return a.StartedAt.Before(*b.StartedAt)
			}
			if a.StartedAt == nil && b.StartedAt != nil {
				return true
			}
		case SortBranch:
			if !strings.EqualFold(a.Branch, b.Branch) {
				return lessText(a.Branch, b.Branch)
			}
		case SortPolicy:
			if !strings.EqualFold(a.AgentPolicy, b.AgentPolicy) {
				return lessText(a.AgentPolicy, b.AgentPolicy)
			}
		}
		return lessText(a.Name, b.Name)
	})
	return out
}

func statusRank(status string) int {
	switch strings.ToLower(status) {
	case "running":
		return 0
	case "created", "restarting":
		return 1
	case "paused":
		return 2
	case "exited", "stopped":
		return 3
	case "dead", "error":
		return 4
	default:
		return 5
	}
}
