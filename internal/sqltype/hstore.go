package sqltype

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// Hstore constructs an extension-owned scalar generation, not a TEXT alias.
func Hstore(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid hstore descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "HSTORE", hstore: true}, nil
}

func (d *Descriptor) IsHstore() bool { return d != nil && d.hstore }

type hstorePair struct {
	key, value string
	null       bool
}

type hstorePayload struct{ pairs []hstorePair }

func hstoreKeyCompare(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func hstoreSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

type hstoreParser struct {
	ctx        context.Context
	text       string
	pos        int
	checkpoint int
}

func (p *hstoreParser) syntax() error {
	message := "syntax error in hstore: unexpected end of string"
	if p.pos < len(p.text) {
		_, size := utf8.DecodeRuneInString(p.text[p.pos:])
		message = fmt.Sprintf("syntax error in hstore, near \"%s\" at position %d", p.text[p.pos:p.pos+size], p.pos)
	}
	return sqlstate.SoftInputError(failure("42601", message))
}

// Escapes can advance by two bytes, so compare against a frontier rather than
// testing a byte-position modulus that an escaped run could skip forever.
func (p *hstoreParser) check() error {
	if p.pos < p.checkpoint {
		return nil
	}
	p.checkpoint = p.pos + 256
	return p.ctx.Err()
}

func (p *hstoreParser) space() error {
	for p.pos < len(p.text) && hstoreSpace(p.text[p.pos]) {
		if err := p.check(); err != nil {
			return err
		}
		p.pos++
	}
	return p.ctx.Err()
}

// token owns its decoded bytes; unescaped text uses one clone, escaped text one
// builder. The input string and parser scratch are never retained by the datum.
func (p *hstoreParser) token(key bool) (string, bool, error) {
	if err := p.space(); err != nil {
		return "", false, err
	}
	if p.pos == len(p.text) {
		return "", false, p.syntax()
	}
	quoted := p.text[p.pos] == '"'
	if quoted {
		p.pos++
	}
	start, segment := p.pos, p.pos
	var decoded strings.Builder
	escaped := false
	for p.pos < len(p.text) {
		if err := p.check(); err != nil {
			return "", false, err
		}
		c := p.text[p.pos]
		if c == '\\' {
			decoded.WriteString(p.text[segment:p.pos])
			p.pos++
			if p.pos == len(p.text) {
				return "", false, p.syntax()
			}
			decoded.WriteByte(p.text[p.pos])
			p.pos++
			segment, escaped = p.pos, true
			continue
		}
		if quoted && c == '"' {
			var text string
			if escaped {
				decoded.WriteString(p.text[segment:p.pos])
				text = decoded.String()
			} else {
				text = strings.Clone(p.text[start:p.pos])
			}
			p.pos++
			return text, true, nil
		}
		if !quoted && (hstoreSpace(c) || key && c == '=' || !key && c == ',' && p.pos != start) {
			break
		}
		p.pos++
	}
	if quoted || p.pos == start {
		return "", false, p.syntax()
	}
	if escaped {
		decoded.WriteString(p.text[segment:p.pos])
		return decoded.String(), false, nil
	}
	return strings.Clone(p.text[start:p.pos]), false, nil
}

func (d *Descriptor) hstoreInput(ctx context.Context, text string) (Value, error) {
	p := hstoreParser{ctx: ctx, text: text}
	var pairs []hstorePair
	for {
		if err := p.space(); err != nil {
			return Value{}, err
		}
		if p.pos == len(text) {
			break
		}
		key, _, err := p.token(true)
		if err != nil {
			return Value{}, err
		}
		if err := p.space(); err != nil {
			return Value{}, err
		}
		if p.pos == len(text) || text[p.pos] != '=' {
			return Value{}, p.syntax()
		}
		p.pos++
		if p.pos == len(text) || text[p.pos] != '>' {
			return Value{}, p.syntax()
		}
		p.pos++
		value, quoted, err := p.token(false)
		if err != nil {
			return Value{}, err
		}
		null := !quoted && strings.EqualFold(value, "NULL")
		if null {
			value = ""
		}
		pairs = append(pairs, hstorePair{key: key, value: value, null: null})
		if err := p.space(); err != nil {
			return Value{}, err
		}
		if p.pos == len(text) {
			break
		}
		if text[p.pos] != ',' {
			return Value{}, p.syntax()
		}
		p.pos++
	}
	return d.ownedHstore(ctx, pairs)
}

// ownedHstore takes exclusive ownership and establishes the payload invariants.
func (d *Descriptor) ownedHstore(ctx context.Context, pairs []hstorePair) (Value, error) {
	var sortErr error
	comparisons := 0
	slices.SortFunc(pairs, func(a, b hstorePair) int {
		comparisons++
		if comparisons&255 == 0 {
			sortErr = ctx.Err()
		}
		if sortErr != nil {
			return 0
		}
		return hstoreKeyCompare(a.key, b.key)
	})
	if sortErr != nil {
		return Value{}, sortErr
	}
	pairs = slices.CompactFunc(pairs, func(a, b hstorePair) bool { return a.key == b.key })
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: d, hstore: &hstorePayload{pairs: pairs}}, nil
}

