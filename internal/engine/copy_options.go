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

type copyForce struct {
	specified, all bool
	columns        []parser.Identifier
}

type copyFormat uint8

const (
	copyFormatText copyFormat = iota
	copyFormatCSV
	copyFormatBinary
)

type copyOptions struct {
	format                                 copyFormat
	delimiter, quote, escape               byte
	null, def                              string
	hasDefault                             bool
	header                                 uint8
	forceQuote, forceNull, forceNotNull    copyForce
	quoteFields, nullFields, notNullFields []bool
}

func parseCopyOptions(s *parser.Copy) (copyOptions, error) {
	o := copyOptions{}
	delimiterSet, nullSet, quoteSet, escapeSet := false, false, false, false
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
			case "csv":
				o.format = copyFormatCSV
			case "binary":
				o.format = copyFormatBinary
			default:
				return o, insertError("22023", fmt.Sprintf("COPY format \"%s\" not recognized", v.Value), v.Pos)
			}
		case "delimiter", "quote", "escape":
			if len(v.Value) != 1 {
				return o, sqlError("0A000", "COPY "+name+" must be a single one-byte character")
			}
			switch name {
			case "delimiter":
				o.delimiter, delimiterSet = v.Value[0], true
			case "quote":
				o.quote, quoteSet = v.Value[0], true
			case "escape":
				o.escape, escapeSet = v.Value[0], true
			}
		case "null":
			o.null, nullSet = v.Value, true
		case "default":
			o.def, o.hasDefault = v.Value, true
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
		case "force_quote", "force_null", "force_not_null":
			if v.Argument != parser.CopyArgumentStar && v.Argument != parser.CopyArgumentColumns {
				return o, insertError("22023", fmt.Sprintf("argument to option \"%s\" must be a list of column names", name), v.Pos)
			}
			force := copyForce{specified: true, all: v.Argument == parser.CopyArgumentStar, columns: v.Columns}
			switch name {
			case "force_quote":
				o.forceQuote = force
			case "force_null":
				o.forceNull = force
			case "force_not_null":
				o.forceNotNull = force
			}
		case "freeze", "on_error", "reject_limit", "log_verbosity", "encoding":
			return o, sqlError("0A000", "COPY "+strings.ToUpper(name)+" is unsupported")
		default:
			return o, insertError("42601", fmt.Sprintf("option \"%s\" not recognized", name), v.Pos)
		}
	}
	if o.format == copyFormatBinary {
		for _, option := range []struct {
			name string
			set  bool
		}{{"DELIMITER", delimiterSet}, {"NULL", nullSet}, {"DEFAULT", o.hasDefault}} {
			if option.set {
				return o, sqlError("42601", "cannot specify "+option.name+" in BINARY mode")
			}
		}
		if o.header != 0 {
			return o, sqlError("0A000", "cannot specify HEADER in BINARY mode")
		}
	}
	if !delimiterSet {
		if o.format == copyFormatCSV {
			o.delimiter = ','
		} else {
			o.delimiter = '\t'
		}
	}
	if !nullSet && o.format != copyFormatCSV {
		o.null = `\N`
	}
	if !quoteSet {
		o.quote = '"'
	}
	if !escapeSet {
		o.escape = o.quote
	}
	if o.format != copyFormatCSV {
		for _, option := range []struct {
			name string
			set  bool
		}{
			{"QUOTE", quoteSet}, {"ESCAPE", escapeSet}, {"FORCE_QUOTE", o.forceQuote.specified},
			{"FORCE_NOT_NULL", o.forceNotNull.specified}, {"FORCE_NULL", o.forceNull.specified},
		} {
			if option.set {
				return o, sqlError("0A000", "COPY "+option.name+" requires CSV mode")
			}
		}
	}
	if o.format == copyFormatBinary {
		if s.Where != nil {
			return o, sqlError("0A000", "COPY FROM WHERE is unsupported")
		}
		return o, nil
	}
	if o.forceQuote.specified && s.From {
		return o, sqlError("0A000", "COPY FORCE_QUOTE cannot be used with COPY FROM")
	}
	if o.forceNotNull.specified && !s.From {
		return o, sqlError("22023", "COPY FORCE_NOT_NULL cannot be used with COPY TO")
	}
	if o.forceNull.specified && !s.From {
		return o, sqlError("22023", "COPY FORCE_NULL cannot be used with COPY TO")
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
	if o.format != copyFormatCSV && (d == '\\' || d == '.' || d >= 'a' && d <= 'z' || d >= '0' && d <= '9') {
		return o, sqlError("22023", fmt.Sprintf("COPY delimiter cannot be \"%c\"", d))
	}
	if o.format == copyFormatCSV && d == o.quote {
		return o, sqlError("22023", "COPY delimiter and quote must be different")
	}
	if strings.IndexByte(o.null, d) >= 0 {
		return o, sqlError("22023", "COPY delimiter character must not appear in the NULL specification")
	}
	if o.format == copyFormatCSV && strings.IndexByte(o.null, o.quote) >= 0 {
		return o, sqlError("22023", "CSV quote character must not appear in the NULL specification")
	}
	if o.hasDefault {
		if !s.From {
			return o, sqlError("0A000", "COPY DEFAULT cannot be used with COPY TO")
		}
		if strings.IndexByte(o.def, d) >= 0 {
			return o, sqlError("0A000", "COPY delimiter character must not appear in the DEFAULT specification")
		}
		if o.format == copyFormatCSV && strings.IndexByte(o.def, o.quote) >= 0 {
			return o, sqlError("0A000", "CSV quote character must not appear in the DEFAULT specification")
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
