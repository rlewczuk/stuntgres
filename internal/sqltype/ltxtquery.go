package sqltype

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
)

// Ltxtquery constructs an installation-owned scalar identity.
func Ltxtquery(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" { return nil, fmt.Errorf("invalid ltxtquery descriptor") }
	return &Descriptor{id: id, name: name, sql: declaration, database: "LTXTQUERY", ltxtquery: true}, nil
}

func (d *Descriptor) IsLtxtquery() bool { return d != nil && d.ltxtquery }
type ltxtqueryPayload struct {
	text string
	nodes []ltxtqueryNode
}

func ltxtqueryError(code, message string) error {
	err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if code == "XX000" { return err }
	return sqlstate.SoftInputError(err)
}

// Postfix nodes own offsets into the final immutable canonical text.
type ltxtqueryNode struct {
	op byte
	flags byte
	start, end, left, right, count int
}
type ltxtqueryFrame struct { operators [32]byte; count int }

func (d *Descriptor) ltxtqueryInput(ctx context.Context, text string) (Value, error) {
	nodes := make([]ltxtqueryNode, 0)
	values := make([]int, 0)
	frames := []ltxtqueryFrame{{}}
	storage, pos := 0, 0
	operand := true
	reduce := func(op byte) error {
		if len(values) == 0 { return ltxtqueryError("42601", "syntax error") }
		right := values[len(values)-1]
		values = values[:len(values)-1]
		n := ltxtqueryNode{op: op, right: right, count: 1 + nodes[right].count}
		if op != '!' {
			if len(values) == 0 { return ltxtqueryError("42601", "syntax error") }
			n.left = values[len(values)-1]
			values = values[:len(values)-1]
			n.count += nodes[n.left].count
		}
		nodes = append(nodes, n)
		values = append(values, len(nodes)-1)
		return nil
	}
	flush := func(all bool) error {
		f := &frames[len(frames)-1]
		for f.count > 0 && (all || f.operators[f.count-1] != '|') {
			if err := ctx.Err(); err != nil { return err }
			f.count--
			if err := reduce(f.operators[f.count]); err != nil { return err }
		}
		return nil
	}
	for {
		if err := ctx.Err(); err != nil { return Value{}, err }
		if operand {
			for pos < len(text) && strings.ContainsRune(" \t\n\r\f\v", rune(text[pos])) {
				if pos&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
				pos++
			}
			if pos == len(text) { return Value{}, ltxtqueryError("42601", "operand syntax error") }
			switch text[pos] {
			case '!':
				f := &frames[len(frames)-1]
				if f.count == len(f.operators) { return Value{}, ltxtqueryError("XX000", "stack too short") }
				f.operators[f.count] = '!'; f.count++; pos++; continue
			case '(':
				frames = append(frames, ltxtqueryFrame{}); pos++; continue
			}
			if !lqueryLabel(text[pos]) { return Value{}, ltxtqueryError("42601", "operand syntax error") }
			start := pos
			for pos < len(text) && lqueryLabel(text[pos]) {
				if pos&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
				pos++
			}
			end := pos
			var flags byte
			for pos < len(text) {
				if pos&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
				switch text[pos] { case '%': flags |= 1; case '@': flags |= 2; case '*': flags |= 4; default: goto modifiersDone }
				pos++
			}
		modifiersDone:
			if flags != 0 && pos < len(text) && lqueryLabel(text[pos]) { return Value{}, ltxtqueryError("42601", "modifiers syntax error") }
			if end-start > 65535 { return Value{}, ltxtqueryError("22023", "word is too long") }
			if storage > 65535 { return Value{}, ltxtqueryError("22023", "value is too big") }
			if end-start > 255 { return Value{}, ltxtqueryError("22023", "operand is too long") }
			storage += end-start+1
			nodes = append(nodes, ltxtqueryNode{start: start, end: end, flags: flags, count: 1})
			values = append(values, len(nodes)-1)
			if err := flush(false); err != nil { return Value{}, err }
			operand = false
			continue
		}
		for pos < len(text) && text[pos] == ' ' {
			if pos&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }; pos++
		}
		if pos == len(text) {
			if len(frames) != 1 { return Value{}, ltxtqueryError("42601", "syntax error") }
			if err := flush(true); err != nil { return Value{}, err }; break
		}
		op := text[pos]; pos++
		if op == ')' {
			if len(frames) == 1 { return Value{}, ltxtqueryError("42601", "syntax error") }
			if err := flush(true); err != nil { return Value{}, err }
			frames = frames[:len(frames)-1]
			if err := flush(false); err != nil { return Value{}, err }
			continue
		}
		if op != '&' && op != '|' { return Value{}, ltxtqueryError("42601", "syntax error") }
		f := &frames[len(frames)-1]
		// Released makepol emits an incoming OR immediately if another OR
		// is pending; reducing that pending OR first changes associativity.
		if f.count > 0 && op == '|' {
			if err := reduce(op); err != nil { return Value{}, err }
		} else {
			if f.count == len(f.operators) { return Value{}, ltxtqueryError("XX000", "stack too short") }
			f.operators[f.count] = op; f.count++
		}
		operand = true
	}
	if len(nodes) > ((1<<30)-1-8-storage)/12 { return Value{}, ltxtqueryError("54000", "ltxtquery is too large") }
	for i, n := range nodes {
		if i&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
		if n.op != 0 && n.op != '!' && 1+nodes[n.right].count > 32767 { return Value{}, ltxtqueryError("54000", "ltxtquery is too large") }
	}
	// Explicit traversal events avoid recursive stack growth and repeated
	// concatenation of entire right subtrees in the released printer.
	type event struct { node int; first bool; text string }
	events := []event{{node: values[0], first: true}}
	var out strings.Builder
	for len(events) > 0 {
		if err := ctx.Err(); err != nil { return Value{}, err }
		e := events[len(events)-1]; events = events[:len(events)-1]
		if e.text != "" { out.WriteString(e.text); continue }
		n := nodes[e.node]
		if n.op == 0 {
			start := out.Len()
			out.WriteString(text[n.start:n.end])
			nodes[e.node].start, nodes[e.node].end = start, out.Len()
			if n.flags&1 != 0 { out.WriteByte('%') }; if n.flags&2 != 0 { out.WriteByte('@') }; if n.flags&4 != 0 { out.WriteByte('*') }
		} else if n.op == '!' {
			out.WriteByte('!')
			group := nodes[n.right].op != 0
			if group { out.WriteString("( "); events = append(events, event{text: " )"}) }
			events = append(events, event{node: n.right, first: group})
		} else {
			if n.op == '|' && !e.first { out.WriteString("( "); events = append(events, event{text: " )"}) }
			operator := " & "; if n.op == '|' { operator = " | " }
			events = append(events, event{node: n.right}, event{text: operator}, event{node: n.left})
		}
	}
	return Value{typ: d, ltxtquery: &ltxtqueryPayload{text: out.String(), nodes: nodes}}, nil
}

func (v Value) validLtxtquery() bool { return v.ltxtquery != nil && v.ltxtquery.text != "" && len(v.ltxtquery.nodes) != 0 }
func (d *Descriptor) ltxtqueryOutput(ctx context.Context, v Value) (string, error) {
	if err := ctx.Err(); err != nil { return "", err }
	if v.typ != d || v.null || !v.validLtxtquery() { return "", fmt.Errorf("invalid ltxtquery payload") }
	return v.ltxtquery.text, nil
}

// RemapLtxtquery is reserved for a source-authorized catalog import.
func (v Value) RemapLtxtquery(target *Descriptor) (Value, error) {
	if !v.typ.IsLtxtquery() || !target.IsLtxtquery() || !v.null && !v.validLtxtquery() { return Value{}, fmt.Errorf("incompatible imported ltxtquery") }
	v.typ = target
	return v, nil
}
