package sqltype

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// CompositeMember is a live catalog attribute, not a physical table constraint.
// Attribute remains stable across rename, append and removal; gaps are legal.
type CompositeMember struct {
	Attribute int32
	Name      string
	Usage     Usage
}

type compositeDefinition struct {
	members    []CompositeMember
	attributes map[int32]int
}
type compositePayload struct {
	fields []Value
	shape  *Descriptor
}

// Composite freezes the catalog's coherent, ordered live-member projection.
// Catalog ownership and relation reciprocity are validated by the provider.
func Composite(id ID, name, declaration string, associatedArray ID, members []CompositeMember) (*Descriptor, error) {
	return compositeDescriptor(id, name, declaration, associatedArray, members, 1600, true, false)
}

// RecordShape is an expression occurrence, never a catalog storage identity.
// Query-result attributes may have type VOID; catalog composites may not.
func RecordShape(members []CompositeMember) (*Descriptor, error) {
	d, err := compositeDescriptor(2249, "record", "RECORD", 2287, members, 1664, false, false)
	if err == nil {
		d.database = "RECORD"
	}
	return d, err
}

// RecordSignatureShape owns declaration-only polymorphic output members. It
// grants no value or codec authority until every member has been specialized.
func RecordSignatureShape(members []CompositeMember) (*Descriptor, error) {
	d, err := compositeDescriptor(2249, "record", "RECORD", 2287, members, 1664, false, true)
	if err == nil {
		d.database = "RECORD"
	}
	return d, err
}

func compositeDescriptor(id ID, name, declaration string, associatedArray ID, members []CompositeMember, maximum int, catalog, signature bool) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" || len(members) > maximum || associatedArray == id {
		return nil, fmt.Errorf("invalid composite descriptor")
	}
	definition := &compositeDefinition{members: slices.Clone(members), attributes: make(map[int32]int, len(members))}
	var names map[string]bool
	if id != 2249 {
		names = make(map[string]bool, len(members))
	}
	generations := make(map[ID]*Descriptor)
	var coherent func(*Descriptor) bool
	coherent = func(d *Descriptor) bool {
		if d.id == id && id != 2249 {
			return false
		}
		// Anonymous shapes share a SQL OID, not a catalog generation.
		if d.id != 2249 {
			if old, found := generations[d.id]; found {
				return old == d
			}
			generations[d.id] = d
		}
		if d.IsDomain() {
			return coherent(d.base.Type)
		}
		if d.IsArray() {
			return coherent(d.element)
		}
		if d.IsComposite() {
			for _, member := range d.composite.members {
				if !coherent(member.Usage.Type) {
					return false
				}
			}
		}
		return true
	}
	deferred := false
	for i, member := range definition.members {
		admitted := member.Usage.Valid()
		if signature && member.Usage.Type != nil && PolymorphicFamily(member.Usage.Type.id) != 0 {
			d := member.Usage.Type
			admitted = d.polymorphicOwner == d && member.Usage == d.Usage()
			deferred = true
		}
		if member.Attribute <= 0 || member.Name == "" || len(member.Name) > 63 || !admitted || catalog && IsPseudo(member.Usage.Type.BaseType().id) || !coherent(member.Usage.Type) {
			return nil, fmt.Errorf("invalid composite member")
		}
		if err := ValidateTextInput(member.Name); err != nil {
			return nil, err
		}
		if _, exists := definition.attributes[member.Attribute]; exists {
			return nil, fmt.Errorf("duplicate composite attribute")
		}
		if names != nil {
			if names[member.Name] {
				return nil, fmt.Errorf("duplicate composite member name")
			}
			names[member.Name] = true
		}
		definition.attributes[member.Attribute] = i
	}
	return &Descriptor{id: id, name: name, sql: declaration, associatedArray: associatedArray, composite: definition, lookupOnly: deferred}, nil
}

func (d *Descriptor) IsComposite() bool { return d != nil && d.composite != nil }
func (d *Descriptor) CompositeLen() int {
	if !d.IsComposite() {
		return 0
	}
	return len(d.composite.members)
}
func (d *Descriptor) CompositeMember(i int) CompositeMember { return d.composite.members[i] }

func (d *Descriptor) CompositeAttributeIndex(attribute int32) (int, bool) {
	if !d.IsComposite() {
		return 0, false
	}
	i, ok := d.composite.attributes[attribute]
	return i, ok
}

