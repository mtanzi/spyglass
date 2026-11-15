package config

import (
	"fmt"
	"log/slog"
	"strings"

	"go.yaml.in/yaml/v3"
)

const redacted = "[redacted]"

// Secret is a resolved secret value. It prints as [redacted] under every fmt
// verb, in slog and in JSON. Reveal is the only way to read it.
type Secret struct{ v string }

// Reveal returns the secret value. Pass it straight to the code that needs it
// and never log it.
func (s Secret) Reveal() string { return s.v }

// IsSet reports whether s holds a value.
func (s Secret) IsSet() bool { return s.v != "" }

// String implements fmt.Stringer.
func (Secret) String() string { return redacted }

// GoString implements fmt.GoStringer.
func (Secret) GoString() string { return redacted }

// Format implements fmt.Formatter so no verb prints the value.
func (Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText implements encoding.TextMarshaler for JSON and similar encoders.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Ref names where a secret comes from: an environment variable or a file,
// exactly one. After Load, the resolved value is held privately and read with
// Secret.
type Ref struct {
	Env  string `yaml:"env"`
	File string `yaml:"file"`

	secret Secret
}

// UnmarshalYAML implements yaml.Unmarshaler. A scalar is refused without echoing
// it, since an inlined secret must not reach an error message. The decoder does
// not pass strict mode into a custom unmarshaler, so the keys are checked here.
func (r *Ref) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: want { env: NAME } or { file: PATH }; secrets are never inlined", n.Line)
	}
	for i := 0; i < len(n.Content); i += 2 {
		if k := n.Content[i].Value; k != "env" && k != "file" {
			return fmt.Errorf("line %d: field %s not found in secret reference", n.Content[i].Line, k)
		}
	}
	type plain Ref
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*r = Ref(p)
	return nil
}

// Secret returns the value resolved at load time.
func (r Ref) Secret() Secret { return r.secret }

// IsZero reports whether neither env nor file is set.
func (r Ref) IsZero() bool { return r.Env == "" && r.File == "" }

// String describes the source, never the value.
func (r Ref) String() string {
	switch {
	case r.Env != "" && r.File != "":
		return "env:" + r.Env + ",file:" + r.File
	case r.Env != "":
		return "env:" + r.Env
	case r.File != "":
		return "file:" + r.File
	}
	return "unset"
}

// GoString implements fmt.GoStringer.
func (r Ref) GoString() string { return "config.Ref{" + r.String() + "}" }

// Format implements fmt.Formatter. The resolved value sits in an unexported
// field that fmt would otherwise print through reflection.
func (r Ref) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(r.String())) }

// LogValue implements slog.LogValuer.
func (r Ref) LogValue() slog.Value { return slog.StringValue(r.String()) }

// validate checks the shape of the reference.
func (r Ref) validate(path string) error {
	switch {
	case r.Env != "" && r.File != "":
		return fmt.Errorf("%s: set only one of env and file", path)
	case r.IsZero():
		return fmt.Errorf("%s: set env or file", path)
	}
	return nil
}

// resolve reads the secret. A missing, empty or unreadable source is an error;
// the error names the source, never a value.
func (r *Ref) resolve(path string, o Options) error {
	if err := r.validate(path); err != nil {
		return err
	}
	var raw string
	if r.Env != "" {
		v, ok := o.LookupEnv(r.Env)
		if !ok {
			return fmt.Errorf("%s: environment variable %s is not set", path, r.Env)
		}
		raw = v
	} else {
		b, err := o.ReadFile(r.File)
		if err != nil {
			return fmt.Errorf("%s: read secret file: %w", path, err)
		}
		raw = string(b)
	}
	raw = strings.TrimRight(raw, "\r\n")
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s: secret from %s is empty", path, r.String())
	}
	r.secret = Secret{raw}
	return nil
}
