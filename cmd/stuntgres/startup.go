package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"stuntgres/internal/engine"
	"stuntgres/internal/sqlstate"
)

const (
	protocolVersion3  = 196608
	cancelRequestCode = 80877102
	sslRequestCode    = 80877103
	gssRequestCode    = 80877104
)

type parameterValue struct{ name, value string }

func startupError(code, message string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message}
}

// Startup errors are returned to the connection owner, which sends the fatal
// diagnostic and retires any session/registration created during admission.
func (c *client) startup() (bool, error) {
	if err := c.socket.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return false, err
	}
	var sslSeen, gssSeen bool
	var packet []byte
	for {
		var err error
		packet, err = c.wire.readStartup()
		if err != nil {
			return false, err
		}
		if len(packet) < 4 {
			return false, startupError("08P01", "invalid startup packet")
		}
		code := binary.BigEndian.Uint32(packet)
		switch code {
		case sslRequestCode, gssRequestCode:
			if len(packet) != 4 || code == sslRequestCode && sslSeen || code == gssRequestCode && gssSeen {
				return false, startupError("08P01", "invalid encryption request")
			}
			if code == sslRequestCode {
				sslSeen = true
			} else {
				gssSeen = true
			}
			if err := c.wire.flush(); err != nil {
				return false, err
			}
			n, err := c.socket.Write([]byte{'N'})
			if err != nil {
				return false, err
			}
			if n != 1 {
				return false, io.ErrShortWrite
			}
			continue
		case cancelRequestCode:
			if len(packet) != 12 {
				return false, startupError("08P01", "invalid cancel request")
			}
			c.server.cancelCommand(binary.BigEndian.Uint32(packet[4:8]), binary.BigEndian.Uint32(packet[8:12]))
			return false, nil
		}
		if code>>16 != 3 {
			return false, startupError("0A000", "unsupported frontend protocol version")
		}
		break
	}

	version := binary.BigEndian.Uint32(packet)
	pairs, unsupported, err := parseStartupParameters(packet[4:])
	if err != nil {
		return false, err
	}
	c.user = pairs["user"]
	if c.user == "" {
		return false, startupError("08P01", "no PostgreSQL user name specified in startup packet")
	}
	c.database = pairs["database"]
	if c.database == "" {
		c.database = c.user
	}
	if version != protocolVersion3 || len(unsupported) != 0 {
		body := appendInt32(nil, protocolVersion3)
		body = appendInt32(body, uint32(len(unsupported)))
		for _, name := range unsupported {
			body = appendString(body, name)
		}
		if err := c.wire.send('v', body); err != nil {
			return false, err
		}
	}
	if replication, ok := pairs["replication"]; ok && replication != "false" {
		return false, startupError("0A000", "replication connections are not supported by stuntgres Phase 1")
	}
	if err := c.wire.send('R', appendInt32(nil, 3)); err != nil {
		return false, err
	}
	if err := c.wire.flush(); err != nil {
		return false, err
	}
	message, err := c.wire.readMessage(nil)
	if err != nil {
		return false, err
	}
	if message.tag != 'p' {
		return false, startupError("08P01", "expected password response")
	}
	password, err := consumeString(&message.body)
	if err != nil {
		return false, err
	}
	if len(message.body) != 0 {
		return false, startupError("08P01", "trailing bytes in password response")
	}
	a, exists := c.server.users[c.user]
	verifier := a
	if !exists {
		verifier = c.server.dummy
	}
	verified := verifyPassword(verifier, password)
	if !exists || !verified {
		authErr := startupError("28P01", fmt.Sprintf("password authentication failed for user %q", c.user))
		if c.server.logs != nil {
			if err := c.server.logs.operational(c.ctx, slog.LevelError, "authentication failure", "connection_id", c.id, "user", c.user, "database", c.database); err != nil {
				return false, errors.Join(authErr, err)
			}
		}
		return false, authErr
	}
	db, exists := c.server.databases[c.database]
	if !exists {
		return false, startupError("3D000", fmt.Sprintf("database %q does not exist", c.database))
	}
	if !allowed(a, c.database) {
		return false, startupError("42501", fmt.Sprintf("permission denied for database %q", c.database))
	}
	defaults, err := startupOptions(pairs["options"])
	if err != nil {
		return false, err
	}
	for name, value := range pairs {
		switch name {
		case "user", "database", "options", "replication":
			continue
		}
		if strings.HasPrefix(name, "_pq_.") {
			continue
		}
		defaults[startupSettingName(name)] = value
	}
	c.db = db
	c.session, err = db.startSession(c.ctx, c.user, defaults)
	if err != nil {
		return false, err
	}
	if err := c.server.register(c); err != nil {
		return false, err
	}
	if err := c.wire.send('R', appendInt32(nil, 0)); err != nil {
		return false, err
	}
	if err := c.sendSettings(c.settingsSnapshot()); err != nil {
		return false, err
	}
	body := appendInt32(nil, c.id)
	body = appendInt32(body, c.key)
	if err := c.wire.send('K', body); err != nil {
		return false, err
	}
	if err := c.sendReady(); err != nil {
		return false, err
	}
	if err := c.wire.flush(); err != nil {
		return false, err
	}
	if err := c.socket.SetDeadline(time.Time{}); err != nil {
		return false, err
	}
	return true, nil
}

