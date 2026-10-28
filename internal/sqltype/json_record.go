package sqltype

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/workadmission"
)

// DeferredRecord transports only a typed SQL NULL until a genuine descriptor
// is available. It is deliberately not a zero-field composite or storage type.
func DeferredRecord() *Descriptor {
	return &Descriptor{id: 2249, name: "record", sql: "RECORD", database: "RECORD", associatedArray: 2287}
}
func (d *Descriptor) IsDeferredRecord() bool {
	return d != nil && d.id == 2249 && !d.lookupOnly && d.composite == nil
}

// JSONRecordPlan contains occurrence-owned type authority, never document,
// evaluated base, conversion buffers or cursor state. Args remain in SQL order.
type JSONRecordPlan struct {
	name                        string
	args                        [2]Usage
	count, baseIndex, jsonIndex int
	result                      Usage
}

func BindJSONRecord(name string, result Usage, baseIndex int, args ...Usage) (JSONRecordPlan, error) {
	p := JSONRecordPlan{name: name, result: result, count: len(args), baseIndex: baseIndex}
	populate := name == "json_populate_record" || name == "json_populate_recordset"
	if !populate && name != "json_to_record" && name != "json_to_recordset" || !result.Valid() || len(args) != 1 && len(args) != 2 {
		return p, fmt.Errorf("invalid JSON record plan")
	}
	if populate {
		if len(args) != 2 || baseIndex < 0 || baseIndex > 1 {
			return p, fmt.Errorf("invalid JSON record base slot")
		}
		p.jsonIndex = 1 - baseIndex
	} else if len(args) != 1 || baseIndex != -1 {
		return p, fmt.Errorf("invalid JSON record arguments")
	}
	for i, u := range args {
		if !u.Valid() {
			return p, fmt.Errorf("invalid JSON record argument usage")
		}
		p.args[i] = u
	}
	if args[p.jsonIndex].Type.ID() != JSON {
		return p, fmt.Errorf("invalid JSON record input usage")
	}
	if populate && args[baseIndex].Type.ID() != 2249 && result.Type != args[baseIndex].Type {
		return p, fmt.Errorf("foreign JSON record result generation")
	}
	if !populate && (result.Type.ID() != 2249 || !result.Type.IsComposite() && !result.Type.IsDeferredRecord()) {
		return p, fmt.Errorf("invalid JSON record result usage")
	}
	return p, nil
}
func (p JSONRecordPlan) Name() string   { return p.name }
func (p JSONRecordPlan) BaseIndex() int { return p.baseIndex }
func (p JSONRecordPlan) ReturnsSet() bool {
	return p.name == "json_populate_recordset" || p.name == "json_to_recordset"
}
func (p JSONRecordPlan) ResultUsage() Usage { return p.result }

// Open retains syntax-only recordset row offsets. Processing decode/conversion
// belongs to each demanded row, so later Unicode cannot mask an earlier error.
func (p JSONRecordPlan) Open(ctx context.Context, args []Value, env InputEnvironment) (JSONRecordCursor, error) {
	c := JSONRecordCursor{plan: p, env: env, next: -1}
	if len(args) != p.count || !p.result.Valid() {
		return c, fmt.Errorf("invalid JSON record invocation")
	}
	for i, v := range args {
		if v.typ != p.args[i].Type {
			return c, fmt.Errorf("foreign JSON record argument generation")
		}
	}
	if err := ctx.Err(); err != nil {
		return c, err
	}
	conversion := p.result
	if p.baseIndex >= 0 {
		c.base = args[p.baseIndex]
		conversion = p.args[p.baseIndex]
		if !conversion.Type.BaseType().IsComposite() && !conversion.Type.IsDeferredRecord() {
			return c, failure("42804", "first argument of "+p.name+" must be a row type")
		}
		if conversion.Type.IsDeferredRecord() && p.result.Type.IsComposite() {
			// A contextual FROM descriptor supplies the shape of NULL::record.
			conversion = p.result
			c.base = Null(conversion.Type)
		}
	} else {
		c.base = Null(conversion.Type)
	}
	input := args[p.jsonIndex]
	if input.null {
		if !p.ReturnsSet() {
			c.scalarNull = true
		}
		return c, nil
	}
	if !conversion.Type.BaseType().IsComposite() {
		return c, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "could not determine row type for result of " + p.name, Hint: "Provide a non-null record argument, or call the function in the FROM clause using a column definition list."}
	}
	c.conversion = conversion
	if p.ReturnsSet() {
		scan := jsonScanResult{mode: jsonScanElements, recordName: p.name, recordSet: true, workClass: workadmission.WorkJSONRecordExpand}
		if _, err := input.typ.scanJSON(ctx, input.text, &scan); err != nil {
			return JSONRecordCursor{}, err
		}
		c.rows, c.text, c.next = scan.members, input.text, 0
	} else {
		c.document.text = input.text
		scan := jsonScanResult{record: &c.document, recordName: p.name, workClass: workadmission.WorkJSONRecordExpand}
		if _, err := input.typ.scanJSON(ctx, input.text, &scan); err != nil {
			return JSONRecordCursor{}, err
		}
		c.next = 0
	}
	return c, nil
}

