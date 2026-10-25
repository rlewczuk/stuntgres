package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Configuration keys equal the long flag names. Each key also reads
// STUNTGRES_<KEY> and the key of the same name in the --config file.
const (
	keyListen     = "listen"
	keyDumpdir    = "dumpdir"
	keyLogdir     = "logdir"
	keyUserfile   = "userfile"
	keyPidfile    = "pidfile"
	keyRestore    = "restore"
	keyVerbose    = "verbose"
	keyBackground = "background"
	keyQuerylog   = "querylog"
	keyReference  = "reference"
	configFlag    = "config"
	envPrefix     = "STUNTGRES"

	keyReferenceTimeout = "reference-timeout"
)

// serveKeys is every configuration key; other commands bind a subset and
// ignore the rest of a shared config file.
var serveKeys = []string{keyListen, keyDumpdir, keyLogdir, keyUserfile, keyPidfile, keyRestore, keyVerbose, keyBackground, keyQuerylog, keyReference, keyReferenceTimeout}

// envKeys maps a key to its STUNTGRES_<KEY> suffix: dashes become underscores.
var envKeys = strings.NewReplacer("-", "_")

// commandConfig resolves keys by precedence: changed flag, environment,
// config file, flag default.
type commandConfig struct{ values *viper.Viper }

// newConfig builds a fresh viper instance for one command run, reading the
// --config file when given and binding keys to flags and environment.
func newConfig(flags *pflag.FlagSet, keys ...string) (commandConfig, error) {
	values := viper.New()
	if path, _ := flags.GetString(configFlag); path != "" {
		values.SetConfigFile(path)
		if err := values.ReadInConfig(); err != nil {
			return commandConfig{}, fmt.Errorf("read config: %w", err)
		}
		for _, key := range values.AllKeys() {
			if !slices.Contains(serveKeys, key) {
				return commandConfig{}, fmt.Errorf("unknown config key %q in %s", key, path)
			}
		}
	}
	values.SetEnvPrefix(envPrefix)
	values.SetEnvKeyReplacer(envKeys)
	for _, key := range keys {
		if err := values.BindPFlag(key, flags.Lookup(key)); err != nil {
			return commandConfig{}, err
		}
		if err := values.BindEnv(key); err != nil {
			return commandConfig{}, err
		}
	}
	return commandConfig{values: values}, nil
}

// text accepts strings and integers (a bare listen port in a config file).
func (c commandConfig) text(key string) (string, error) {
	switch value := c.values.Get(key).(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case int:
		return strconv.Itoa(value), nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	default:
		return "", fmt.Errorf("%s must be a string, got %T", key, value)
	}
}

// list accepts a repeated flag, a config-file list of strings, or a
// whitespace-separated environment value.
func (c commandConfig) list(key string) ([]string, error) {
	switch value := c.values.Get(key).(type) {
	case nil:
		return nil, nil
	case []string:
		return value, nil
	case string:
		return strings.Fields(value), nil
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s entries must be strings, got %T", key, item)
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be a list of strings, got %T", key, value)
	}
}

// path resolves a non-empty value against the current directory.
func (c commandConfig) path(key string) (string, error) {
	value, err := c.text(key)
	if err != nil || value == "" {
		return value, err
	}
	return filepath.Abs(value)
}

// boolean rejects malformed text instead of treating it as false.
func (c commandConfig) boolean(key string) (bool, error) {
	switch value := c.values.Get(key).(type) {
	case nil:
		return false, nil
	case bool:
		return value, nil
	case string:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return false, fmt.Errorf("invalid %s value %q", key, value)
		}
		return parsed, nil
	default:
		return false, fmt.Errorf("%s must be a boolean, got %T", key, value)
	}
}

// childEnvironment removes configuration variables and the readiness
// descriptor so a daemon child is configured only by serveArguments.
func childEnvironment(environment []string) []string {
	child := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name == envPrefix+"_READY_FD" || isConfigVariable(name) {
			continue
		}
		child = append(child, entry)
	}
	return child
}

func isConfigVariable(name string) bool {
	return slices.ContainsFunc(serveKeys, func(key string) bool {
		return name == envPrefix+"_"+strings.ToUpper(envKeys.Replace(key))
	})
}
