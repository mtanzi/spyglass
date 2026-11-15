package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mtanzi/spyglass/internal/core"
)

// EnvConfigPath names the environment variable that holds the config file path
// when --config is not given.
const EnvConfigPath = EnvPrefix + "_CONFIG"

// Provider kinds and auth modes.
const (
	KindAnthropic        = "anthropic"
	KindOpenAICompatible = "openai_compatible"

	AuthProxyKey          = "proxy_key"
	AuthClientPassthrough = "client_passthrough"
)

// Config is the validated content of the configuration file. Load returns it
// fully resolved: defaults applied, environment overrides merged, secrets read.
type Config struct {
	Listen        Listen     `yaml:"listen"`
	Database      Database   `yaml:"database"`
	Providers     []Provider `yaml:"providers"`
	Keys          []core.Key `yaml:"keys"`
	Pricing       []Pricing  `yaml:"pricing"`
	Retention     Retention  `yaml:"retention"`
	Server        Server     `yaml:"server"`
	QueueCapacity int        `yaml:"queue_capacity"`
	Log           Log        `yaml:"log"`
}

// Listen holds the listener addresses. Changing them needs a restart.
type Listen struct {
	Data  string `yaml:"data"`
	Admin string `yaml:"admin"` // must be a loopback address
	TLS   TLS    `yaml:"tls"`
}

// TLS is the optional certificate pair for the data listener. Both fields empty
// means plain HTTP.
type TLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Enabled reports whether a certificate pair is configured.
func (t TLS) Enabled() bool { return t.CertFile != "" || t.KeyFile != "" }

// Database holds the Postgres connection settings.
type Database struct {
	URL      Ref `yaml:"url"`
	MaxConns int `yaml:"max_conns"`
}

// Provider is one upstream provider. v1 takes exactly one, of kind anthropic.
// Kind openai_compatible, credential none and headers_to_add are v2 settings:
// the fields stay so that validation can reject them by name.
type Provider struct {
	Name         string            `yaml:"name"`
	Kind         string            `yaml:"kind"`
	BaseURL      string            `yaml:"base_url"`
	AuthMode     string            `yaml:"auth_mode"`
	Credential   Credential        `yaml:"credential"`
	IdleTimeout  Duration          `yaml:"idle_timeout"`
	HeadersToAdd map[string]string `yaml:"headers_to_add"`
}

// Credential is the upstream credential of a provider: a secret reference, or
// the word none for a local backend that needs no credential.
type Credential struct {
	Ref  Ref
	None bool
}

// String describes the source, never the value.
func (c Credential) String() string {
	if c.None {
		return "none"
	}
	return c.Ref.String()
}

// GoString implements fmt.GoStringer.
func (c Credential) GoString() string { return "config.Credential{" + c.String() + "}" }

// Format implements fmt.Formatter so the resolved value never prints.
func (c Credential) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(c.String())) }

// UnmarshalYAML implements yaml.Unmarshaler.
func (c *Credential) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Value == "none" {
		*c = Credential{None: true}
		return nil
	}
	*c = Credential{}
	return c.Ref.UnmarshalYAML(n)
}

// Pricing is one price version for a model: it applies from the From date until
// the next version of the same provider and model. Prices are US dollars per
// million tokens.
type Pricing struct {
	Provider     string `yaml:"provider"`
	Model        string `yaml:"model"`
	From         Date   `yaml:"from"`
	Input        Rate   `yaml:"input"`
	Output       Rate   `yaml:"output"`
	CacheRead    Rate   `yaml:"cache_read"`
	CacheWrite5m Rate   `yaml:"cache_write_5m"`
	CacheWrite1h Rate   `yaml:"cache_write_1h"`
}

// Price returns the per-column prices as core.Price, the type core.Cost takes.
func (p Pricing) Price() core.Price {
	return core.Price{
		Input:        p.Input.MicroUSD(),
		Output:       p.Output.MicroUSD(),
		CacheRead:    p.CacheRead.MicroUSD(),
		CacheWrite5m: p.CacheWrite5m.MicroUSD(),
		CacheWrite1h: p.CacheWrite1h.MicroUSD(),
	}
}

