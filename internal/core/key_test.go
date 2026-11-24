package core

import (
	"strings"
	"testing"
)

func TestHashKey(t *testing.T) {
	// SHA-256 of "abc".
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := HashKey("abc"); got != want {
		t.Fatalf("HashKey(abc) = %s, want %s", got, want)
	}
	first, second := HashKey("lp_x"), HashKey("lp_x")
	if first != second || first == HashKey("lp_y") {
		t.Fatal("HashKey must be deterministic and input-sensitive")
	}
	if !keyHashRE.MatchString(HashKey("")) {
		t.Fatal("HashKey must be 64 lowercase hex characters")
	}
}

func TestValidateKeys(t *testing.T) {
	a, b := HashKey("lp_a"), HashKey("lp_b")
	tests := []struct {
		name string
		keys []Key
		want []string // substrings, one per expected error
	}{
		{"valid", []Key{{Name: "a", SHA256: a}, {Name: "b", SHA256: b}}, nil},
		{"empty", nil, nil},
		{"project", []Key{{Name: "a", SHA256: a, Project: "spyglass"}}, nil},
		{"project of 128 characters", []Key{{Name: "a", SHA256: a, Project: strings.Repeat("p", 128)}}, nil},
		{"project too long", []Key{{Name: "a", SHA256: a, Project: strings.Repeat("p", 129)}}, []string{"keys[0]: project must be 1 to 128 printable characters"}},
		{"project with a control character", []Key{{Name: "a", SHA256: a, Project: "a\tb"}}, []string{"keys[0]: project must be"}},
		{"project with a space at the end", []Key{{Name: "a", SHA256: a, Project: "a "}}, []string{"keys[0]: project must be"}},
		{"missing name", []Key{{SHA256: a}}, []string{"keys[0]: name is required"}},
		{"duplicate name", []Key{{Name: "x", SHA256: a}, {Name: "x", SHA256: b}}, []string{`keys[1]: duplicate name "x" (also keys[0])`}},
		{"bad hash", []Key{{Name: "x", SHA256: "ABC"}}, []string{"keys[0]: sha256 must be 64 lowercase hex characters"}},
		{"uppercase hash", []Key{{Name: "x", SHA256: strings.ToUpper(a)}}, []string{"sha256 must be 64 lowercase hex"}},
		{"duplicate hash", []Key{{Name: "x", SHA256: a}, {Name: "y", SHA256: a}}, []string{"keys[1]: duplicate sha256 (also keys[0])"}},
		{"all problems listed", []Key{{Name: "x", SHA256: a}, {Name: "x", SHA256: a}, {SHA256: "z"}}, []string{"duplicate name", "duplicate sha256", "name is required", "sha256 must be"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateKeys(tt.keys)
			if len(errs) != len(tt.want) {
				t.Fatalf("got %d errors %v, want %d", len(errs), errs, len(tt.want))
			}
			for i, w := range tt.want {
				if !strings.Contains(errs[i].Error(), w) {
					t.Errorf("error %d = %q, want it to contain %q", i, errs[i], w)
				}
			}
			for _, e := range errs {
				if strings.Contains(e.Error(), a) {
					t.Errorf("error leaks a hash: %q", e)
				}
			}
		})
	}
}
