package serverref

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/serverauth"
	"time"
)

const DefaultTimeout = 30 * time.Second

// Config is one --reference DB=URL entry: the stuntgres database that
// runs in dual-backend mode and the PostgreSQL connection it is compared with.
// It is immutable after parsing.
type Config struct {
	Spec        string // the original DB=URL argument, reproduced for the daemon child
	Database    string // stuntgres database
	Host, Port  string
	User        string
	Password    string
	PasswordSet bool
	DBName      string
	SSLMode     string
}

// Parse parses DB=postgres://user[:password]@host[:port][/dbname][?sslmode=MODE].
func Parse(spec string) (Config, error) {
	database, raw, ok := strings.Cut(spec, "=")
	if !ok || database == "" || raw == "" {
		return Config{}, fmt.Errorf("invalid reference %q: expected DB=URL", redactedSpec(spec))
	}
	if err := serverauth.ValidName(database, true); err != nil {
		return Config{}, fmt.Errorf("invalid reference database: %w", err)
	}
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("invalid reference for database %q: %s", database, fmt.Sprintf(format, args...))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Config{}, invalid("malformed URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return Config{}, invalid("URL scheme must be postgres or postgresql")
	}
	if u.Opaque != "" || u.Fragment != "" {
		return Config{}, invalid("malformed URL")
	}
	c := Config{Spec: spec, Database: database, Host: u.Hostname(), Port: u.Port(), SSLMode: "disable"}
	if c.Host == "" {
		return Config{}, invalid("host is required")
	}
	if c.Port == "" {
		c.Port = "5432"
	}
	if n, err := strconv.Atoi(c.Port); err != nil || n < 1 || n > 65535 {
		return Config{}, invalid("port must be between 1 and 65535")
	}
	if u.User == nil || u.User.Username() == "" {
		return Config{}, invalid("user is required")
	}
	c.User = u.User.Username()
	c.Password, c.PasswordSet = u.User.Password()
	c.DBName = strings.TrimPrefix(u.Path, "/")
	if c.DBName == "" {
		c.DBName = database
	}
	if strings.Contains(c.DBName, "/") {
		return Config{}, invalid("malformed database name")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Config{}, invalid("malformed query")
	}
	for key, values := range query {
		if key != "sslmode" {
			return Config{}, invalid("unsupported parameter %q", key)
		}
		if len(values) != 1 {
			return Config{}, invalid("sslmode must be given once")
		}
		c.SSLMode = values[0]
	}
	switch c.SSLMode {
	case "disable", "require", "verify-full":
	default:
		return Config{}, invalid("sslmode must be disable, require or verify-full")
	}
	return c, nil
}

// ParseAll parses every --reference entry; each database may appear once.
func ParseAll(specs []string) (map[string]Config, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	references := make(map[string]Config, len(specs))
	for _, spec := range specs {
		c, err := Parse(spec)
		if err != nil {
			return nil, err
		}
		if _, exists := references[c.Database]; exists {
			return nil, fmt.Errorf("duplicate reference for database %q", c.Database)
		}
		references[c.Database] = c
	}
	return references, nil
}

// Specs returns the original entries in database order.
func Specs(references map[string]Config) []string {
	specs := make([]string, 0, len(references))
	for _, c := range references {
		specs = append(specs, c.Spec)
	}
	slices.Sort(specs)
	return specs
}

// Address is the reference host and port for dialing.
func (c *Config) Address() string { return net.JoinHostPort(c.Host, c.Port) }

// Display is the reference URL without its password, for operational logs.
func (c *Config) Display() string {
	u := url.URL{Scheme: "postgres", User: url.User(c.User), Host: c.Address(), Path: "/" + c.DBName}
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

func ValidateTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return errors.New("reference timeout must be positive")
	}
	return nil
}