// CompositeValue freezes external storage; OwnedCompositeValue transfers an
// exclusively owned buffer. Neither accessor exposes a mutable slice.
func CompositeValue(d *Descriptor, fields []Value) (Value, error) {
	v, err := OwnedCompositeValue(d, fields)
	if err != nil {
		return Value{}, err
	}
	v.composite.fields = slices.Clone(fields)
	return v, nil
}
func OwnedCompositeValue(d *Descriptor, fields []Value) (Value, error) {
	if !d.Concrete() || !d.BaseType().IsComposite() || len(fields) != d.BaseType().CompositeLen() {
		return Value{}, fmt.Errorf("invalid composite payload descriptor or arity")
	}
	for i, field := range fields {
		if field.typ != d.BaseType().composite.members[i].Usage.Type {
			return Value{}, fmt.Errorf("foreign composite field generation")
		}
	}
	return Value{typ: d, composite: &compositePayload{fields: fields, shape: d}}, nil
}
func (v Value) CompositeLen() int {
	if v.null || v.composite == nil {
		return 0
	}
	return len(v.composite.fields)
}
func (v Value) CompositeField(i int) Value { return v.composite.fields[i] }

func (v Value) validCompositePayload() bool {
	if v.typ == nil || !v.typ.BaseType().IsComposite() || v.composite == nil || len(v.composite.fields) != v.typ.BaseType().CompositeLen() {
		return false
	}
	for i, field := range v.composite.fields {
		if field.typ != v.typ.BaseType().composite.members[i].Usage.Type {
			return false
		}
	}
	return true
}

// MapComposite is an authorized catalog transformation, never a structural SQL
// cast. Retained fields are mapped by stable attribute, missing fields become
// exact typed NULLs, and dropped fields disappear. Unchanged payloads are shared.
func (v Value) MapComposite(target *Descriptor, transform func(Value, *Descriptor) (Value, error)) (Value, error) {
	if v.typ != nil && v.typ.ID() == 10029 && v.typ.Usage().CatalogNullUsage() {
		if target == nil || target.ID() != 10029 || !target.Usage().CatalogNullUsage() || !v.validStatisticResult() {
			return Value{}, fmt.Errorf("invalid statistic row mapping")
		}
		if v.null {
			return ResultNull(target.Usage()), nil
		}
		var fields []Value
		for i, old := range v.composite.fields {
			next, err := transform(old, target.CompositeMember(i).Usage.Type)
			if err != nil {
				return Value{}, err
			}
			if !next.ValidResult(target.CompositeMember(i).Usage) {
				return Value{}, fmt.Errorf("invalid mapped statistic field")
			}
			if next != old && fields == nil {
				fields = slices.Clone(v.composite.fields)
			}
			if fields != nil {
				fields[i] = next
			}
		}
		if fields == nil {
			v.typ = target
			return v, nil
		}
		return OwnedStatisticRowValue(target, fields)
	}
	if !v.typ.Concrete() || !v.typ.BaseType().IsComposite() || !target.Concrete() || !target.BaseType().IsComposite() {
		return Value{}, fmt.Errorf("invalid composite mapping")
	}
	if v.null {
		return Null(target), nil
	}
	if !v.validCompositePayload() {
		return Value{}, fmt.Errorf("invalid composite payload")
	}
	from, to := v.typ.BaseType().composite, target.BaseType().composite
	var fields []Value
	if len(from.members) != len(to.members) {
		fields = make([]Value, len(to.members))
	}
	for i, member := range to.members {
		at, found := from.attributes[member.Attribute]
		var next Value
		if found {
			if transform == nil {
				return Value{}, fmt.Errorf("missing composite transformation")
			}
			var err error
			next, err = transform(v.composite.fields[at], member.Usage.Type)
			if err != nil {
				return Value{}, err
			}
			if next.typ != member.Usage.Type {
				return Value{}, fmt.Errorf("invalid mapped field generation")
			}
		} else {
			next = Null(member.Usage.Type)
		}
		if fields == nil && (!found || at != i || next.typ != v.composite.fields[i].typ || next != v.composite.fields[i] && !SameDatum(next, v.composite.fields[i])) {
			fields = slices.Clone(v.composite.fields)
		}
		if fields != nil {
			fields[i] = next
		}
	}
	v.typ = target
	if fields != nil {
		v.composite = &compositePayload{fields: fields}
	}
	return v, nil
}

type compositeReader struct {
	ctx   context.Context
	input string
	at    int
}

