package subscription

import (
	"fmt"
	"strconv"
	"strings"
)

// This is REL_18_6 PQconninfoOptions, including internal keys. Parsing never
// supplies defaults or consults service, password, environment or network state.
func connectionKeyword(key string) bool {
	switch key {
	case "service", "user", "password", "passfile", "channel_binding", "connect_timeout", "dbname", "host", "hostaddr", "port",
		"client_encoding", "options", "application_name", "fallback_application_name", "keepalives", "keepalives_idle", "keepalives_interval", "keepalives_count", "tcp_user_timeout",
		"sslmode", "sslnegotiation", "sslcompression", "sslcert", "sslkey", "sslcertmode", "sslpassword", "sslrootcert", "sslcrl", "sslcrldir", "sslsni", "requirepeer", "require_auth",
		"min_protocol_version", "max_protocol_version", "ssl_min_protocol_version", "ssl_max_protocol_version", "gssencmode", "krbsrvname", "gsslib", "gssdelegation", "replication",
		"target_session_attrs", "load_balance_hosts", "scram_client_key", "scram_server_key", "oauth_issuer", "oauth_client_id", "oauth_client_secret", "oauth_scope", "sslkeylogfile", "requiressl":
		return true
	}
	return false
}

func connectionError(format string, args ...any) error {
	return failure("42601", "invalid connection string syntax: "+fmt.Sprintf(format, args...)+"\n")
}
func connectionSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}
func parseConnection(text string) (bool, error) {
	values := make(map[string]string)
	if strings.HasPrefix(text, "postgresql://") || strings.HasPrefix(text, "postgres://") {
		if err := parseURI(text, values); err != nil {
			return false, err
		}
		return values["password"] != "", nil
	}
	for at := 0; at < len(text); {
		for at < len(text) && connectionSpace(text[at]) {
			at++
		}
		if at == len(text) {
			break
		}
		start := at
		for at < len(text) && text[at] != '=' && !connectionSpace(text[at]) {
			at++
		}
		name := text[start:at]
		for at < len(text) && connectionSpace(text[at]) {
			at++
		}
		if at == len(text) || text[at] != '=' {
			return false, connectionError(`missing "=" after "%s" in connection info string`, name)
		}
		at++
		for at < len(text) && connectionSpace(text[at]) {
			at++
		}
		var value strings.Builder
		quoted := at < len(text) && text[at] == '\''
		if quoted {
			at++
		}
		closed := !quoted
		for at < len(text) {
			c := text[at]
			at++
			if c == '\\' {
				if at < len(text) {
					value.WriteByte(text[at])
					at++
				}
				continue
			}
			if quoted {
				if c == '\'' {
					closed = true
					break
				}
			} else if connectionSpace(c) {
				break
			}
			value.WriteByte(c)
		}
		if !closed {
			return false, connectionError("unterminated quoted string in connection info string")
		}
		if !connectionKeyword(name) {
			return false, connectionError(`invalid connection option "%s"`, name)
		}
		values[name] = value.String()
	}
	return values["password"] != "", nil
}
func decodeURI(raw string) (string, error) {
	text := strings.TrimLeft(raw, " ")
	if !strings.ContainsAny(text, "% ") {
		return text, nil
	}
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == ' ' {
			if strings.TrimLeft(text[i:], " ") != "" {
				return "", connectionError(`unexpected spaces found in "%s", use percent-encoded spaces (%%20) instead`, raw)
			}
			break
		}
		if text[i] != '%' {
			out.WriteByte(text[i])
			continue
		}
		if i+2 >= len(text) {
			return "", connectionError(`invalid percent-encoded token: "%s"`, raw)
		}
		n, err := strconv.ParseUint(text[i+1:i+3], 16, 8)
		if err != nil {
			return "", connectionError(`invalid percent-encoded token: "%s"`, raw)
		}
		if n == 0 {
			return "", connectionError(`forbidden value %%00 in percent-encoded value: "%s"`, raw)
		}
		out.WriteByte(byte(n))
		i += 2
	}
	return out.String(), nil
}
func parseURI(uri string, values map[string]string) error {
	prefix := strings.Index(uri, "://") + 3
	at := prefix
	store := func(key, raw string) error {
		v, err := decodeURI(raw)
		if err == nil {
			values[key] = v
		}
		return err
	}
	// libpq looks for credentials up to the first slash, not net/url's authority.
	end := at
	for end < len(uri) && uri[end] != '@' && uri[end] != '/' {
		end++
	}
	if end < len(uri) && uri[end] == '@' {
		credentials := uri[at:end]
		user, password, has := strings.Cut(credentials, ":")
		if user != "" {
			if err := store("user", user); err != nil {
				return err
			}
		}
		if has && password != "" {
			if err := store("password", password); err != nil {
				return err
			}
		}
		at = end + 1
	}
	var hosts, ports strings.Builder
	for {
		start := at
		host := ""
		if at < len(uri) && uri[at] == '[' {
			at++
			start = at
			for at < len(uri) && uri[at] != ']' {
				at++
			}
			if at == len(uri) {
				return connectionError(`end of string reached when looking for matching "]" in IPv6 host address in URI: "%s"`, uri)
			}
			if at == start {
				return connectionError(`IPv6 host address may not be empty in URI: "%s"`, uri)
			}
			host = uri[start:at]
			at++
			if at < len(uri) && !strings.ContainsRune(":/?,", rune(uri[at])) {
				return connectionError(`unexpected character "%c" at position %d in URI (expected ":" or "/"): "%s"`, uri[at], at+1, uri)
			}
		} else {
			for at < len(uri) && !strings.ContainsRune(":/?,", rune(uri[at])) {
				at++
			}
			host = uri[start:at]
		}
		hosts.WriteString(host)
		if at < len(uri) && uri[at] == ':' {
			at++
			start = at
			for at < len(uri) && !strings.ContainsRune("/?,", rune(uri[at])) {
				at++
			}
			ports.WriteString(uri[start:at])
		}
		if at == len(uri) || uri[at] != ',' {
			break
		}
		at++
		hosts.WriteByte(',')
		ports.WriteByte(',')
	}
	if hosts.Len() != 0 {
		if err := store("host", hosts.String()); err != nil {
			return err
		}
	}
	if ports.Len() != 0 {
		if err := store("port", ports.String()); err != nil {
			return err
		}
	}
	if at < len(uri) && uri[at] == '/' {
		at++
		start := at
		for at < len(uri) && uri[at] != '?' {
			at++
		}
		if at > start {
			if err := store("dbname", uri[start:at]); err != nil {
				return err
			}
		}
	}
	if at == len(uri) {
		return nil
	}
	at++
	for at < len(uri) {
		end := at
		for end < len(uri) && uri[end] != '&' {
			end++
		}
		part := uri[at:end]
		key, raw, ok := strings.Cut(part, "=")
		if !ok {
			return connectionError(`missing key/value separator "=" in URI query parameter: "%s"`, part)
		}
		if strings.Contains(raw, "=") {
			return connectionError(`extra key/value separator "=" in URI query parameter: "%s"`, key)
		}
		key, err := decodeURI(key)
		if err != nil {
			return err
		}
		value, err := decodeURI(raw)
		if err != nil {
			return err
		}
		if key == "ssl" && value == "true" {
			key, value = "sslmode", "require"
		}
		if !connectionKeyword(key) {
			return connectionError(`invalid URI query parameter: "%s"`, key)
		}
		values[key] = value
		at = end + 1
	}
	return nil
}