func (p JSONRecordPlan) Evaluate(ctx context.Context, args []Value, env InputEnvironment) (Value, error) {
	if p.ReturnsSet() {
		return Value{}, fmt.Errorf("unlifted JSON recordset")
	}
	c, err := p.Open(ctx, args, env)
	if err != nil {
		return Value{}, err
	}
	defer c.Close()
	v, _, err := c.Next(ctx)
	return v, err
}

type JSONRecordCursor struct {
	plan                 JSONRecordPlan
	env                  InputEnvironment
	base                 Value
	conversion           Usage
	document             jsonRecordDocument
	next                 int
	rows                 []jsonMember
	text                 string
	scalarNull, consumed bool
}

func (c *JSONRecordCursor) Close() { *c = JSONRecordCursor{next: -1, consumed: true} }
func (c *JSONRecordCursor) Next(ctx context.Context) (Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, false, err
	}
	if c.consumed {
		return Value{}, false, nil
	}
	if c.scalarNull {
		c.consumed = true
		return c.finish(c.base)
	}
	if c.next < 0 {
		return Value{}, false, nil
	}
	at := c.next
	if c.plan.ReturnsSet() {
		if c.next == len(c.rows) {
			return Value{}, false, nil
		}
		row := c.rows[c.next]
		c.next++
		text := c.text[row.start:row.end]
		if row.kind != '{' {
			if row.kind == 's' {
				if err := jsonRecordCheckString(ctx, text, c.text, row.start); err != nil {
					return Value{}, false, err
				}
			}
			return Value{}, false, failure("22023", "argument of "+c.plan.name+" must be an array of objects")
		}
		clear(c.document.nodes)
		clear(c.document.keys)
		c.document.text = text
		c.document.nodes, c.document.keys, c.document.open = c.document.nodes[:0], c.document.keys[:0], c.document.open[:0]
		scan := jsonScanResult{record: &c.document, recordName: c.plan.name, origin: c.text, originOffset: row.start, workClass: workadmission.WorkJSONRecordExpand}
		if _, err := c.plan.args[c.plan.jsonIndex].Type.scanJSON(ctx, text, &scan); err != nil {
			return Value{}, false, err
		}
		at = 0
	} else {
		c.consumed = true
	}
	v, err := c.document.convert(ctx, at, c.conversion, c.base, c.env)
	if err != nil {
		return Value{}, false, err
	}
	return c.finish(v)
}

func (c *JSONRecordCursor) finish(v Value) (Value, bool, error) {
	if v.null {
		return Null(c.plan.result.Type), true, nil
	}
	if v.typ == c.plan.result.Type {
		return v, true, nil
	}
	actual, expected := v.typ.BaseType(), c.plan.result.Type.BaseType()
	if actual.CompositeLen() != expected.CompositeLen() {
		noun := "attributes"
		if actual.CompositeLen() == 1 {
			noun = "attribute"
		}
		return Value{}, false, &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804",
			Message: "function return row and query-specified return row do not match",
			Detail:  fmt.Sprintf("Returned row contains %d %s, but query expects %d.", actual.CompositeLen(), noun, expected.CompositeLen()),
		}
	}
	ordinal := 0
	v, err := v.MapComposite(c.plan.result.Type, func(field Value, target *Descriptor) (Value, error) {
		ordinal++
		return jsonRecordOutputField(field, target, ordinal)
	})
	return v, err == nil, err
}

// Output reconciliation uses the existing directional implicit-binary catalog
// facts, not assignment coercion or typmod sizing. Same-OID foreign generations
// are never identity; domain sources may expose their actual base generation,
// but a destination domain cannot acquire unchecked domain identity.
func jsonRecordBinaryOutput(from, to *Descriptor) bool {
	if !from.Concrete() || !to.Concrete() {
		return false
	}
	if from == to {
		return true
	}
	if to.IsDomain() {
		return false
	}
	from = from.BaseType()
	if from == to {
		return true
	}
	if from.id == to.id {
		return false
	}
	for _, cast := range castMetadata {
		if cast.Source == from.id && cast.Target == to.id && cast.Context == 'i' && cast.Method == 'b' {
			return true
		}
	}
	return false
}

