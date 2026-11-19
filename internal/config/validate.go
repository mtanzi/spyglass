package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/mtanzi/spyglass/internal/core"
)

// validate checks every rule that needs no secret. It returns all problems.
func (c *Config) validate() []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if err := validAddr(c.Listen.Data); err != nil {
		add("listen.data: %v", err)
	}
	if err := validAddr(c.Listen.Admin); err != nil {
		add("listen.admin: %v", err)
	} else if !IsLoopback(c.Listen.Admin) {
		add("listen.admin: %q is not a loopback address; the admin plane must listen on 127.0.0.1, ::1 or localhost", c.Listen.Admin)
	}
	if (c.Listen.TLS.CertFile == "") != (c.Listen.TLS.KeyFile == "") {
		add("listen.tls: set both cert_file and key_file, or neither")
	}

	if c.Database.MaxConns < 1 {
		add("database.max_conns: must be at least 1, got %d", c.Database.MaxConns)
	}

	errs = append(errs, c.validateProviders()...)
	errs = append(errs, c.validateKeys()...)
	errs = append(errs, c.validatePricing()...)

	if c.Retention.Calls <= 0 {
		add("retention.calls: must be greater than zero")
	}
	if c.Server.DrainTimeout <= 0 {
		add("server.drain_timeout: must be greater than zero")
	}
	if c.Server.MaxRequestBytes <= 0 {
		add("server.max_request_bytes: must be greater than zero")
	}
	if c.QueueCapacity < 1 {
		add("queue_capacity: must be at least 1, got %d", c.QueueCapacity)
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		add("log.level: unknown level %q; want debug, info, warn or error", c.Log.Level)
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		add("log.format: unknown format %q; want json or text", c.Log.Format)
	}
	return errs
}

func (c *Config) validateProviders() []error {
	var errs []error
	add := func(i int, format string, args ...any) {
		errs = append(errs, fmt.Errorf("providers[%d]: %s", i, fmt.Sprintf(format, args...)))
	}
	if n := len(c.Providers); n != 1 {
		errs = append(errs, fmt.Errorf("providers: v1 needs exactly one provider, of kind %s; got %d", KindAnthropic, n))
	}
	for i, p := range c.Providers {
		if p.Name == "" {
			add(i, "name is required")
		}
		switch p.Kind {
		case KindAnthropic:
		case KindOpenAICompatible:
			add(i, "kind %s is v2; v1 supports only kind %s", KindOpenAICompatible, KindAnthropic)
		default:
			add(i, "unknown kind %q; want %s", p.Kind, KindAnthropic)
		}
		if u, err := url.Parse(p.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			add(i, "base_url must be an absolute http or https URL without credentials")
		}
		switch p.AuthMode {
		case AuthProxyKey, AuthClientPassthrough:
		default:
			add(i, "unknown auth_mode %q; want %s or %s", p.AuthMode, AuthProxyKey, AuthClientPassthrough)
		}
		switch {
		case p.Credential.None:
			add(i, "credential none is v2; v1 needs a credential, or auth_mode %s", AuthClientPassthrough)
		case !p.Credential.Ref.IsZero():
		case p.AuthMode != AuthClientPassthrough:
			add(i, "credential is required for auth_mode %s", p.AuthMode)
		}
		if p.IdleTimeout <= 0 {
			add(i, "idle_timeout must be greater than zero")
		}
		if len(p.HeadersToAdd) > 0 {
			add(i, "headers_to_add is v2; v1 does not apply it")
		}
	}
	return errs
}

func (c *Config) validateKeys() []error { return core.ValidateKeys(c.Keys) }

func (c *Config) validatePricing() []error {
	var errs []error
	add := func(i int, format string, args ...any) {
		errs = append(errs, fmt.Errorf("pricing[%d]: %s", i, fmt.Sprintf(format, args...)))
	}
	type version struct{ provider, model, from string }
	providers := map[string]bool{}
	for _, p := range c.Providers {
		providers[p.Name] = true
	}
	seen := map[version]int{}
	for i, p := range c.Pricing {
		switch {
		case p.Provider == "":
			add(i, "provider is required")
		case !providers[p.Provider]:
			add(i, "provider %q is not declared in providers", p.Provider)
		}
		if p.Model == "" {
			add(i, "model is required")
		}
		if p.From.Time().IsZero() {
			add(i, "from is required (YYYY-MM-DD)")
			continue
		}
		v := version{p.Provider, p.Model, p.From.String()}
		if j, dup := seen[v]; dup {
			add(i, "overlaps pricing[%d]: same provider, model and from date %s", j, v.from)
		} else {
			seen[v] = i
		}
	}
	return errs
}

// validAddr checks host:port with a numeric port; the host may be empty.
func validAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q must be host:port", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q has an invalid port", addr)
	}
	return nil
}

// IsLoopback reports whether addr names only the loopback interface. An empty
// host listens on every interface and so is not loopback.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
