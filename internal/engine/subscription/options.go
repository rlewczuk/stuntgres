// Package subscription owns subscription metadata validation and staging, never
// publisher connections, replication workers, slots or synchronization.
package subscription

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func failure(code, message string) *sqlstate.Error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

type options struct {
	values                                          map[string]string
	supplied                                        map[string]bool
	connect, enabled, createSlot, copyData, refresh bool
	lsn                                             uint64
}

// NumericOnly applies a sign after the scanner has classified the unsigned
// token as int32 or Float. Float nodes retain their spelling in defGetString.
func optionInteger(option parser.DictionaryOption) (int64, bool) {
	if !option.Numeric {
		return 0, false
	}
	text := strings.TrimPrefix(strings.TrimPrefix(option.Value, "-"), "+")
	base := 10
	if len(text) > 2 && text[0] == '0' && strings.ContainsRune("bBoOxX", rune(text[1])) {
		base = 0
	}
	n, err := strconv.ParseUint(strings.ReplaceAll(text, "_", ""), base, 31)
	if err != nil {
		return 0, false
	}
	if strings.HasPrefix(option.Value, "-") {
		return -int64(n), true
	}
	return int64(n), true
}

func optionString(option parser.DictionaryOption) string {
	if n, ok := optionInteger(option); ok {
		return strconv.FormatInt(n, 10)
	}
	return option.Value
}

func allowedOption(action, name string) bool {
	switch name {
	case "slot_name", "synchronous_commit", "binary", "streaming", "two_phase", "disable_on_error", "password_required", "run_as_owner", "failover", "origin":
		return action == "create" || action == "options"
	case "connect", "enabled", "create_slot":
		return action == "create"
	case "copy_data":
		return action == "create" || action == "set" || action == "add" || action == "remove" || action == "refresh"
	case "refresh":
		return action == "set" || action == "add" || action == "remove"
	case "lsn":
		return action == "skip"
	}
	return false
}

func boolean(option parser.DictionaryOption) (bool, error) {
	if !option.Supplied {
		return true, nil
	}
	if n, ok := optionInteger(option); ok {
		if n == 0 || n == 1 {
			return n == 1, nil
		}
	} else if !option.Numeric {
		switch strings.ToLower(option.Value) {
		case "true", "on":
			return true, nil
		case "false", "off":
			return false, nil
		}
	}
	return false, failure("42601", option.Name.Text+" requires a Boolean value")
}
func parseOptions(s *parser.Subscription, h Host) (options, error) {
	o := options{values: make(map[string]string), supplied: make(map[string]bool), connect: true, enabled: s.Action == "create", createSlot: s.Action == "create", copyData: true, refresh: true}
	// Exact names, not substrings of an inventory; decoded quoted identifiers
	// may contain whitespace and must still identify a single supported option.
	for _, option := range s.Options {
		name := option.Name.Text
		if !allowedOption(s.Action, name) {
			return o, failure("42601", fmt.Sprintf(`unrecognized subscription parameter: "%s"`, name))
		}
		if name == "lsn" && !option.Supplied {
			return o, failure("42601", "lsn requires a parameter")
		}
		if o.supplied[name] {
			err := failure("42601", "conflicting or redundant options")
			err.Position = fmt.Sprint(option.Name.Pos.Character)
			return o, err
		}
		o.supplied[name] = true
		value := option.Value
		switch name {
		case "slot_name", "synchronous_commit", "origin", "lsn":
			if !option.Supplied {
				return o, failure("42601", name+" requires a parameter")
			}
			value = optionString(option)
			if name == "slot_name" && value != "none" {
				if err := validateSlot(value); err != nil {
					return o, err
				}
			}
			if name == "origin" && !strings.EqualFold(value, "any") && !strings.EqualFold(value, "none") {
				return o, failure("22023", fmt.Sprintf(`unrecognized origin value: "%s"`, value))
			}
			if name == "synchronous_commit" {
				if err := h.ValidateSubscriptionSync(value); err != nil {
					return o, err
				}
			}
			if name == "lsn" && value != "none" {
				var err error
				o.lsn, err = h.ParseSubscriptionLSN(value)
				if err != nil {
					return o, err
				}
				if o.lsn == 0 {
					return o, failure("22023", "invalid WAL location (LSN): "+value)
				}
			}
		case "streaming":
			if option.Supplied && strings.EqualFold(value, "parallel") {
				value = "p"
			} else {
				b, err := boolean(option)
				if err != nil {
					return o, failure("42601", `streaming requires a Boolean value or "parallel"`)
				}
				value = "f"
				if b {
					value = "t"
				}
			}
		default:
			b, err := boolean(option)
			if err != nil {
				return o, err
			}
			value = "false"
			if b {
				value = "true"
			}
			switch name {
			case "connect":
				o.connect = b
			case "enabled":
				o.enabled = b
			case "create_slot":
				o.createSlot = b
			case "copy_data":
				o.copyData = b
			case "refresh":
				o.refresh = b
			}
		}
		o.values[name] = value
	}
	if s.Action == "create" {
		if !o.connect {
			for _, name := range []string{"enabled", "create_slot", "copy_data"} {
				if o.supplied[name] && o.values[name] == "true" {
					return o, failure("42601", "connect = false and "+name+" = true are mutually exclusive options")
				}
			}
			o.enabled, o.createSlot, o.copyData = false, false, false
		}
		if o.supplied["slot_name"] && o.values["slot_name"] == "none" {
			for _, flag := range []struct {
				name  string
				value bool
			}{{"enabled", o.enabled}, {"create_slot", o.createSlot}} {
				if flag.value {
					if o.supplied[flag.name] {
						return o, failure("42601", "slot_name = NONE and "+flag.name+" = true are mutually exclusive options")
					}
					return o, failure("42601", "subscription with slot_name = NONE must also set "+flag.name+" = false")
				}
			}
		}
	}
	return o, nil
}
func validateSlot(name string) error {
	if len(name) == 0 {
		return failure("42602", `replication slot name "" is too short`)
	}
	if len(name) >= 64 {
		return failure("42622", fmt.Sprintf(`replication slot name "%s" is too long`, name))
	}
	for _, c := range []byte(name) {
		if c < 'a' || c > 'z' {
			if c >= '0' && c <= '9' || c == '_' {
				continue
			}
			e := failure("42602", fmt.Sprintf(`replication slot name "%s" contains invalid character`, name))
			e.Hint = "Replication slot names may only contain lower case letters, numbers, and the underscore character."
			return e
		}
	}
	return nil
}
func (o options) apply(d *catalog.Subscription) {
	for name, value := range o.values {
		b := value == "true"
		switch name {
		case "slot_name":
			d.HasSlot = value != "none"
			d.SlotName = value
			if !d.HasSlot {
				d.SlotName = ""
			}
		case "synchronous_commit":
			d.SynchronousCommit = value
		case "origin":
			d.Origin = value
		case "binary":
			d.Binary = b
		case "streaming":
			d.Streaming = value[0]
		case "two_phase":
			d.TwoPhase = 'd'
			if b {
				d.TwoPhase = 'p'
			}
		case "disable_on_error":
			d.DisableOnError = b
		case "password_required":
			d.PasswordRequired = b
		case "run_as_owner":
			d.RunAsOwner = b
		case "failover":
			d.Failover = b
		}
	}
}
