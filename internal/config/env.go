package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// EnvPrefix starts the name of every environment variable the proxy reads.
// The LLM_PROXY_ prefix of release 0.1.0 is no longer read (ADR 0054).
const EnvPrefix = "SPYGLASS"

// applyEnv overrides scalar keys from SPYGLASS_<SECTION>_<KEY> variables. The
// names come from the yaml tags of Config, so every key has one. Lists and maps
// are skipped. A secret reference is replaced whole by its _ENV and _FILE
// parts, so overriding one part drops the other part set in the file.
func applyEnv(cfg *Config, o Options) []error {
	var errs []error
	walkLeaves(reflect.ValueOf(cfg).Elem(), []string{EnvPrefix}, func(name string, v reflect.Value) {
		if v.Type() == refType {
			if ref, ok := refFromEnv(name, o); ok {
				v.Set(reflect.ValueOf(ref))
			}
			return
		}
		raw, ok := o.LookupEnv(name)
		if !ok {
			return
		}
		if err := setLeaf(v, raw); err != nil {
			errs = append(errs, fmt.Errorf("environment variable %s: %w", name, err))
		}
	})
	return errs
}

// refFromEnv reads the _ENV and _FILE parts of the secret reference name. It
// returns false when no part is set.
func refFromEnv(name string, o Options) (Ref, bool) {
	env, envOK := o.LookupEnv(name + "_ENV")
	file, fileOK := o.LookupEnv(name + "_FILE")
	return Ref{Env: env, File: file}, envOK || fileOK
}

var (
	durationType = reflect.TypeFor[Duration]()
	sizeType     = reflect.TypeFor[Size]()
	refType      = reflect.TypeFor[Ref]()
)

// walkLeaves calls fn with the variable name and field of each overridable key.
func walkLeaves(v reflect.Value, path []string, fn func(name string, v reflect.Value)) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if !f.IsExported() || tag == "" || tag == "-" {
			continue
		}
		p := append(path[:len(path):len(path)], strings.ToUpper(tag))
		fv := v.Field(i)
		switch {
		case f.Type == durationType, f.Type == sizeType, f.Type == refType:
			fn(strings.Join(p, "_"), fv)
		case f.Type.Kind() == reflect.Struct:
			walkLeaves(fv, p, fn)
		case f.Type.Kind() == reflect.String, f.Type.Kind() == reflect.Int, f.Type.Kind() == reflect.Bool:
			fn(strings.Join(p, "_"), fv)
		}
	}
}

func setLeaf(v reflect.Value, raw string) error {
	switch v.Type() {
	case durationType:
		d, err := ParseDuration(raw)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(d))
		return nil
	case sizeType:
		s, err := ParseSize(raw)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(s))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("invalid number %q", raw)
		}
		v.SetInt(int64(n))
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid boolean %q", raw)
		}
		v.SetBool(b)
	}
	return nil
}
