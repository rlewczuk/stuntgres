package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/sqlstate"
)

type jsonpathExecKind uint8

const (
	jsonpathComplete jsonpathExecKind = iota
	jsonpathConsumerStop
	jsonpathSuppressibleStop
)

type jsonpathExecStatus struct {
	kind       jsonpathExecKind
	diagnostic error
}
type jsonpathStructuralError struct{ diagnostic error }

func (e *jsonpathStructuralError) Error() string { return e.diagnostic.Error() }
func jsonpathStructure(code, message string) error {
	return &jsonpathStructuralError{failure(code, message)}
}

// A path item borrows either an immutable JSON subtree or an invocation-owned
// temporal datum. Temporal items never become persistent JSONB node kinds.
type jsonpathItem struct {
	*jsonbNode
	temporal          *Value
	offsetSecondsWest int32
	hasOffset         bool
}

func (v jsonpathItem) itemKind() jsonbKind {
	if v.temporal != nil {
		return jsonbKind(255)
	}
	return v.kind
}

type jsonpathBinding struct {
	name string
	item jsonpathItem
}

type jsonpathScope struct {
	current       jsonpathItem
	origin        uint64
	currentOrigin uint64
	last          int
	ignore        bool
}
type jsonpathExecutor struct {
	ctx        context.Context
	root, vars *jsonbNode
	strict     bool
	regex      map[*jsonpathNode]*PGRegexProgram
	identities map[jsonpathObject]int64
	nextID     int64
	nextOrigin uint64
	bindings   []jsonpathBinding
	input      InputEnvironment
	notice     func(sqlstate.Error)
	timezone   bool
}

// JSONPathEnvironment belongs to one execution, never a held plan or AST.
type JSONPathEnvironment struct {
	Input    InputEnvironment
	Notice   func(sqlstate.Error)
	Timezone bool // Provider policy, copied into this invocation's executor.
}

// The executor owns neither its inputs nor a result collection. Consumer stop
// and structural stop are distinct, and neither discards previously emitted items.
func executeJSONPath(ctx context.Context, document, path, variables Value, env JSONPathEnvironment, emit func(jsonpathItem) (bool, error)) (jsonpathExecStatus, error) {
	if document.jsonb == nil || path.jsonpath == nil || path.jsonpath.root == nil || variables.jsonb == nil {
		return jsonpathExecStatus{}, fmt.Errorf("missing JSONPath executor payload")
	}
	if err := ctx.Err(); err != nil {
		return jsonpathExecStatus{}, err
	}
	if variables.jsonb.root.kind != jsonbObject {
		return jsonpathExecStatus{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `"vars" argument is not an object`, Detail: `Jsonpath parameters should be encoded as key-value pairs of "vars" object.`}
	}
	e := jsonpathExecutor{ctx: ctx, root: &document.jsonb.root, vars: &variables.jsonb.root, strict: path.jsonpath.strict, input: env.Input, notice: env.Notice, timezone: env.Timezone}
	return e.execute(path, emit)
}

func (e *jsonpathExecutor) execute(path Value, emit func(jsonpathItem) (bool, error)) (jsonpathExecStatus, error) {
	// Reserve separate bases for the document, vars object and typed bindings.
	e.nextOrigin = uint64(len(e.bindings)) + 2
	root := jsonpathItem{jsonbNode: e.root}
	stopped, err := e.walk(path.jsonpath.root, root, jsonpathScope{current: root, last: -1}, emit)
	if s, ok := err.(*jsonpathStructuralError); ok {
		return jsonpathExecStatus{jsonpathSuppressibleStop, s.diagnostic}, nil
	}
	if err != nil {
		return jsonpathExecStatus{}, err
	}
	if stopped {
		return jsonpathExecStatus{kind: jsonpathConsumerStop}, nil
	}
	return jsonpathExecStatus{kind: jsonpathComplete}, nil
}

type jsonpathWork struct {
	path        *jsonpathNode
	node        jsonpathItem
	scope       jsonpathScope
	unwrap      bool
	recursive   bool
	depth       int32
	rangeStep   bool
	arg         int
	cursor, end int64
	size        int
	children    bool
	child       int
	keyvalue    bool
	identity    int64
}