func jsonRecordOutputField(field Value, target *Descriptor, ordinal int) (Value, error) {
	if !jsonRecordBinaryOutput(field.typ, target) {
		return Value{}, &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804",
			Message: "function return row and query-specified return row do not match",
			Detail:  fmt.Sprintf("Returned type %s at ordinal position %d, but query expects %s.", field.typ.DiagnosticName(), ordinal, target.DiagnosticName()),
		}
	}
	if field.typ == target {
		return field, nil
	}
	if field.null {
		return Null(target), nil
	}
	if field.typ.BaseType().id == Int4 && target.id == Oid {
		// PostgreSQL's binary edge preserves int4 bits. Native uses a separate
		// OID slot, so reuse its representation translation after admission.
		return oidCoercion(field, target)
	}
	field.typ = target
	return field, nil
}

func jsonRecordCheckString(ctx context.Context, text, origin string, offset int) error {
	lexer := jsonLexer{ctx: ctx, text: text, line: 1, decode: true, discard: true, origin: origin, originOffset: offset}
	return lexer.next()
}

type jsonRecordNode struct {
	key, decoded                  string
	kind                          byte
	start, end, first, last, next int
}

// Flat adjacency avoids recursive parsing and retains source slices without a
// second JSON representation or scalar numeric normalization.
type jsonRecordDocument struct {
	text  string
	nodes []jsonRecordNode
	open  []int
	keys  []string
}

func (d *jsonRecordDocument) finish(l *jsonLexer, depth int, state jsonState) {
	if depth >= len(d.open) || d.open[depth] < 0 || state != jsonEnd && state != jsonObjectNext && state != jsonArrayNext {
		return
	}
	d.nodes[d.open[depth]].end = l.end
	d.open[depth] = -1
}
func (d *jsonRecordDocument) token(l *jsonLexer, depth int, state jsonState) {
	for len(d.open) <= depth {
		d.open = append(d.open, -1)
		d.keys = append(d.keys, "")
	}
	if (state == jsonObjectFirst || state == jsonObjectKey) && l.kind == 's' {
		d.keys[depth] = l.decoded
		return
	}
	if state != jsonValue && state != jsonObjectValue && state != jsonArrayValue && (state != jsonArrayFirst || l.kind == ']') {
		return
	}
	i := len(d.nodes)
	d.nodes = append(d.nodes, jsonRecordNode{key: d.keys[depth], decoded: l.decoded, kind: l.kind, start: l.start, end: l.end, first: -1, last: -1, next: -1})
	if depth > 0 {
		parent := &d.nodes[d.open[depth-1]]
		if parent.first < 0 {
			parent.first = i
		} else {
			d.nodes[parent.last].next = i
		}
		parent.last = i
	}
	d.open[depth] = i
}

type jsonRecordFrame struct {
	node, next int
	usage      Usage
	base       Value
	key        string
	members    map[string]int
	fields     []Value
	leaves     []int
	dimensions []ArrayDimension
	phase      byte
}