// Checked input and typed constructors publish owned, immutable private pairs.
// Consumers validate carrier presence, not UTF8, sorting and uniqueness again.
func (v Value) validHstore() bool {
	return v.hstore != nil
}

func (d *Descriptor) hstoreOutput(ctx context.Context, v Value) (string, error) {
	if v.typ != d || v.null || !v.validHstore() {
		return "", fmt.Errorf("invalid hstore payload")
	}
	var out strings.Builder
	size := 0
	for i, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			size += 2
		}
		size += len(pair.key) + strings.Count(pair.key, `"`) + strings.Count(pair.key, `\`) + 4
		if pair.null {
			size += 4
		} else {
			size += len(pair.value) + strings.Count(pair.value, `"`) + strings.Count(pair.value, `\`) + 2
		}
	}
	out.Grow(size)
	quote := func(text string) error {
		out.WriteByte('"')
		for i := range len(text) {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if text[i] == '"' || text[i] == '\\' {
				out.WriteByte('\\')
			}
			out.WriteByte(text[i])
		}
		out.WriteByte('"')
		return nil
	}
	for i, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			out.WriteString(", ")
		}
		if err := quote(pair.key); err != nil {
			return "", err
		}
		out.WriteString("=>")
		if pair.null {
			out.WriteString("NULL")
		} else if err := quote(pair.value); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// RemapHstore is used only after the catalog authorizes source/destination roles.
// Ordinary Retag rejects foreign generations, even when their OIDs coincide.
func (v Value) RemapHstore(target *Descriptor) (Value, error) {
	if !v.typ.IsHstore() || !target.IsHstore() || !v.null && !v.validHstore() {
		return Value{}, fmt.Errorf("incompatible imported hstore")
	}
	v.typ = target
	return v, nil
}

// HstoreArray converts private immutable pairs into an exclusively owned TEXT
// array. The binder supplies the authentic installed source generation.
func (v Value) HstoreArray(ctx context.Context, source, result *Descriptor, matrix bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !source.IsHstore() || v.Type() != source || result == nil || result.ID() != 1009 || !result.Concrete() || !result.IsArray() || result.Element().ID() != Text {
		return Value{}, fmt.Errorf("invalid hstore array conversion")
	}
	if v.null {
		return Null(result), nil
	}
	if !v.validHstore() {
		return Value{}, fmt.Errorf("invalid hstore payload")
	}
	n := len(v.hstore.pairs)
	if uint64(n) > uint64(1<<31-2)/2 {
		return Value{}, failure("54000", "array size exceeds the maximum allowed")
	}
	if n == 0 {
		return OwnedArrayValue(result, nil, nil)
	}
	elements := make([]Value, 2*n)
	text := result.Element()
	for i, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		elements[2*i] = String(text, pair.key)
		if pair.null {
			elements[2*i+1] = Null(text)
		} else {
			elements[2*i+1] = String(text, pair.value)
		}
	}
	var dimensions []ArrayDimension
	if matrix {
		dimensions = []ArrayDimension{{Lower: 1, Length: int32(n)}, {Lower: 1, Length: 2}}
	} else {
		dimensions = []ArrayDimension{{Lower: 1, Length: int32(2 * n)}}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	value, err := OwnedArrayValue(result, dimensions, elements)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return value, nil
}
