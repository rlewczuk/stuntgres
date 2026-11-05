package remote

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
)

// Conninfo is a parsed admission value, not a DSN, connection or capability.
// It retains only explicit-password presence and an owned port value.
type Conninfo struct {
	PasswordPresent bool
	port            string
}

// ParseConninfo matches syntax-only PQconninfoParse credential admission. It
// never resolves defaults or retains the original text/password. Numeric
// connection-option validation belongs AFTER named duplicate admission.
func ParseConninfo(text string) (Conninfo, error) {
	var values Conninfo
	var err error
	if strings.HasPrefix(text, "postgresql://") || strings.HasPrefix(text, "postgres://") {
		err = parseConninfoURI(text, &values)
	} else {
		err = parseConninfoKeywords(text, &values)
	}
	if err != nil {
		return Conninfo{}, err
	}
	return values, nil
}
func (c Conninfo) Validate() error {
	for part := range strings.SplitSeq(c.port, ",") {
		if part == "" {
			continue
		}
		port, err := strconv.ParseInt(strings.TrimSpace(part), 10, 32)
		if err != nil {
			return fmt.Errorf("invalid integer value \"%s\" for connection option \"port\"", part)
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("invalid port number: \"%s\"", part)
		}
	}
	return nil
}
func conninfoSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// Released libpq keywords, not arbitrary runtime settings.
func knownConninfoOption(key string) bool {
	switch key {
	case "service", "user", "password", "passfile", "channel_binding", "connect_timeout",
		"dbname", "host", "hostaddr", "port", "client_encoding", "options",
		"application_name", "fallback_application_name", "keepalives", "keepalives_idle",
		"keepalives_interval", "keepalives_count", "tcp_user_timeout", "sslmode",
		"requiressl", "sslnegotiation", "sslcompression", "sslcert", "sslkey",
		"sslpassword", "sslcertmode", "sslrootcert", "sslcrl", "sslcrldir", "sslsni",
		"requirepeer", "require_auth", "ssl_min_protocol_version", "ssl_max_protocol_version",
		"gssencmode", "krbsrvname", "gsslib", "gssdelegation", "replication",
		"target_session_attrs", "load_balance_hosts", "oauth_issuer", "oauth_client_id",
		"oauth_client_secret", "oauth_scope":
		return true
	}
	return false
}
func conninfoOption(values *Conninfo, key, value string) error {
	if !knownConninfoOption(key) {
		return fmt.Errorf("invalid connection option \"%s\"", key)
	}
	switch key {
	case "password":
		values.PasswordPresent = value != ""
	case "port":
		values.port = value
	}
	return nil
}
func parseConninfoKeywords(text string, values *Conninfo) error {
	for i := 0; i < len(text); {
		for i < len(text) && conninfoSpace(text[i]) {
			i++
		}
		if i == len(text) {
			return nil
		}
		start := i
		for i < len(text) && !conninfoSpace(text[i]) && text[i] != '=' {
			i++
		}
		key := text[start:i]
		for i < len(text) && conninfoSpace(text[i]) {
			i++
		}
		if i == len(text) || text[i] != '=' {
			return fmt.Errorf("missing \"=\" after \"%s\" in connection info string", key)
		}
		i++
		for i < len(text) && conninfoSpace(text[i]) {
			i++
		}
		var value strings.Builder
		retain := key == "port"
		nonempty := false
		quoted := i < len(text) && text[i] == '\''
		if quoted {
			i++
		}
		closed := !quoted
		for i < len(text) {
			c := text[i]
			if quoted && c == '\'' {
				i++
				closed = true
				break
			}
			if !quoted && conninfoSpace(c) {
				break
			}
			i++
			if c == '\\' {
				if i == len(text) {
					break
				}
				c = text[i]
				i++
			}
			nonempty = true
			if retain {
				value.WriteByte(c)
			}
		}
		if !closed {
			return fmt.Errorf("unterminated quoted string in connection info string")
		}
		if key == "password" {
			values.PasswordPresent = nonempty
		} else if err := conninfoOption(values, key, value.String()); err != nil {
			return err
		}
	}
	return nil
}
func conninfoDecode(text string) (string, error) {
	decoded, err := url.PathUnescape(text) // '+' is literal, not form-space.
	if err != nil {
		return "", fmt.Errorf("invalid percent-encoded token in connection URI")
	}
	if strings.IndexByte(decoded, 0) >= 0 {
		return "", fmt.Errorf("forbidden value %%00 in percent-encoded value")
	}
	return decoded, nil
}
func parseConninfoURI(text string, values *Conninfo) error {
	_, rest, _ := strings.Cut(text, "://")
	authority := rest
	if n := strings.IndexAny(rest, "/?"); n >= 0 {
		authority, rest = rest[:n], rest[n:]
	} else {
		rest = ""
	}
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		userinfo := authority[:at]
		authority = authority[at+1:]
		user, password, present := strings.Cut(userinfo, ":")
		if _, err := conninfoDecode(user); err != nil {
			return err
		}
		if present {
			decoded, err := conninfoDecode(password)
			if err != nil {
				return err
			}
			values.PasswordPresent = decoded != ""
		}
	}
	var ports strings.Builder
	first := true
	for host := range strings.SplitSeq(authority, ",") {
		port := ""
		if strings.HasPrefix(host, "[") {
			end := strings.IndexByte(host, ']')
			if end < 0 {
				return fmt.Errorf("end of string reached when looking for matching \"]\" in IPv6 host address in URI")
			}
			tail := host[end+1:]
			host = host[1:end]
			if tail != "" {
				if tail[0] != ':' {
					return fmt.Errorf("unexpected character after IPv6 host address in URI")
				}
				port = tail[1:]
			}
		} else if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
			host, port = host[:colon], host[colon+1:]
		}
		if _, err := conninfoDecode(host); err != nil {
			return err
		}
		decoded, err := conninfoDecode(port)
		if err != nil {
			return err
		}
		if !first {
			ports.WriteByte(',')
		}
		first = false
		ports.WriteString(decoded)
	}
	values.port = ports.String()
	path, query, _ := strings.Cut(rest, "?")
	if path != "" {
		if path[0] != '/' {
			return fmt.Errorf("invalid connection URI path")
		}
		if _, err := conninfoDecode(path[1:]); err != nil {
			return err
		}
	}
	if query == "" {
		return nil
	}
	for parameter := range strings.SplitSeq(query, "&") {
		key, value, ok := strings.Cut(parameter, "=")
		if !ok {
			return fmt.Errorf("missing key/value separator \"=\" in URI query parameter")
		}
		key, err := conninfoDecode(key)
		if err != nil {
			return err
		}
		// A plain decoded port would otherwise retain the original URI's backing
		// allocation, including credentials. Percent decoding already owns its copy.
		if key == "port" && !strings.Contains(value, "%") {
			value = strings.Clone(value)
		}
		value, err = conninfoDecode(value)
		if err != nil {
			return err
		}
		if key == "ssl" && value == "true" {
			key, value = "sslmode", "require"
		}
		if err := conninfoOption(values, key, value); err != nil {
			return err
		}
	}
	return nil
}

// DblinkOption admits only released SERVER or USER MAPPING connection options.
// Debug/client-encoding/OAuth and wrapper-only metadata cannot become conninfo.
func DblinkOption(name string, mapping bool) bool {
	if !knownConninfoOption(name) || name == "replication" || name == "client_encoding" || name == "fallback_application_name" || strings.HasPrefix(name, "oauth_") {
		return false
	}
	secure := name == "user" || name == "password" || name == "sslpassword"
	return secure == mapping
}

// ConninfoOptions admits and escapes SQL/MED server/mapping options in released
// context order. No value can inject another keyword or credential. Original
// direct conninfo never uses this path.
func ConninfoOptions(server, mapping []catalog.ForeignOption) string {
	var out strings.Builder
	for group, options := range [2][]catalog.ForeignOption{server, mapping} {
		for _, option := range options {
			if !DblinkOption(option.Name, group == 1) {
				continue
			}
			if out.Len() != 0 {
				out.WriteByte(' ')
			}
			out.WriteString(option.Name)
			out.WriteString("='")
			for _, c := range option.Value {
				if c == '\\' || c == '\'' {
					out.WriteByte('\\')
				}
				out.WriteRune(c)
			}
			out.WriteByte('\'')
		}
	}
	return out.String()
}
