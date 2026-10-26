package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/sqlstate"
)

// Lquery constructs an installation-owned scalar identity, not a builtin OID.
func Lquery(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid lquery descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "LQUERY", lquery: true}, nil
}

func (d *Descriptor) IsLquery() bool { return d != nil && d.lquery }

type lqueryPayload struct { text string }

func lqueryError(code, message, detail string) error {
	return sqlstate.SoftInputError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail})
}

func lquerySyntax(pos int) error {
	return lqueryError("42601", fmt.Sprintf("lquery syntax error at character %d", pos), "")
}

func lqueryEnd() error {
	return lqueryError("42601", "lquery syntax error", "Unexpected end of input.")
}

func lqueryLabel(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-'
}

// Released Linux/amd64 atoi saturates at LONG_MAX, then narrows to int32.
// Consume every digit, including after saturation; no artificial decimal cap.
func lqueryNumber(ctx context.Context, text string, pos *int, high bool, low int32) (int32, error) {
	start := *pos
	var n uint64
	const max = uint64(1<<63 - 1)
	for *pos < len(text) && text[*pos] >= '0' && text[*pos] <= '9' {
		if (*pos-start)&255 == 0 {
			if err := ctx.Err(); err != nil { return 0, err }
		}
		digit := uint64(text[*pos] - '0')
		if n > (max-digit)/10 { n = max } else { n = n*10 + digit }
		*pos++
	}
	value := int32(n)
	kind := "Low"
	if high { kind = "High" }
	if value < 0 || value > 65535 {
		return 0, lqueryError("54000", "lquery syntax error", fmt.Sprintf("%s limit (%d) exceeds the maximum allowed (65535), at character %d.", kind, value, start+1))
	}
	if high && low > value {
		return 0, lqueryError("42601", "lquery syntax error", fmt.Sprintf("Low limit (%d) is greater than high limit (%d), at character %d.", low, value, start+1))
	}
	return value, nil
}

func lqueryQuantifier(ctx context.Context, text string, pos *int) (int32, int32, error) {
	*pos++ // opening brace
	if *pos == len(text) { return 0, 0, lqueryEnd() }
	low, high := int32(0), int32(65535)
	var err error
	if text[*pos] != ',' {
		if text[*pos] < '0' || text[*pos] > '9' { return 0, 0, lquerySyntax(*pos+1) }
		low, err = lqueryNumber(ctx, text, pos, false, 0)
		if err != nil { return 0, 0, err }
		if *pos == len(text) { return 0, 0, lqueryEnd() }
		if text[*pos] == '}' { *pos++; return low, low, nil }
		if text[*pos] != ',' { return 0, 0, lquerySyntax(*pos+1) }
	}
	*pos++ // comma
	if *pos == len(text) { return 0, 0, lqueryEnd() }
	if text[*pos] != '}' {
		if text[*pos] < '0' || text[*pos] > '9' { return 0, 0, lquerySyntax(*pos+1) }
		high, err = lqueryNumber(ctx, text, pos, true, low)
		if err != nil { return 0, 0, err }
		if *pos == len(text) { return 0, 0, lqueryEnd() }
		if text[*pos] != '}' { return 0, 0, lquerySyntax(*pos+1) }
	}
	*pos++
	return low, high, nil
}

func lqueryWriteCount(out *strings.Builder, low, high int32, wildcard bool) {
	if wildcard && low == 0 && high == 65535 { return }
	var digits [12]byte
	out.WriteByte('{')
	if low == high {
		out.Write(strconv.AppendInt(digits[:0], int64(low), 10))
	} else {
		if low != 0 { out.Write(strconv.AppendInt(digits[:0], int64(low), 10)) }
		out.WriteByte(',')
		if high != 65535 { out.Write(strconv.AppendInt(digits[:0], int64(high), 10)) }
	}
	out.WriteByte('}')
}

