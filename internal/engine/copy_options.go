package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

// CopyBoundary is the database/sql capability error; it does no object lookup.
func CopyBoundary(s *parser.Copy) error {
	switch s.Endpoint {
	case parser.CopyFile:
		return sqlError("0A000", "COPY server files are unsupported")
	case parser.CopyProgram:
		return sqlError("0A000", "COPY PROGRAM is unsupported")
	}
	if s.From {
		return sqlError("0A000", "COPY FROM STDIN requires stuntgres.CopyFrom")
	}
	return sqlError("0A000", "COPY TO STDOUT requires stuntgres.CopyTo")
}

type copyOptions struct {
	delimiter  byte
	null, def  string
	hasDefault bool
	header     uint8
}

func copyTextOptions(s *parser.Copy) (copyOptions, error) {
	o := copyOptions{delimiter: '\t', null: `\N`}
	for i, v := range s.Options {
		name := strings.ToLower(v.Name)
		for _, prior := range s.Options[:i] {
			if strings.EqualFold(prior.Name, name) {
				return o, insertError("42601", "conflicting or redundant options", v.Pos)
			}
		}
		switch name {
		case "format", "delimiter", "null", "default":
			if !v.HasValue {
				return o, sqlError("42601", name+" requires a parameter")
			}
		}
		switch name {
		case "format":
			switch v.Value {
			case "text":
			case "csv", "binary":
				return o, sqlError("0A000", "COPY "+strings.ToUpper(v.Value)+" format is unsupported")
			default:
				return o, insertError("22023", fmt.Sprintf("COPY format \"%s\" not recognized", v.Value), v.Pos)
			}
		case "delimiter":
			if len(v.Value) != 1 {
				return o, sqlError("0A000", "COPY delimiter must be a single one-byte character")
			}
			o.delimiter = v.Value[0]
		case "null":
			o.null = v.Value
		case "default":
			o.def = v.Value
			o.hasDefault = true
		case "header":
			value := strings.ToLower(v.Value)
			if !v.HasValue {
				value = "true"
			}
			if v.Numeric {
				switch value {
				case "0":
					value = "false"
				case "1":
					value = "true"
				}
			}
			switch value {
			case "true", "on":
				o.header = 1
			case "false", "off":
				o.header = 0
			case "match":
				if !s.From {
					return o, sqlError("0A000", `cannot use "match" with HEADER in COPY TO`)
				}
				o.header = 2
			default:
				return o, sqlError("42601", `header requires a Boolean value or "match"`)
			}
		case "quote", "escape", "force_quote", "force_not_null", "force_null":
			return o, sqlError("0A000", "COPY "+strings.ToUpper(name)+" requires CSV mode")
		case "freeze", "on_error", "reject_limit", "log_verbosity", "encoding":
			return o, sqlError("0A000", "COPY "+strings.ToUpper(name)+" is unsupported")
		default:
			return o, insertError("42601", fmt.Sprintf("option \"%s\" not recognized", name), v.Pos)
		}
	}
	if o.delimiter == '\r' || o.delimiter == '\n' {
		return o, sqlError("22023", "COPY delimiter cannot be newline or carriage return")
	}
	if strings.ContainsAny(o.null, "\r\n") {
		return o, sqlError("22023", "COPY null representation cannot use newline or carriage return")
	}
	if o.hasDefault && strings.ContainsAny(o.def, "\r\n") {
		return o, sqlError("22023", "COPY default representation cannot use newline or carriage return")
	}
	d := o.delimiter
	if d == '\\' || d == '.' || d >= 'a' && d <= 'z' || d >= '0' && d <= '9' {
		return o, sqlError("22023", fmt.Sprintf("COPY delimiter cannot be \"%c\"", d))
	}
	if strings.IndexByte(o.null, d) >= 0 {
		return o, sqlError("22023", "COPY delimiter character must not appear in the NULL specification")
	}
	if o.hasDefault {
		if !s.From {
			return o, sqlError("0A000", "COPY DEFAULT cannot be used with COPY TO")
		}
		if strings.IndexByte(o.def, d) >= 0 {
			return o, sqlError("0A000", "COPY delimiter character must not appear in the DEFAULT specification")
		}
		if o.null == o.def {
			return o, sqlError("0A000", "NULL specification and DEFAULT specification cannot be the same")
		}
	}
	if s.Where != nil {
		return o, sqlError("0A000", "COPY FROM WHERE is unsupported")
	}
	return o, nil
}
