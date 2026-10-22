package copyfmt

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Force is one parsed FORCE_QUOTE, FORCE_NULL or FORCE_NOT_NULL option:
// all columns (*) or a column list. Options.BindForces resolves it to
// per-field flags.
type Force struct {
	specified, all bool
	columns        []parser.Identifier
}

// Format is the COPY data format.
type Format uint8

const (
	Text Format = iota
	CSV
	Binary
)

type ErrorAction uint8

const (
	Stop ErrorAction = iota
	Ignore
)

type LogVerbosity uint8

const (
	LogDefault LogVerbosity = iota
	LogVerbose
	LogSilent
)

// Options are the parsed COPY options with PostgreSQL's defaults applied.
// Header is 0 (none), 1 (true) or 2 (MATCH). The *Fields flags are filled by
// BindForces, one per COPY field.
type Options struct {
	Format                                 Format
	Delimiter, Quote, Escape               byte
	Null, Default                          string
	HasDefault                             bool
	Header                                 uint8
	ForceQuote, ForceNull, ForceNotNull    Force
	QuoteFields, NullFields, NotNullFields []bool
	OnError                                ErrorAction
	LogVerbosity                           LogVerbosity
	RejectLimit                            int64 // Zero means unlimited.
}

// ParseOptions validates the options of a COPY statement in PostgreSQL's
// order and applies the format defaults.
func ParseOptions(s *parser.Copy) (Options, error) {
	o := Options{}
	delimiterSet, nullSet, quoteSet, escapeSet := false, false, false, false
	for i, v := range s.Options {
		name := strings.ToLower(v.Name)
		for _, prior := range s.Options[:i] {
			if strings.EqualFold(prior.Name, name) {
				return o, insertError("42601", "conflicting or redundant options", v.Pos)
			}
		}
		switch name {
		case "format", "delimiter", "null", "default", "on_error", "log_verbosity":
			if !v.HasValue {
				return o, sqlError("42601", name+" requires a parameter")
			}
		}
		switch name {
		case "format":
			switch v.Value {
			case "text":
			case "csv":
				o.Format = CSV
			case "binary":
				o.Format = Binary
			default:
				return o, insertError("22023", fmt.Sprintf("COPY format \"%s\" not recognized", v.Value), v.Pos)
			}
		case "delimiter", "quote", "escape":
			if len(v.Value) != 1 {
				return o, sqlError("0A000", "COPY "+name+" must be a single one-byte character")
			}
			switch name {
			case "delimiter":
				o.Delimiter, delimiterSet = v.Value[0], true
			case "quote":
				o.Quote, quoteSet = v.Value[0], true
			case "escape":
				o.Escape, escapeSet = v.Value[0], true
			}
		case "null":
			o.Null, nullSet = v.Value, true
		case "default":
			o.Default, o.HasDefault = v.Value, true
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
				o.Header = 1
			case "false", "off":
				o.Header = 0
			case "match":
				if !s.From {
					return o, sqlError("0A000", `cannot use "match" with HEADER in COPY TO`)
				}
				o.Header = 2
			default:
				return o, sqlError("42601", `header requires a Boolean value or "match"`)
			}
		case "force_quote", "force_null", "force_not_null":
			if v.Argument != parser.CopyArgumentStar && v.Argument != parser.CopyArgumentColumns {
				return o, insertError("22023", fmt.Sprintf("argument to option \"%s\" must be a list of column names", name), v.Pos)
			}
			force := Force{specified: true, all: v.Argument == parser.CopyArgumentStar, columns: v.Columns}
			switch name {
			case "force_quote":
				o.ForceQuote = force
			case "force_null":
				o.ForceNull = force
			case "force_not_null":
				o.ForceNotNull = force
			}
		case "on_error", "log_verbosity":
			value := v.Value
			if v.Argument == parser.CopyArgumentColumns {
				var name strings.Builder
				for i, column := range v.Columns {
					if i != 0 {
						name.WriteByte('.')
					}
					name.WriteString(column.Text)
				}
				value = name.String()
			}
			if name == "on_error" {
				if !s.From {
					return o, insertError("22023", "COPY ON_ERROR cannot be used with COPY TO", v.Pos)
				}
				switch strings.ToLower(value) {
				case "stop":
					o.OnError = Stop
				case "ignore":
					o.OnError = Ignore
				default:
					return o, insertError("22023", fmt.Sprintf("COPY ON_ERROR \"%s\" not recognized", value), v.Pos)
				}
			} else {
				switch strings.ToLower(value) {
				case "default":
					o.LogVerbosity = LogDefault
				case "verbose":
					o.LogVerbosity = LogVerbose
				case "silent":
					o.LogVerbosity = LogSilent
				default:
					return o, insertError("22023", fmt.Sprintf("COPY LOG_VERBOSITY \"%s\" not recognized", value), v.Pos)
				}
			}
		case "reject_limit":
			if !v.HasValue || v.Argument != parser.CopyArgumentScalar {
				return o, sqlError("42601", "reject_limit requires a numeric value")
			}
			limit, err := sqltype.ParseInt8(v.Value)
			if err != nil {
				return o, err
			}
			if limit <= 0 {
				return o, sqlError("22023", fmt.Sprintf("REJECT_LIMIT (%d) must be greater than zero", limit))
			}
			o.RejectLimit = limit
		case "freeze", "encoding":
			return o, sqlError("0A000", "COPY "+strings.ToUpper(name)+" is unsupported")
		default:
			return o, insertError("42601", fmt.Sprintf("option \"%s\" not recognized", name), v.Pos)
		}
	}
	if o.RejectLimit != 0 && o.OnError != Ignore {
		return o, sqlError("22023", "COPY REJECT_LIMIT requires ON_ERROR to be set to IGNORE")
	}
	if o.Format == Binary && o.OnError != Stop {
		return o, sqlError("42601", "only ON_ERROR STOP is allowed in BINARY mode")
	}
	if o.Format == Binary {
		for _, option := range []struct {
			name string
			set  bool
		}{{"DELIMITER", delimiterSet}, {"NULL", nullSet}, {"DEFAULT", o.HasDefault}} {
			if option.set {
				return o, sqlError("42601", "cannot specify "+option.name+" in BINARY mode")
			}
		}
		if o.Header != 0 {
			return o, sqlError("0A000", "cannot specify HEADER in BINARY mode")
		}
	}
	if !delimiterSet {
		if o.Format == CSV {
			o.Delimiter = ','
		} else {
			o.Delimiter = '\t'
		}
	}
	if !nullSet && o.Format != CSV {
		o.Null = `\N`
	}
	if !quoteSet {
		o.Quote = '"'
	}
	if !escapeSet {
		o.Escape = o.Quote
	}
	if o.Format != CSV {
		for _, option := range []struct {
			name string
			set  bool
		}{
			{"QUOTE", quoteSet}, {"ESCAPE", escapeSet}, {"FORCE_QUOTE", o.ForceQuote.specified},
			{"FORCE_NOT_NULL", o.ForceNotNull.specified}, {"FORCE_NULL", o.ForceNull.specified},
		} {
			if option.set {
				return o, sqlError("0A000", "COPY "+option.name+" requires CSV mode")
			}
		}
	}
	if o.Format == Binary {
		return o, nil
	}
	if o.ForceQuote.specified && s.From {
		return o, sqlError("0A000", "COPY FORCE_QUOTE cannot be used with COPY FROM")
	}
	if o.ForceNotNull.specified && !s.From {
		return o, sqlError("22023", "COPY FORCE_NOT_NULL cannot be used with COPY TO")
	}
	if o.ForceNull.specified && !s.From {
		return o, sqlError("22023", "COPY FORCE_NULL cannot be used with COPY TO")
	}
	if o.Delimiter == '\r' || o.Delimiter == '\n' {
		return o, sqlError("22023", "COPY delimiter cannot be newline or carriage return")
	}
	if strings.ContainsAny(o.Null, "\r\n") {
		return o, sqlError("22023", "COPY null representation cannot use newline or carriage return")
	}
	if o.HasDefault && strings.ContainsAny(o.Default, "\r\n") {
		return o, sqlError("22023", "COPY default representation cannot use newline or carriage return")
	}
	d := o.Delimiter
	if o.Format != CSV && (d == '\\' || d == '.' || d >= 'a' && d <= 'z' || d >= '0' && d <= '9') {
		return o, sqlError("22023", fmt.Sprintf("COPY delimiter cannot be \"%c\"", d))
	}
	if o.Format == CSV && d == o.Quote {
		return o, sqlError("22023", "COPY delimiter and quote must be different")
	}
	if strings.IndexByte(o.Null, d) >= 0 {
		return o, sqlError("22023", "COPY delimiter character must not appear in the NULL specification")
	}
	if o.Format == CSV && strings.IndexByte(o.Null, o.Quote) >= 0 {
		return o, sqlError("22023", "CSV quote character must not appear in the NULL specification")
	}
	if o.HasDefault {
		if !s.From {
			return o, sqlError("0A000", "COPY DEFAULT cannot be used with COPY TO")
		}
		if strings.IndexByte(o.Default, d) >= 0 {
			return o, sqlError("0A000", "COPY delimiter character must not appear in the DEFAULT specification")
		}
		if o.Format == CSV && strings.IndexByte(o.Default, o.Quote) >= 0 {
			return o, sqlError("0A000", "CSV quote character must not appear in the DEFAULT specification")
		}
		if o.Null == o.Default {
			return o, sqlError("0A000", "NULL specification and DEFAULT specification cannot be the same")
		}
	}
	return o, nil
}