// Check encoding with bounded cancellation, retaining the shared diagnostics.
func validateCompositeInput(ctx context.Context, text string) error {
	for i, count := 0, 0; i < len(text); count++ {
		if count&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		_, width := utf8.DecodeRuneInString(text[i:])
		if text[i] == 0 || width == 1 && text[i] >= utf8.RuneSelf {
			return ValidateTextInput(text[i:])
		}
		i += width
	}
	return ctx.Err()
}
func (p *compositeReader) malformed(detail string) error {

	err := failure("22P02", fmt.Sprintf("malformed record literal: \"%s\"", p.input)).(*sqlstate.Error)
	err.Detail = detail
	return err
}
func (p *compositeReader) whitespace() error {
	for p.at < len(p.input) && space(p.input[p.at]) {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		p.at++
	}
	return nil
}
func (p *compositeReader) field() (string, bool, error) {
	if p.at == len(p.input) {
		return "", false, p.malformed("Unexpected end of input.")
	}
	if p.input[p.at] == ',' || p.input[p.at] == ')' {
		return "", true, nil
	}
	start := p.at
	quoted, changed := false, false
	var out strings.Builder
	for p.at < len(p.input) {
		if err := p.ctx.Err(); err != nil {
			return "", false, err
		}
		c := p.input[p.at]
		if c == '\\' || c == '"' {
			out.WriteString(p.input[start:p.at])
			changed = true
			p.at++
			if c == '\\' {
				if p.at == len(p.input) {
					return "", false, p.malformed("Unexpected end of input.")
				}
				out.WriteByte(p.input[p.at])
				p.at++
			} else if quoted && p.at < len(p.input) && p.input[p.at] == '"' {
				out.WriteByte('"')
				p.at++
			} else {
				quoted = !quoted
			}
			start = p.at
			continue
		}
		if !quoted && (c == ',' || c == ')') {
			if !changed {
				return p.input[start:p.at], false, nil
			}
			out.WriteString(p.input[start:p.at])
			return out.String(), false, nil
		}
		p.at++
	}
	return "", false, p.malformed("Unexpected end of input.")
}
func (d *Descriptor) compositeInput(ctx context.Context, input string, env InputEnvironment) (Value, error) {
	p := compositeReader{ctx: ctx, input: input}
	if err := p.whitespace(); err != nil {
		return Value{}, err
	}
	if p.at == len(input) || input[p.at] != '(' {
		return Value{}, p.malformed("Missing left parenthesis.")
	}
	p.at++
	fields := make([]Value, d.CompositeLen())
	for i, member := range d.composite.members {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if i > 0 {
			if p.at == len(input) {
				return Value{}, p.malformed("Unexpected end of input.")
			}
			if input[p.at] == ')' {
				return Value{}, p.malformed("Too few columns.")
			}
			p.at++ // The preceding field stopped at a comma.
		}
		text, null, err := p.field()
		if err != nil {
			return Value{}, err
		}
		if null {
			fields[i] = Null(member.Usage.Type)
			if err := member.Usage.Type.CheckDomainInput(ctx, fields[i], env); err != nil {
				return Value{}, err
			}
		} else {
			fields[i], err = member.Usage.Type.InputWithEnvironment(ctx, text, member.Usage.Typmod, env)
			if err != nil {
				return Value{}, err
			}
		}
	}
	if p.at == len(input) || input[p.at] != ')' {
		return Value{}, p.malformed("Too many columns.")
	}
	p.at++
	if err := p.whitespace(); err != nil {
		return Value{}, err
	}
	if p.at != len(input) {
		return Value{}, p.malformed("Junk after right parenthesis.")
	}
	return OwnedCompositeValue(d, fields)
}
func (d *Descriptor) compositeOutput(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	if !v.validCompositePayload() {
		return "", fmt.Errorf("invalid composite payload")
	}
	var out strings.Builder
	out.WriteByte('(')
	for i, field := range v.composite.fields {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i > 0 {
			out.WriteByte(',')
		}
		if field.null {
			continue
		}
		text, err := field.typ.OutputWithEnvironment(ctx, field, env)
		if err != nil {
			return "", err
		}
		quote := len(text) == 0
		for j := range len(text) {
			if j&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			c := text[j]
			if c == '(' || c == ')' || c == ',' || c == '"' || c == '\\' || space(c) {
				quote = true
			}
		}
		if quote {
			out.WriteByte('"')
		}
		for j := range len(text) {
			if j&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			c := text[j]
			if c == '"' || c == '\\' {
				out.WriteByte(c)
			}
			out.WriteByte(c)
		}
		if quote {
			out.WriteByte('"')
		}
	}
	out.WriteByte(')')
	return out.String(), nil
}
