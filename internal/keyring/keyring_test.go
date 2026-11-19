package keyring

import (
	"strings"
	"testing"

	"github.com/mtanzi/spyglass/internal/core"
)

func TestLookup(t *testing.T) {
	r, err := New([]core.Key{
		{Name: "a", SHA256: core.HashKey("lp_a")},
		{Name: "b", SHA256: core.HashKey("lp_b"), Disabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 2 {
		t.Errorf("Len = %d", r.Len())
	}
	if k, ok := r.Lookup(core.HashKey("lp_a")); !ok || k.Name != "a" || k.Disabled {
		t.Errorf("known key: %+v %v", k, ok)
	}
	if k, ok := r.Lookup(core.HashKey("lp_b")); !ok || !k.Disabled {
		t.Errorf("disabled key: %+v %v", k, ok)
	}
	if _, ok := r.Lookup(core.HashKey("lp_c")); ok {
		t.Error("unknown key found")
	}
}

func TestEmpty(t *testing.T) {
	r, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup(core.HashKey("x")); ok {
		t.Error("empty keyring found a key")
	}
}

func TestNewRejects(t *testing.T) {
	h := core.HashKey("x")
	cases := []struct {
		name string
		keys []core.Key
		want string
	}{
		{"duplicate name", []core.Key{{Name: "a", SHA256: h}, {Name: "a", SHA256: core.HashKey("y")}}, "duplicate name"},
		{"duplicate hash", []core.Key{{Name: "a", SHA256: h}, {Name: "b", SHA256: h}}, "duplicate sha256"},
		{"short hash", []core.Key{{Name: "a", SHA256: "abc"}}, "64 lowercase hex"},
		{"uppercase hash", []core.Key{{Name: "a", SHA256: strings.ToUpper(h)}}, "64 lowercase hex"},
		{"non-hex hash", []core.Key{{Name: "a", SHA256: strings.Repeat("z", 64)}}, "64 lowercase hex"},
		{"empty name", []core.Key{{SHA256: h}}, "name is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := New(c.keys)
			if err == nil || r != nil {
				t.Fatalf("New = %v, %v; want error", r, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q lacks %q", err, c.want)
			}
		})
	}
}
