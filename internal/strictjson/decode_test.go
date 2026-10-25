package strictjson

import (
	"strings"
	"testing"
)

func TestManagementJSONRejectsAmbiguity(t *testing.T) {
	for _, input := range []string{`{"container":"a","container":"b"}`, `{"container":"a","Container":"b"}`, `{"container":"a"} {"container":"b"}`, `{"container":"a","unknown":1}`, strings.Repeat("[", 66) + strings.Repeat("]", 66), `{"container":"` + strings.Repeat("x", 1024) + `"}`} {
		var dst struct {
			Container string `json:"container"`
		}
		if err := Decode(strings.NewReader(input), 1024, &dst); err == nil {
			t.Fatalf("ambiguous or oversized JSON accepted: %.90s", input)
		}
	}
	var dst struct {
		Container string `json:"container"`
	}
	if err := Decode(strings.NewReader(`{"container":"a"}`), 1024, &dst); err != nil || dst.Container != "a" {
		t.Fatal(err)
	}
}
func FuzzManagementJSON(f *testing.F) {
	for _, seed := range []string{`{"container":"a"}`, `{"container":"a","Container":"b"}`, `{"container":"a"}{}`, `null`, `[]`, `{"container":"\ud800"}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 128<<10 {
			t.Skip()
		}
		var dst struct {
			Container string `json:"container"`
		}
		err := Decode(strings.NewReader(input), 96<<10, &dst)
		if err == nil {
			var next struct {
				Container string `json:"container"`
			}
			if Decode(strings.NewReader(input+" {}"), 96<<10, &next) == nil {
				t.Fatal("appended JSON accepted")
			}
			trimmed := strings.TrimSpace(input)
			if strings.HasPrefix(trimmed, "{") {
				prefix := trimmed[:len(trimmed)-1]
				if strings.TrimSpace(prefix) != "{" {
					prefix += ","
				}
				collision := prefix + `"container":"first","Container":"second"}`
				if Decode(strings.NewReader(collision), 96<<10, &next) == nil {
					t.Fatal("injected duplicate field accepted")
				}
				unknown := prefix + `"unrecognized_management_field":true}`
				if Decode(strings.NewReader(unknown), 96<<10, &next) == nil {
					t.Fatal("injected unknown field accepted")
				}
			}
		}
	})
}

func TestUnicodeFieldAliasesAreDuplicate(t *testing.T) {
	var dst struct {
		CaseID string `json:"case_id"`
	}
	if err := Decode(strings.NewReader(`{"case_id":"first","caſe_id":"second"}`), 1024, &dst); err == nil {
		t.Fatal("Unicode alias duplicate accepted")
	}
}
