package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
	"unsafe"
)

type RegexpKind uint8

const (
	RegexpCount RegexpKind = iota + 1
	RegexpInstr
	RegexpLike
	RegexpMatch
	RegexpMatches
	RegexpReplace
	RegexpSplitArray
	RegexpSplitTable
	RegexpSubstr
)

func RegexpFunctionKind(name string) RegexpKind {
	switch name {
	case "regexp_count":
		return RegexpCount
	case "regexp_instr":
		return RegexpInstr
	case "regexp_substr":
		return RegexpSubstr
	case "regexp_like":
		return RegexpLike
	case "regexp_match":
		return RegexpMatch
	case "regexp_matches":
		return RegexpMatches
	case "regexp_replace":
		return RegexpReplace
	case "regexp_split_to_array":
		return RegexpSplitArray
	case "regexp_split_to_table":
		return RegexpSplitTable
	}
	return 0
}

type RegexpPlan struct {
	kind                                                      RegexpKind
	argCount                                                  uint8
	args                                                      [7]Usage
	result                                                    Usage
	collation                                                 CollationRef
	conflict                                                  bool
	citext                                                    bool
	replacement, start, occurrence, endOption, flags, subexpr int8
}

func (p RegexpPlan) Name() string {
	switch p.kind {
	case RegexpCount:
		return "regexp_count"
	case RegexpInstr:
		return "regexp_instr"
	case RegexpSubstr:
		return "regexp_substr"
	case RegexpLike:
		return "regexp_like"
	case RegexpMatch:
		return "regexp_match"
	case RegexpMatches:
		return "regexp_matches"
	case RegexpReplace:
		return "regexp_replace"
	case RegexpSplitArray:
		return "regexp_split_to_array"
	case RegexpSplitTable:
		return "regexp_split_to_table"
	}
	return ""
}
func (p RegexpPlan) ArgCount() int           { return int(p.argCount) }
func (p RegexpPlan) ArgumentUsage(i int) Usage { return p.args[i] }
func (p RegexpPlan) ReturnsSet() bool        { return p.kind == RegexpMatches || p.kind == RegexpSplitTable }
func (p RegexpPlan) ResultUsage() Usage      { return p.result }
func (p RegexpPlan) Collation() CollationRef { return p.collation }
func (p RegexpPlan) Conflict() bool          { return p.conflict }
func BindRegexpFunction(name string, args []Usage, result Usage, collation CollationRef, conflict bool) (RegexpPlan, error) {
	p := RegexpPlan{kind: RegexpFunctionKind(name), argCount: uint8(len(args)), result: result, collation: collation, conflict: conflict,
		replacement: -1, start: -1, occurrence: -1, endOption: -1, flags: -1, subexpr: -1}
	n := len(args)
	valid := n >= 2 && n <= 7 && result.Valid()
	switch p.kind {
	case RegexpCount:
		valid = valid && n <= 4
		if n >= 3 {
			p.start = 2
		}
		if n == 4 {
			p.flags = 3
		}
	case RegexpInstr:
		if n >= 3 {
			p.start = 2
		}
		if n >= 4 {
			p.occurrence = 3
		}
		if n >= 5 {
			p.endOption = 4
		}
		if n >= 6 {
			p.flags = 5
		}
		if n == 7 {
			p.subexpr = 6
		}
	case RegexpSubstr:
		valid = valid && n <= 6
		if n >= 3 {
			p.start = 2
		}
		if n >= 4 {
			p.occurrence = 3
		}
		if n >= 5 {
			p.flags = 4
		}
		if n == 6 {
			p.subexpr = 5
		}
	case RegexpLike, RegexpMatch, RegexpMatches, RegexpSplitArray, RegexpSplitTable:
		valid = valid && n <= 3
		if n == 3 {
			p.flags = 2
		}
	case RegexpReplace:
		valid = valid && n >= 3 && n <= 6
		p.replacement = 2
		if n == 4 && args[3].Type != nil && args[3].Type.ID() == Text {
			p.flags = 3
		} else if n >= 4 {
			p.start = 3
		}
		if n >= 5 {
			p.occurrence = 4
		}
		if n == 6 {
			p.flags = 5
		}
	default:
		valid = false
	}
	if !valid {
		return p, fmt.Errorf("invalid regexp signature")
	}
	for i, u := range args {
		id := Text
		if i == int(p.start) || i == int(p.occurrence) || i == int(p.endOption) || i == int(p.subexpr) {
			id = Int4
		}
		if !u.Valid() || u.Type.ID() != id {
			return p, fmt.Errorf("invalid regexp argument")
		}
		p.args[i] = u
	}
	id := Text
	switch p.kind {
	case RegexpCount, RegexpInstr:
		id = Int4
	case RegexpLike:
		id = Bool
	case RegexpMatch, RegexpMatches, RegexpSplitArray:
		id = 1009
	}
	if result.Type.ID() != id {
		return p, fmt.Errorf("invalid regexp result")
	}
	return p, nil
}
// BindCitextRegexpFunction preserves the installed formal descriptors while
// sharing the text kernel. Citext values already retain their original spelling.
func BindCitextRegexpFunction(name string, args []Usage, result, text Usage, collation CollationRef, conflict bool) (RegexpPlan, error) {
	if len(args) < 2 || len(args) > 4 || !args[0].Valid() || !args[1].Valid() || !args[0].Type.IsCitext() || args[1].Type != args[0].Type || !text.Valid() || text.Type.ID() != Text {
		return RegexpPlan{}, fmt.Errorf("invalid citext regexp signature")
	}
	kind := RegexpFunctionKind(name)
	if kind != RegexpMatch && kind != RegexpMatches && kind != RegexpReplace && kind != RegexpSplitArray && kind != RegexpSplitTable || kind != RegexpReplace && len(args) > 3 {
		return RegexpPlan{}, fmt.Errorf("invalid citext regexp member")
	}
	var adapted [4]Usage
	copy(adapted[:], args)
	adapted[0], adapted[1] = text, text
	p, err := BindRegexpFunction(name, adapted[:len(args)], result, collation, conflict)
	if err != nil {
		return p, err
	}
	copy(p.args[:], args)
	p.citext = true
	return p, nil
}
// Starting insensitive is equivalent to the citext SQL wrappers' appended i:
// any explicit c resets it, and subsequent i/c options retain written order.
func regexpFlags(ctx context.Context, text string, insensitive bool) (PGRegexOptions, bool, error) {
	o := PGRegexOptions{CaseInsensitive: insensitive}
	global := false
	for _, r := range text {
		if err := ctx.Err(); err != nil {
			return o, false, err
		}
		switch r {
		case 'b', 'e':
			o.Mode = pgRegexBRE
		case 'q':
			o.Mode = pgRegexQuote
		case 'c':
			o.CaseInsensitive = false
		case 'i':
			o.CaseInsensitive = true
		case 'g':
			global = true
		case 'x':
			o.Expanded = true
		case 't':
			o.Expanded = false
		case 'm', 'n':
			o.NewlineStop, o.NewlineAnchor = true, true
		case 'p':
			o.NewlineStop, o.NewlineAnchor = true, false
		case 'w':
			o.NewlineStop, o.NewlineAnchor = false, true
		case 's':
			o.NewlineStop, o.NewlineAnchor = false, false
		default:
			return o, false, failure("22023", fmt.Sprintf("invalid regular expression option: \"%c\"", r))
		}
	}
	return o, global, nil
}
func (p RegexpPlan) Evaluate(ctx context.Context, args [7]Value, state *PGRegexExecution) (Value, error) {
	if p.ReturnsSet() {
		return Value{}, fmt.Errorf("set regexp evaluated as scalar")
	}
	value, _, err := p.evaluate(ctx, args, state)
	return value, err
}
func (p RegexpPlan) EvaluateSet(ctx context.Context, args [7]Value, state *PGRegexExecution) ([]Value, error) {
	if !p.ReturnsSet() {
		return nil, fmt.Errorf("scalar regexp evaluated as set")
	}
	_, values, err := p.evaluate(ctx, args, state)
	return values, err
}