// Retention holds how long records are kept.
type Retention struct {
	Calls Duration `yaml:"calls"` // calls and tool_events
}

// Server holds request and shutdown limits.
type Server struct {
	DrainTimeout    Duration `yaml:"drain_timeout"`
	MaxRequestBytes Size     `yaml:"max_request_bytes"`
}

// Log holds logger settings.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// Options injects the environment and the file system, so tests need neither.
// The zero value uses the real ones.
type Options struct {
	LookupEnv func(string) (string, bool)
	ReadFile  func(string) ([]byte, error)

	// BuildKeys builds the key index that travels with each Snapshot from the
	// validated keys. The composition root passes keyring.New; config cannot
	// import keyring. When nil, a Snapshot has no key index. A build error
	// rejects the config like a validation error.
	BuildKeys func([]core.Key) (KeyIndex, error)
}

func (o Options) withDefaults() Options {
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.ReadFile == nil {
		o.ReadFile = os.ReadFile
	}
	return o
}

// Path returns the config file path: the --config value when set, otherwise
// SPYGLASS_CONFIG.
func Path(flagValue string, o Options) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if v, ok := o.withDefaults().LookupEnv(EnvConfigPath); ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no config file: pass --config or set %s", EnvConfigPath)
}

// Load reads and validates the config file at path, and reads every secret it
// references. Call it again to reload: files and secrets are read afresh.
func Load(path string, o Options) (*Config, error) {
	o = o.withDefaults()
	data, err := o.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data, o)
}

// Parse decodes, overlays, resolves and validates config text. See Load.
func Parse(data []byte, o Options) (*Config, error) {
	o = o.withDefaults()
	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse config: more than one YAML document")
	}

	errs := applyEnv(&cfg, o)
	cfg.normalize()
	errs = append(errs, cfg.validate()...)
	errs = append(errs, cfg.resolve(o)...)
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid config:\n  %w", joinLines(errs))
	}
	return &cfg, nil
}

func joinLines(errs []error) error {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return errors.New(strings.Join(msgs, "\n  "))
}

func defaults() Config {
	return Config{
		Listen:        Listen{Data: ":8080", Admin: "127.0.0.1:9090"},
		Database:      Database{MaxConns: 20},
		Retention:     Retention{Calls: Duration(400 * 24 * time.Hour)},
		Server:        Server{DrainTimeout: Duration(10 * time.Minute), MaxRequestBytes: 32 << 20},
		QueueCapacity: 10000,
		Log:           Log{Level: "info", Format: "json"},
	}
}

// normalize fills the defaults of list entries.
func (c *Config) normalize() {
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.AuthMode == "" {
			p.AuthMode = AuthProxyKey
		}
		if p.IdleTimeout == 0 {
			p.IdleTimeout = Duration(120 * time.Second)
		}
	}
}

// resolve reads every secret reference. All failures are returned.
func (c *Config) resolve(o Options) []error {
	var errs []error
	if err := c.Database.URL.resolve("database.url", o); err != nil {
		errs = append(errs, err)
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.Credential.None || (p.Credential.Ref.IsZero() && p.AuthMode == AuthClientPassthrough) {
			continue
		}
		if err := p.Credential.Ref.resolve(fmt.Sprintf("providers[%d].credential", i), o); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// PriceVersions returns the pricing entries as core.PriceVersion, the type
// core.SelectPrice and core.CostAt take.
func (c *Config) PriceVersions() []core.PriceVersion {
	vs := make([]core.PriceVersion, len(c.Pricing))
	for i, p := range c.Pricing {
		vs[i] = core.PriceVersion{Provider: p.Provider, Model: p.Model, From: p.From.Time(), Price: p.Price()}
	}
	return vs
}
