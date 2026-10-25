package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const defaultReferenceTimeout = 30 * time.Second

// referenceConfig is one --reference DB=URL entry: the stuntgres database that
// runs in dual-backend mode and the PostgreSQL connection it is compared with.
// It is immutable after parsing.
type referenceConfig struct {
	spec        string // the original DB=URL argument, reproduced for the daemon child
	database    string // stuntgres database
	host, port  string
	user        string
	password    string
	passwordSet bool
	dbname      string
	sslmode     string
}

// parseReference parses DB=postgres://user[:password]@host[:port][/dbname][?sslmode=MODE].
func parseReference(spec string) (referenceConfig, error) {
	database, raw, ok := strings.Cut(spec, "=")
	if !ok || database == "" || raw == "" {
		return referenceConfig{}, fmt.Errorf("invalid reference %q: expected DB=URL", redactedSpec(spec))
	}
	if err := validName(database, true); err != nil {
		return referenceConfig{}, fmt.Errorf("invalid reference database: %w", err)
	}
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("invalid reference for database %q: %s", database, fmt.Sprintf(format, args...))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return referenceConfig{}, invalid("malformed URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return referenceConfig{}, invalid("URL scheme must be postgres or postgresql")
	}
	if u.Opaque != "" || u.Fragment != "" {
		return referenceConfig{}, invalid("malformed URL")
	}
	c := referenceConfig{spec: spec, database: database, host: u.Hostname(), port: u.Port(), sslmode: "disable"}
	if c.host == "" {
		return referenceConfig{}, invalid("host is required")
	}
	if c.port == "" {
		c.port = "5432"
	}
	if n, err := strconv.Atoi(c.port); err != nil || n < 1 || n > 65535 {
		return referenceConfig{}, invalid("port must be between 1 and 65535")
	}
	if u.User == nil || u.User.Username() == "" {
		return referenceConfig{}, invalid("user is required")
	}
	c.user = u.User.Username()
	c.password, c.passwordSet = u.User.Password()
	c.dbname = strings.TrimPrefix(u.Path, "/")
	if c.dbname == "" {
		c.dbname = database
	}
	if strings.Contains(c.dbname, "/") {
		return referenceConfig{}, invalid("malformed database name")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return referenceConfig{}, invalid("malformed query")
	}
	for key, values := range query {
		if key != "sslmode" {
			return referenceConfig{}, invalid("unsupported parameter %q", key)
		}
		if len(values) != 1 {
			return referenceConfig{}, invalid("sslmode must be given once")
		}
		c.sslmode = values[0]
	}
	switch c.sslmode {
	case "disable", "require", "verify-full":
	default:
		return referenceConfig{}, invalid("sslmode must be disable, require or verify-full")
	}
	return c, nil
}

// parseReferences parses every --reference entry; each database may appear once.
func parseReferences(specs []string) (map[string]referenceConfig, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	references := make(map[string]referenceConfig, len(specs))
	for _, spec := range specs {
		c, err := parseReference(spec)
		if err != nil {
			return nil, err
		}
		if _, exists := references[c.database]; exists {
			return nil, fmt.Errorf("duplicate reference for database %q", c.database)
		}
		references[c.database] = c
	}
	return references, nil
}

// referenceSpecs returns the original entries in database order.
func referenceSpecs(references map[string]referenceConfig) []string {
	specs := make([]string, 0, len(references))
	for _, c := range references {
		specs = append(specs, c.spec)
	}
	slices.Sort(specs)
	return specs
}

// address is the reference host and port for dialing.
func (c *referenceConfig) address() string { return net.JoinHostPort(c.host, c.port) }

// display is the reference URL without its password, for operational logs.
func (c *referenceConfig) display() string {
	u := url.URL{Scheme: "postgres", User: url.User(c.user), Host: c.address(), Path: "/" + c.dbname}
	return u.String()
}

// redactedSpec hides a URL password in a rejected entry before it is reported.
func redactedSpec(spec string) string {
	database, raw, _ := strings.Cut(spec, "=")
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		if _, set := u.User.Password(); set {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
			return database + "=" + u.String()
		}
	}
	return spec
}

func validateReferenceTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return errors.New("reference timeout must be positive")
	}
	return nil
}