func (d *Descriptor) lqueryInput(ctx context.Context, text string) (Value, error) {
	// The released item-count check precedes syntax, and level-size errors are
	// deferred until the entire grammar (including variant counts) is valid.
	levels := 1
	for i := range len(text) {
		if i&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
		if text[i] == '.' { levels++ }
	}
	if levels > 65535 {
		return Value{}, lqueryError("54000", fmt.Sprintf("number of lquery items (%d) exceeds the maximum allowed (65535)", levels), "")
	}
	if len(text) == 0 { return Value{}, lqueryEnd() }
	var out strings.Builder
	oversized := false
	for pos := 0; pos < len(text); {
		if err := ctx.Err(); err != nil { return Value{}, err }
		if pos != 0 { out.WriteByte('.') }
		wildcard := text[pos] == '*'
		if wildcard {
			out.WriteByte('*'); pos++
		} else {
			negated := text[pos] == '!'
			if negated { out.WriteByte('!'); pos++ }
			variants, size := 0, 16
			for {
				start := pos
				// A leading ! enters WAITDELIM even with an empty label;
				// other levels and alternatives require a label immediately.
				if (variants != 0 || !negated) && (pos == len(text) || !lqueryLabel(text[pos])) {
					if pos == len(text) { return Value{}, lqueryEnd() }
					return Value{}, lquerySyntax(pos+1)
				}
				variants++
				if variants > 65535 { return Value{}, lqueryError("54000", "lquery level has too many variants", "Number of variants exceeds the maximum allowed (65535).") }
				for pos < len(text) && lqueryLabel(text[pos]) {
					if (pos-start)&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
					pos++
				}
				end := pos
				var flags byte
				for pos < len(text) {
					if pos&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
					switch text[pos] {
					case '%': flags |= 1
					case '@': flags |= 2
					case '*': flags |= 4
					default: goto modifiersDone
					}
					pos++
				}
			modifiersDone:
				if pos < len(text) && text[pos] != '|' && text[pos] != '{' && text[pos] != '.' { return Value{}, lquerySyntax(pos+1) }
				if end == start { return Value{}, lqueryError("42601", fmt.Sprintf("lquery syntax error at character %d", end+1), "Empty labels are not allowed.") }
				if end-start > 1000 {
					return Value{}, lqueryError("42622", "label string is too long", fmt.Sprintf("Label length is %d, must be at most 1000, at character %d.", end-start, end+1))
				}
				size += (8 + end-start + 7) &^ 7
				out.WriteString(text[start:end])
				if flags&1 != 0 { out.WriteByte('%') }
				if flags&2 != 0 { out.WriteByte('@') }
				if flags&4 != 0 { out.WriteByte('*') }
				if pos == len(text) || text[pos] != '|' { break }
				out.WriteByte('|'); pos++
			}
			oversized = oversized || size > 65535
		}
		if pos < len(text) && text[pos] == '{' {
			low, high, err := lqueryQuantifier(ctx, text, &pos)
			if err != nil { return Value{}, err }
			lqueryWriteCount(&out, low, high, wildcard)
		}
		if pos == len(text) { break }
		if text[pos] != '.' { return Value{}, lquerySyntax(pos+1) }
		pos++
		if pos == len(text) { return Value{}, lqueryEnd() }
	}
	if oversized { return Value{}, lqueryError("54000", "lquery level is too large", "Total size of level exceeds the maximum allowed (65535 bytes).") }
	return Value{typ: d, lquery: &lqueryPayload{text: out.String()}}, nil
}

func (v Value) validLquery() bool { return v.lquery != nil && v.lquery.text != "" }

func (d *Descriptor) lqueryOutput(ctx context.Context, v Value) (string, error) {
	if err := ctx.Err(); err != nil { return "", err }
	if v.typ != d || v.null || !v.validLquery() { return "", fmt.Errorf("invalid lquery payload") }
	return v.lquery.text, nil
}

// RemapLquery is reserved for an explicit, source-authorized catalog import.
// Retag never authorizes crossing installation generations, even for NULL.
func (v Value) RemapLquery(target *Descriptor) (Value, error) {
	if !v.typ.IsLquery() || !target.IsLquery() || !v.null && !v.validLquery() { return Value{}, fmt.Errorf("incompatible imported lquery") }
	v.typ = target
	return v, nil
}