// Every key/value is terminated, followed by exactly one empty key. Keep wire
// order for unsupported protocol options; never retain sensitive packet bodies.
func parseStartupParameters(body []byte) (map[string]string, []string, error) {
	pairs := make(map[string]string)
	seen := make(map[string]bool)
	var unsupported []string
	for {
		name, err := consumeString(&body)
		if err != nil {
			return nil, nil, err
		}
		if name == "" {
			if len(body) != 0 {
				return nil, nil, startupError("08P01", "trailing bytes in startup packet")
			}
			return pairs, unsupported, nil
		}
		value, err := consumeString(&body)
		if err != nil {
			return nil, nil, err
		}
		if !utf8.ValidString(name) || !utf8.ValidString(value) {
			return nil, nil, startupError("08P01", "invalid UTF-8 in startup packet")
		}
		canonical := startupSettingName(name)
		if seen[canonical] {
			return nil, nil, startupError("08P01", "duplicate startup parameter")
		}
		seen[canonical] = true
		pairs[name] = value
		if strings.HasPrefix(name, "_pq_.") {
			unsupported = append(unsupported, name)
		}
	}
}

func startupSettingName(name string) string {
	for i := range len(name) {
		if name[i] >= 'A' && name[i] <= 'Z' {
			folded := []byte(name)
			for j := i; j < len(folded); j++ {
				if folded[j] >= 'A' && folded[j] <= 'Z' {
					folded[j] += 'a' - 'A'
				}
			}
			return string(folded)
		}
	}
	return name
}

// PostgreSQL options use backslash escaping, not shell quoting. An escaped
// whitespace byte remains part of its token; a terminal backslash is malformed.
func startupOptionTokens(options string) ([]string, error) {
	var tokens []string
	var token []byte
	started := false
	for i := 0; i < len(options); i++ {
		b := options[i]
		if b == '\\' {
			i++
			if i == len(options) {
				return nil, startupError("08P01", "unterminated escape in startup options")
			}
			token = append(token, options[i])
			started = true
		} else if b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f' {
			if started {
				tokens = append(tokens, string(token))
				token = token[:0]
				started = false
			}
		} else {
			token = append(token, b)
			started = true
		}
	}
	if started {
		tokens = append(tokens, string(token))
	}
	return tokens, nil
}

func startupOptions(options string) (startupDefaults, error) {
	tokens, err := startupOptionTokens(options)
	if err != nil {
		return nil, err
	}
	defaults := make(startupDefaults)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		var assignment string
		switch {
		case token == "-c":
			i++
			if i == len(tokens) {
				return nil, startupError("08P01", "missing setting in startup options")
			}
			assignment = tokens[i]
		case strings.HasPrefix(token, "-c"):
			assignment = token[2:]
		case strings.HasPrefix(token, "--"):
			assignment = token[2:]
		default:
			return nil, startupError("08P01", "unsupported startup option")
		}
		name, value, ok := strings.Cut(assignment, "=")
		if !ok || name == "" {
			return nil, startupError("08P01", "invalid setting in startup options")
		}
		defaults[startupSettingName(name)] = value
	}
	return defaults, nil
}

func (c *client) settingsSnapshot() []parameterValue {
	values := []parameterValue{{"server_version", "18.0"}, {"integer_datetimes", "on"}}
	for _, name := range []string{"application_name", "client_encoding", "server_encoding", "DateStyle", "IntervalStyle", "TimeZone", "standard_conforming_strings"} {
		if value, ok := c.session.Setting(name); ok {
			values = append(values, parameterValue{name, value})
		}
	}
	return values
}

func (c *client) sendSettings(values []parameterValue) error {
	if c.lastSettings == nil {
		c.lastSettings = make(map[string]string, len(values))
	}
	for _, value := range values {
		if previous, exists := c.lastSettings[value.name]; exists && previous == value.value {
			continue
		}
		body := appendString(nil, value.name)
		body = appendString(body, value.value)
		if err := c.wire.send('S', body); err != nil {
			return err
		}
		c.lastSettings[value.name] = value.value
	}
	return nil
}

func (c *client) sendReady() error {
	if err := c.session.Check(c.ctx); err != nil {
		fatal := *diagnostic(err)
		fatal.Severity, fatal.SeverityUnlocalized = "FATAL", "FATAL"
		return &fatal
	}
	var state byte
	switch c.session.TransactionState() {
	case engine.TxIdle:
		state = 'I'
	case engine.TxActive:
		state = 'T'
	case engine.TxAborted:
		state = 'E'
	default:
		return errors.New("stuntgres: unfinished implicit transaction at ReadyForQuery")
	}
	return c.wire.send('Z', []byte{state})
}