// Every invocation drains before returning. Only immutable output values escape;
// borrowed capture scratch and the single statement program remain private.
func (p RegexpPlan) evaluate(ctx context.Context, args [7]Value, state *PGRegexExecution) (value Value, values []Value, err error) {
	defer func() {
		if err != nil {
			state.Reset()
		}
	}()
	if err = ctx.Err(); err != nil {
		return
	}
	null := false
	for i := range p.ArgCount() {
		if args[i].typ != p.args[i].Type {
			err = fmt.Errorf("invalid regexp value")
			return
		}
		null = null || args[i].null
	}
	if null {
		state.Reset()
		value = Null(p.result.Type)
		return
	}
	if p.conflict {
		err = regexCollationConflict()
		return
	}
	start, n, endopt, subexpr := int64(1), int64(1), int64(0), int64(0)
	get := func(slot int8, def int64) int64 {
		if slot < 0 {
			return def
		}
		return args[slot].integer
	}
	start = get(p.start, start)
	n = get(p.occurrence, n)
	endopt = get(p.endOption, endopt)
	subexpr = get(p.subexpr, subexpr)
	invalid := func(name string, v int64) error {
		return failure("22023", fmt.Sprintf("invalid value for parameter \"%s\": %d", name, v))
	}
	if start <= 0 {
		err = invalid("start", start)
		return
	}
	if (p.kind == RegexpInstr || p.kind == RegexpSubstr) && n <= 0 || p.kind == RegexpReplace && n < 0 {
		err = invalid("n", n)
		return
	}
	if endopt != 0 && endopt != 1 {
		err = invalid("endoption", endopt)
		return
	}
	if subexpr < 0 {
		err = invalid("subexpr", subexpr)
		return
	}
	flagtext := ""
	if p.flags >= 0 {
		flagtext = args[p.flags].text
	}
	var options PGRegexOptions
	var global bool
	options, global, err = regexpFlags(ctx, flagtext, p.citext)
	if err != nil {
		if p.kind == RegexpReplace && p.flags == 3 && len(flagtext) > 0 && flagtext[0] >= '0' && flagtext[0] <= '9' {
			if e, ok := err.(*sqlstate.Error); ok {
				e.Hint = "If you meant to use regexp_replace() with a start parameter, cast the fourth argument to integer explicitly."
			}
		}
		return
	}
	if global && p.kind != RegexpMatches && p.kind != RegexpReplace {
		err = failure("22023", p.Name()+"() does not support the \"global\" option")
		if p.kind == RegexpMatch {
			err.(*sqlstate.Error).Hint = "Use the regexp_matches function instead."
		}
		return
	}
	var program *PGRegexProgram
	program, err = state.resolveProgram(ctx, args[1].text, options, p.collation)
	if err != nil {
		return
	}
	work := pgRegexWork{ctx: ctx}
	// Consumer allocations share the matcher allowance, including growth peaks.
	owned := 0
	reserve := func(size int) error {
		if e := program.budget.reserve(size); e != nil {
			return e
		}
		owned += size
		return nil
	}
	defer func() { program.budget.used -= owned; program.budget.work = nil }()
	program.budget.work = &work
	source := args[0].text
	pos := 0
	char := int64(1)
	for char < start && pos < len(source) {
		if err = work.step(); err != nil {
			return
		}
		_, width := utf8.DecodeRuneInString(source[pos:])
		pos += width
		char++
	}
	// Distinguish the terminal character position from positions beyond it.
	if char < start {
		pos = len(source) + 1
	}
	demand := PGRegexWhole
	if p.kind == RegexpLike {
		demand = PGRegexBoolean
	}
	if p.kind == RegexpMatch || p.kind == RegexpMatches || p.kind == RegexpReplace || (p.kind == RegexpInstr || p.kind == RegexpSubstr) && subexpr > 0 {
		demand = PGRegexCaptures
	}
	if p.kind == RegexpReplace && p.occurrence < 0 {
		if global {
			n = 0
		}
	}
	var fields []Value
	appendValue := func(dst *[]Value, v Value) error {
		program.budget.work = &work
		before := program.budget.used
		next, e := pgRegexGrow(&program.budget, *dst, len(*dst)+1)
		owned += program.budget.used - before
		if e != nil {
			return e
		}
		*dst = append(next, v)
		return nil
	}
	array := func(elements []Value) (Value, error) {
		if err := reserve(int(unsafe.Sizeof(arrayPayload{})) + int(unsafe.Sizeof(ArrayDimension{}))); err != nil {
			return Value{}, err
		}
		return OwnedArrayValue(p.result.Type, []ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
	}
	capture := func(m PGRegexMatch) (Value, error) {
		count := len(m.Captures)
		if count == 0 {
			count = 1
		}
		if e := reserve(count * int(unsafe.Sizeof(Value{}))); e != nil {
			return Value{}, e
		}
		cells := make([]Value, count)
		for i := range cells {
			if e := work.step(); e != nil {
				return Value{}, e
			}
			span := m.Whole
			if len(m.Captures) > 0 {
				span = m.Captures[i]
			}
			cells[i] = Null(p.result.Type.Element())
			if span.Start >= 0 {
				cells[i] = String(p.result.Type.Element(), source[span.Start:span.End])
			}
		}
		return array(cells)
	}
	var output []byte
	appendText := func(s string) error {
		program.budget.work = &work
		before := program.budget.used
		next, e := pgRegexGrow(&program.budget, output, len(output)+len(s))
		owned += program.budget.used - before
		if e != nil {
			return e
		}
		output = next
		for at := 0; at < len(s); at += 4096 {
			if e = ctx.Err(); e != nil {
				return e
			}
			end := min(at+4096, len(s))
			output = append(output, s[at:end]...)
		}
		return nil
	}
	last, previousEnd := 0, 0
	seen := int64(0)
	for pos <= len(source) {
		if err = work.step(); err != nil {
			return
		}
		var m PGRegexMatch
		m, err = program.MatchFrom(ctx, source, pos, demand)
		program.budget.work = &work
		if err != nil {
			return
		}
		if !m.Matched {
			break
		}
		seen++
		switch p.kind {
		case RegexpLike:
			value = Boolean(p.result.Type, true)
			return
		case RegexpCount:
		case RegexpInstr, RegexpSubstr:
			if seen == n {
				span := m.Whole
				if subexpr > 0 {
					if len(m.Captures) == 0 {
						if subexpr > 1 {
							span.Start = -1
						}
					} else if subexpr > int64(len(m.Captures)) {
						span.Start = -1
					} else {
						span = m.Captures[subexpr-1]
					}
				}
				if p.kind == RegexpSubstr {
					value = Null(p.result.Type)
					if span.Start >= 0 {
						value = String(p.result.Type, source[span.Start:span.End])
					}
					return
				}
				index := int64(0)
				if span.Start >= 0 {
					at := span.Start
					if endopt == 1 {
						at = span.End
					}
					index = 1
					for bytepos := 0; bytepos < at; {
						if err = work.step(); err != nil {
							return
						}
						_, width := utf8.DecodeRuneInString(source[bytepos:])
						bytepos += width
						index++
					}
				}
				value, err = Integer(p.result.Type, index)
				return
			}
		case RegexpMatch, RegexpMatches:
			var v Value
			v, err = capture(m)
			if err != nil {
				return
			}
			if p.kind == RegexpMatch {
				value = v
				return
			}
			err = appendValue(&values, v)
			if err != nil {
				return
			}
			if !global {
				return
			}
		case RegexpReplace:
			if n == 0 || seen == n {
				err = appendText(source[last:m.Whole.Start])
				if err != nil {
					return
				}
				replacement := args[p.replacement].text
				for at := 0; at < len(replacement); {
					if err = work.step(); err != nil {
						return
					}
					end := at
					for end < len(replacement) && replacement[end] != '\\' {
						end++
						if err = work.step(); err != nil {
							return
						}
					}
					if end > at {
						err = appendText(replacement[at:end])
						if err != nil {
							return
						}
						at = end
						continue
					}
					if at+1 == len(replacement) {
						err = appendText(replacement[at:])
						if err != nil {
							return
						}
						at++
						continue
					}
					c := replacement[at+1]
					span := PGRegexSpan{-1, -1}
					switch {
					case c == '&':
						span = m.Whole
					case c >= '1' && c <= '9':
						if int(c-'1') < len(m.Captures) {
							span = m.Captures[c-'1']
						}
					case c == '\\':
						err = appendText("\\")
					default:
						err = appendText(replacement[at : at+2])
					}
					if err != nil {
						return
					}
					if span.Start >= 0 {
						err = appendText(source[span.Start:span.End])
						if err != nil {
							return
						}
					}
					at += 2
				}
				last = m.Whole.End
				if n > 0 {
					pos = len(source) + 1
					continue
				}
			}
		case RegexpSplitArray, RegexpSplitTable:
			if m.Whole.Start < len(source) && m.Whole.End > previousEnd {
				element := p.result.Type
				if p.kind == RegexpSplitArray {
					element = element.Element()
				}
				err = appendValue(&fields, String(element, source[last:m.Whole.Start]))
				if err != nil {
					return
				}
				last = m.Whole.End
			}
			previousEnd = m.Whole.End
		}
		pos = m.Whole.End
		if m.Whole.Start == m.Whole.End {
			if pos == len(source) {
				break
			}
			_, width := utf8.DecodeRuneInString(source[pos:])
			pos += width
		}
	}
	switch p.kind {
	case RegexpCount:
		value, err = Integer(p.result.Type, seen)
	case RegexpInstr:
		value, err = Integer(p.result.Type, 0)
	case RegexpLike:
		value = Boolean(p.result.Type, false)
	case RegexpMatch, RegexpSubstr:
		value = Null(p.result.Type)
	case RegexpReplace:
		if last == 0 && len(output) == 0 {
			value = String(p.result.Type, source)
			return
		}
		err = appendText(source[last:])
		if err != nil {
			return
		}
		// output is invocation-owned and never mutated after publication.
		value = String(p.result.Type, unsafe.String(unsafe.SliceData(output), len(output)))
	case RegexpSplitArray, RegexpSplitTable:
		element := p.result.Type
		if p.kind == RegexpSplitArray {
			element = element.Element()
		}
		err = appendValue(&fields, String(element, source[last:]))
		if err != nil {
			return
		}
		if p.kind == RegexpSplitTable {
			values = fields
		} else {
			value, err = array(fields)
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	return
}