// convert uses an explicit work stack: schema depth and nested arrays never
// consume the Go call stack. Every fresh domain/null construction is checked.
func (d *jsonRecordDocument) convert(ctx context.Context, node int, usage Usage, base Value, env InputEnvironment) (Value, error) {
	var local [16]jsonRecordFrame
	stack := append(local[:0], jsonRecordFrame{node: node, usage: usage, base: base})
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		f := &stack[len(stack)-1]
		target := f.usage.Type
		physical := target.BaseType()
		var value Value
		var err error
		fresh := true
		if f.phase == 0 {
			if f.node < 0 && !f.base.null && f.base.typ == target {
				value, fresh = f.base, false
			} else if f.node < 0 || d.nodes[f.node].kind == 'z' {
				value = Null(physical)
			} else {
				n := &d.nodes[f.node]
				switch {
				case physical.IsComposite() && n.kind == '{':
					if n.first >= 0 && physical.CompositeLen() != 0 {
						f.members = make(map[string]int, physical.CompositeLen())
						for i := n.first; i >= 0; i = d.nodes[i].next {
							if err := ctx.Err(); err != nil {
								return Value{}, err
							}
							f.members[d.nodes[i].key] = i
						}
					}
					f.fields = make([]Value, physical.CompositeLen())
					f.phase = 1
					continue
				case physical.IsComposite() && n.kind != 's':
					kind := "a scalar"
					if n.kind == '[' {
						kind = "an array"
					}
					return Value{}, failure("22023", "cannot call populate_composite on "+kind)
				case physical.IsArray() && n.kind != 's':
					if n.kind != '[' {
						return Value{}, jsonRecordArrayExpected(f.key, "")
					}
					f.dimensions, f.leaves, err = d.arrayShape(ctx, f.node, f.key)
					if err != nil {
						return Value{}, err
					}
					f.fields = make([]Value, len(f.leaves))
					f.phase = 2
					continue
				default:
					text := d.text[n.start:n.end]
					if n.kind == 's' {
						text = n.decoded
						if physical.id == JSON || physical.id == JSONB {
							var out strings.Builder
							if err := writeJSONBString(ctx, &out, text); err != nil {
								return Value{}, err
							}
							text = out.String()
						}
					}
					if physical.id == JSON {
						value = Value{typ: physical, text: text}
					} else {
						value, err = target.InputWithEnvironment(ctx, text, f.usage.Typmod, env)
						fresh = false // The input codec owns domain checks.
					}
				}
			}
		} else if f.next < len(f.fields) {
			i := f.next
			f.next++
			child := jsonRecordFrame{node: -1}
			if f.phase == 1 {
				member := physical.CompositeMember(i)
				child.usage, child.key = member.Usage, member.Name
				if at, found := f.members[member.Name]; found {
					child.node = at
				}
				child.base = Null(member.Usage.Type)
				if !f.base.null && f.base.composite != nil {
					child.base = f.base.CompositeField(i)
				}
			} else {
				child.node, child.usage, child.key = f.leaves[i], physical.element.Usage(), f.key
				child.usage.Typmod = f.usage.BaseUsage().Typmod
				child.usage.Collation = f.usage.Collation
				child.base = Null(child.usage.Type)
			}
			stack = append(stack, child)
			continue
		} else if f.phase == 1 {
			value, err = OwnedCompositeValue(physical, f.fields)
		} else {
			value, err = OwnedArrayValue(physical, f.dimensions, f.fields)
		}
		if err != nil {
			return Value{}, err
		}
		if fresh && target.IsDomain() {
			if err := target.CheckDomainInput(ctx, value, env); err != nil {
				return Value{}, err
			}
			value, err = target.Wrap(value)
			if err != nil {
				return Value{}, err
			}
		}
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			return value, nil
		}
		parent := &stack[len(stack)-1]
		parent.fields[parent.next-1] = value
	}
	return Value{}, fmt.Errorf("invalid JSON record conversion")
}

func jsonRecordArrayExpected(key, path string) error {
	hint := fmt.Sprintf("See the value of key %q.", key)
	if path != "" {
		hint = fmt.Sprintf("See the array element %s of key %q.", path, key)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "expected JSON array", Hint: hint}
}
func (d *jsonRecordDocument) arrayShape(ctx context.Context, root int, key string) ([]ArrayDimension, []int, error) {
	type frame struct {
		child, index int
		entered      bool
	}
	var frames [6]frame
	stack := frames[:0]
	var dims []ArrayDimension
	var leaves []int
	leafDepth := -1
	ragged := func() error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "malformed JSON array", Detail: "Multidimensional arrays must have sub-arrays with matching dimensions."}
	}
	stack = append(stack, frame{child: d.nodes[root].first})
	errorPath := func() string {
		var out strings.Builder
		for _, f := range stack {
			fmt.Fprintf(&out, "[%d]", f.index-1)
		}
		return out.String()
	}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		f := &stack[len(stack)-1]
		depth := len(stack)
		if !f.entered {
			count := int32(0)
			for i := f.child; i >= 0; i = d.nodes[i].next {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
				count++
			}
			if len(dims) < depth {
				dims = append(dims, ArrayDimension{Lower: 1, Length: count})
			} else if dims[depth-1].Length != count {
				return nil, nil, ragged()
			}
			f.entered = true
		}
		if f.child < 0 {
			stack = stack[:len(stack)-1]
			continue
		}
		at := f.child
		f.child = d.nodes[at].next
		f.index++
		if d.nodes[at].kind == '[' {
			if leafDepth >= 0 && leafDepth <= depth {
				return nil, nil, ragged()
			}
			if depth == 6 {
				return nil, nil, failure("54000", "number of array dimensions (7) exceeds the maximum allowed (6)")
			}
			stack = append(stack, frame{child: d.nodes[at].first})
			continue
		}
		if len(dims) > depth {
			return nil, nil, jsonRecordArrayExpected(key, errorPath())
		}
		if leafDepth < 0 {
			leafDepth = depth
		}
		if leafDepth != depth {
			return nil, nil, jsonRecordArrayExpected(key, errorPath())
		}
		leaves = append(leaves, at)
	}
	// Zero elements canonicalize to PostgreSQL's zero-dimensional empty array.
	if len(leaves) == 0 {
		return nil, nil, nil
	}
	return dims, leaves, nil
}