func (e *jsonpathExecutor) walk(path *jsonpathNode, input jsonpathItem, scope jsonpathScope, emit func(jsonpathItem) (bool, error)) (bool, error) {
	var local [32]jsonpathWork
	stack := append(local[:0], jsonpathWork{path: path, node: input, scope: scope, unwrap: true})
	for len(stack) > 0 {
		if err := e.ctx.Err(); err != nil {
			return false, err
		}
		w := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n, v, s := w.path, w.node, w.scope
		// Iterator frames bound scratch by nesting depth rather than collection width.
		if w.keyvalue {
			if w.child < len(v.fields) {
				field := &v.fields[w.child]
				w.child++
				stack = append(stack, w)
				pair := &jsonbNode{kind: jsonbObject, fields: []jsonbEntry{
					{key: "id", value: jsonpathInteger(w.identity)},
					{key: "key", value: jsonbNode{kind: jsonbString, text: field.key}, ordinal: 1},
					{key: "value", value: field.value, ordinal: 2},
				}}
				// Each generated wrapper establishes its own base, including the
				// borrowed value subtree. Never mutate source nodes or shared plans.
				s.origin = e.nextOrigin
				e.nextOrigin++
				stack = append(stack, jsonpathWork{path: n.next, node: jsonpathItem{jsonbNode: pair}, scope: s, unwrap: true})
			}
			continue
		}
		if w.children {
			var child *jsonbNode
			if v.itemKind() == jsonbObject {
				if w.child < len(v.fields) {
					child = &v.fields[w.child].value
				}
			} else if v.itemKind() == jsonbArray && w.child < len(v.items) {
				child = &v.items[w.child]
			}
			if child != nil {
				w.child++
				stack = append(stack, w)
				stack = append(stack, jsonpathWork{path: n, node: jsonpathItem{jsonbNode: child}, scope: s, unwrap: w.unwrap, recursive: w.recursive, depth: w.depth})
			}
			continue
		}
		if n == nil {
			more, err := emit(v)
			if err != nil || !more {
				return !more, err
			}
			continue
		}
		push := func(p *jsonpathNode, node jsonpathItem, sc jsonpathScope) {
			stack = append(stack, jsonpathWork{path: p, node: node, scope: sc, unwrap: true})
		}
		if w.rangeStep {
			if w.cursor <= w.end {
				i := w.cursor
				w.cursor++
				stack = append(stack, w)
				item := v
				if v.itemKind() == jsonbArray {
					item = jsonpathItem{jsonbNode: &v.items[int(i)]}
				}
				push(n.next, item, s)
				continue
			}
			if w.arg >= len(n.args) {
				continue
			}
			arg := n.args[w.arg]
			w.arg++
			bounds := s
			bounds.last = w.size - 1
			from, err := e.bound(arg.from, v, bounds)
			if err != nil {
				return false, err
			}
			to := from
			if arg.to != nil {
				to, err = e.bound(arg.to, v, bounds)
				if err != nil {
					return false, err
				}
			}
			if e.strict && !s.ignore && (from < 0 || to < from || to >= int64(w.size)) {
				return false, jsonpathStructure("22033", "jsonpath array subscript is out of bounds")
			}
			w.cursor = max(from, 0)
			w.end = min(to, int64(w.size)-1)
			stack = append(stack, w)
			continue
		}
		if w.recursive {
			leaf := v.itemKind() != jsonbArray && v.itemKind() != jsonbObject
			if (n.last < 0 || w.depth < n.last) && !leaf {
				stack = append(stack, jsonpathWork{path: n, node: v, scope: s, children: true, recursive: true, depth: w.depth + 1})
			}
			// Two LAST bounds select descendant leaves, not a scalar starting item.
			// Explicit ranges beginning at zero still include that starting item.
			if (n.first < 0 && n.last < 0 && leaf && w.depth > 0 || n.first >= 0 && w.depth >= n.first) && (n.last < 0 || w.depth <= n.last) {
				s.ignore = true
				push(n.next, v, s)
			}
			continue
		}
		// Only the first level is automatically unwrapped by each relevant operation.
		if !e.strict && w.unwrap && v.itemKind() == jsonbArray && (n.kind == "key" || n.kind == "anykey" || n.kind == "filter" || n.kind == "method" && (n.text == "boolean" || n.text == "string" || n.text == "keyvalue" || n.text == "integer" || n.text == "bigint" || n.text == "decimal" || n.text == "number" || n.text == "double" || n.text == "abs" || n.text == "floor" || n.text == "ceiling" || jsonpathDatetimeMethod(n.text))) {
			stack = append(stack, jsonpathWork{path: n, node: v, scope: s, children: true})
			continue
		}
		var structural error
		switch n.kind {
		case "$":
			s.origin = 0
			push(n.next, jsonpathItem{jsonbNode: e.root}, s)
		case "@":
			s.origin = s.currentOrigin
			push(n.next, s.current, s)
		case "variable":
			var item jsonpathItem
			s.origin = 1
			for i, binding := range e.bindings {
				if binding.name == n.text {
					item = binding.item
					s.origin = uint64(i) + 2
					break
				}
			}
			if item.jsonbNode == nil && item.temporal == nil && e.vars != nil {
				node, err := jsonbExtractField(e.ctx, e.vars, n.text)
				if err != nil {
					return false, err
				}
				item.jsonbNode = node
			}
			if item.jsonbNode == nil && item.temporal == nil {
				return false, failure("42704", fmt.Sprintf("could not find jsonpath variable \"%s\"", n.text))
			}
			push(n.next, item, s)
		case "number":
			x := &jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: n.number.text, exponent: n.number.exponent, scale: n.number.scale, negative: n.number.negative}}
			push(n.next, jsonpathItem{jsonbNode: x}, s)
		case "last":
			// As in exactOperand, construct decimal digits directly from the integer;
			// LAST never needs SQL numeric input parsing or a temporary descriptor.
			value := Value{text: strconv.Itoa(s.last), negative: s.last < 0}
			if value.negative {
				value.text = value.text[1:]
			}
			value = normalizeNumeric(value)
			push(n.next, jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: value.text, exponent: value.exponent, scale: value.scale, negative: value.negative}}}, s)
		case "string":
			push(n.next, jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbString, text: n.text}}, s)
		case "true", "false":
			push(n.next, jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbBoolean, boolean: n.kind == "true"}}, s)
		case "null":
			push(n.next, jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbNull}}, s)
		case "key", "anykey":
			if v.itemKind() != jsonbObject {
				if n.kind == "key" {
					structural = jsonpathStructure("2203A", "jsonpath member accessor can only be applied to an object")
				} else {
					structural = jsonpathStructure("2203C", "jsonpath wildcard member accessor can only be applied to an object")
				}
				break
			}
			if n.kind == "anykey" {
				stack = append(stack, jsonpathWork{path: n.next, node: v, scope: s, children: true, unwrap: true})
			} else {
				node, err := jsonbExtractField(e.ctx, v.jsonbNode, n.text)
				if err != nil {
					return false, err
				}
				if node == nil {
					structural = jsonpathStructure("2203A", fmt.Sprintf("JSON object does not contain key \"%s\"", n.text))
				} else {
					push(n.next, jsonpathItem{jsonbNode: node}, s)
				}
			}
		case "anyarray", "index":
			size := 1
			if v.itemKind() == jsonbArray {
				size = len(v.items)
			}
			if v.itemKind() != jsonbArray {
				if e.strict {
					message := "jsonpath array accessor can only be applied to an array"
					if n.kind == "anyarray" {
						message = "jsonpath wildcard array accessor can only be applied to an array"
					}
					structural = jsonpathStructure("22039", message)
					break
				}
				size = 1
			}
			if n.kind == "anyarray" {
				if v.itemKind() == jsonbArray {
					stack = append(stack, jsonpathWork{path: n.next, node: v, scope: s, children: true, unwrap: true})
				} else {
					push(n.next, v, s)
				}
				break
			}
			stack = append(stack, jsonpathWork{path: n, node: v, scope: s, rangeStep: true, cursor: 1, end: 0, size: size})
		case "recursive":
			stack = append(stack, jsonpathWork{path: n, node: v, scope: s, recursive: true})
		case "filter":
			candidate := s
			candidate.current = v
			candidate.currentOrigin = s.origin
			truth, err := e.condition(n.left, candidate)
			if err != nil {
				return false, err
			}
			if truth == jsonpathTrue {
				push(n.next, v, s)
			}
		case "method":
			if jsonpathDatetimeMethod(n.text) {
				result, err := e.datetimeMethod(n, v)
				if err != nil {
					return false, err
				}
				push(n.next, result, s)
				break
			}
			if v.temporal != nil {
				switch n.text {
				case "type":
					label := ""
					switch v.temporal.typ.id {
					case Date:
						label = "date"
					case Time:
						label = "time without time zone"
					case Timetz:
						label = "time with time zone"
					case Timestamp:
						label = "timestamp without time zone"
					case Timestamptz:
						label = "timestamp with time zone"
					}
					push(n.next, jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbString, text: label}}, s)
					continue
				case "string":
					push(n.next, jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbString, text: jsonpathTemporalText(v)}}, s)
					continue
				}
				// Retain method-specific wrong-kind errors without dereferencing
				// the absent JSON node of a typed item.
				v = jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbKind(255)}}
			}
			if n.text == "keyvalue" {
				if v.itemKind() != jsonbObject {
					return false, jsonpathStructure("2203C", "jsonpath item method .keyvalue() can only be applied to an object")
				}
				if len(v.fields) != 0 {
					stack = append(stack, jsonpathWork{path: n, node: v, scope: s, keyvalue: true, identity: e.objectID(v.jsonbNode, s.origin)})
				}
				break
			}
			if n.text == "size" && v.itemKind() != jsonbArray && e.strict {
				structural = jsonpathStructure("22039", "jsonpath item method .size() can only be applied to an array")
				break
			}
			result, err := e.scalarMethod(n, v.jsonbNode)
			if err != nil {
				return false, err
			}
			push(n.next, jsonpathItem{jsonbNode: result}, s)
		case "+", "-", "*", "/", "%", "unary+", "unary-":
			if stopped, err := e.arithmetic(n, v, s, emit); stopped || err != nil {
				return stopped, err
			}
		case "||", "&&", "==", "!=", "<", ">", "<=", ">=", "starts with", "like_regex", "!", "is unknown", "exists":
			truth, err := e.condition(n, s)
			if err != nil {
				return false, err
			}
			node := &jsonbNode{kind: jsonbNull}
			if truth != jsonpathUnknown {
				node.kind = jsonbBoolean
				node.boolean = truth == jsonpathTrue
			}
			push(n.next, jsonpathItem{jsonbNode: node}, s)
		default:
			return false, failure("0A000", "stuntgres: JSONPath scalar methods are unsupported")
		}
		if structural != nil && e.strict && !s.ignore {
			return false, structural
		}
	}
	return false, e.ctx.Err()
}

func (e *jsonpathExecutor) bound(n *jsonpathNode, v jsonpathItem, s jsonpathScope) (int64, error) {
	var found jsonpathItem
	count := 0
	// Cardinality is checked only after complete operand evaluation: even after
	// two items, later demanded errors take precedence over the cardinality error.
	_, err := e.walk(n, v, s, func(x jsonpathItem) (bool, error) {
		if count == 0 {
			found = x
		}
		if count < 2 {
			count++
		}
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	if count != 1 || found.itemKind() != jsonbNumber {
		return 0, jsonpathStructure("22033", "jsonpath array subscript is not a single numeric value")
	}
	d := found.number
	width := int64(len(d.digits)) + d.exponent
	if width > 10 {
		return 0, jsonpathStructure("22033", "jsonpath array subscript is out of integer range")
	}
	var value int64
	for i := range max(width, 0) {
		value *= 10
		if i < int64(len(d.digits)) {
			value += int64(d.digits[i] - '0')
		}
	}
	if d.negative {
		value = -value
	}
	if value < -2147483648 || value > 2147483647 {
		return 0, jsonpathStructure("22033", "jsonpath array subscript is out of integer range")
	}
	return value, e.ctx.Err()
}
