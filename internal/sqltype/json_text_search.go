package sqltype

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/workadmission"
)

// JSONTextSearchFlags selects semantic scalar categories, not containers or nulls.
type JSONTextSearchFlags uint8

const (
	JSONTextSearchString JSONTextSearchFlags = 1 << iota
	JSONTextSearchNumeric
	JSONTextSearchBoolean
	JSONTextSearchKey
	JSONTextSearchAll = JSONTextSearchString | JSONTextSearchNumeric | JSONTextSearchBoolean | JSONTextSearchKey
)

func ParseJSONTextSearchFlags(ctx context.Context, filter Value) (JSONTextSearchFlags, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if filter.typ == nil || filter.typ.BaseType().id != JSONB || filter.null || filter.jsonb == nil {
		return 0, fmt.Errorf("invalid JSONB text search filter")
	}
	root := &filter.jsonb.root
	if root.kind == jsonbObject {
		return 0, failure("22023", "wrong flag type, only arrays and scalars are allowed")
	}
	var flags JSONTextSearchFlags
	add := func(n *jsonbNode) error {
		if err := workadmission.Admit(ctx, workadmission.WorkTSVectorConvert); err != nil {
			return err
		}
		if n.kind == jsonbArray || n.kind == jsonbObject {
			return failure("XX000", "unexpected end of flag array")
		}
		hint := `Possible values are: "string", "numeric", "boolean", "key", and "all".`
		if n.kind != jsonbString {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "flag array element is not a string", Hint: hint}
		}
		// Keywords are ASCII; EqualFold alone would also accept Unicode equivalents.
		keyword := n.text
		ascii := len(keyword) <= 7
		if ascii {
			for i := range len(keyword) {
				if keyword[i] >= 128 {
					ascii = false
					break
				}
			}
		}
		if ascii {
			switch {
			case strings.EqualFold(keyword, "string"):
				flags |= JSONTextSearchString
			case strings.EqualFold(keyword, "numeric"):
				flags |= JSONTextSearchNumeric
			case strings.EqualFold(keyword, "boolean"):
				flags |= JSONTextSearchBoolean
			case strings.EqualFold(keyword, "key"):
				flags |= JSONTextSearchKey
			case strings.EqualFold(keyword, "all"):
				flags |= JSONTextSearchAll
			default:
				ascii = false
			}
			if ascii {
				return nil
			}
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("wrong flag in flag array: \"%s\"", keyword), Hint: hint}
	}
	if root.kind == jsonbArray {
		for i := range root.items {
			if err := add(&root.items[i]); err != nil {
				return 0, err
			}
		}
	} else if err := add(root); err != nil {
		return 0, err
	}
	return flags, ctx.Err()
}

// WalkJSONTextSearch borrows immutable input and invokes visit synchronously.
// Text JSON retains source order/duplicates; JSONB retains its own field order.
func WalkJSONTextSearch(ctx context.Context, document Value, flags JSONTextSearchFlags, visit func(string) error) error {
	if document.typ == nil || document.null || visit == nil || flags & ^JSONTextSearchAll != 0 {
		return fmt.Errorf("invalid JSON text search traversal")
	}
	switch document.typ.BaseType().id {
	case JSON:
		if document.text == "" {
			return fmt.Errorf("invalid retained JSON document")
		}
		return walkJSONTextSearch(ctx, document.text, flags, visit)
	case JSONB:
		if document.jsonb == nil {
			return fmt.Errorf("invalid JSONB text search document")
		}
		return walkJSONBTextSearch(ctx, &document.jsonb.root, flags, visit)
	default:
		return fmt.Errorf("invalid JSON text search document")
	}
}

func walkJSONTextSearch(ctx context.Context, source string, flags JSONTextSearchFlags, visit func(string) error) error {
	l := jsonLexer{ctx: ctx, text: source, line: 1, decode: true, workClass: workadmission.WorkTSVectorConvert}
	type frame struct {
		kind byte
		key  bool
	}
	var local [32]frame
	stack := local[:0]
	var scalar, key string
	scalarPending, keyPending := false, false
	for {
		if err := workadmission.Admit(ctx, workadmission.WorkTSVectorConvert); err != nil {
			return err
		}
		if err := l.next(); err != nil {
			return err
		}
		// Scalar callbacks follow one lexical lookahead. Field callbacks follow the
		// first value token (the colon itself is not that lookahead).
		if scalarPending {
			if err := visit(scalar); err != nil {
				return err
			}
			scalarPending = false
		}
		if keyPending && l.kind != ':' {
			if err := visit(key); err != nil {
				return err
			}
			keyPending = false
		}
		switch l.kind {
		case 0:
			if len(stack) != 0 {
				return fmt.Errorf("incomplete retained JSON")
			}
			return ctx.Err()
		case ',', ':':
			continue
		case '}', ']':
			if len(stack) == 0 {
				return fmt.Errorf("invalid retained JSON end")
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if len(stack) > 0 {
			p := &stack[len(stack)-1]
			if p.kind == '{' && !p.key {
				if l.kind != 's' {
					return fmt.Errorf("invalid retained JSON key")
				}
				p.key = true
				if flags&JSONTextSearchKey != 0 {
					key, keyPending = l.decoded, true
				}
				continue
			}
			p.key = false
		}
		switch l.kind {
		case '{', '[':
			stack = append(stack, frame{kind: l.kind})
		case 's':
			if flags&JSONTextSearchString != 0 {
				scalar, scalarPending = l.decoded, true
			}
		case 'n':
			if flags&JSONTextSearchNumeric != 0 {
				scalar, scalarPending = source[l.start:l.end], true
			}
		case 't', 'f':
			if flags&JSONTextSearchBoolean != 0 {
				scalar, scalarPending = source[l.start:l.end], true
			}
		case 'z':
		default:
			return fmt.Errorf("invalid retained JSON token")
		}
	}
}

func walkJSONBTextSearch(ctx context.Context, root *jsonbNode, flags JSONTextSearchFlags, visit func(string) error) error {
	var local [32]jsonbWalkFrame
	stack := local[:0]
	n := root
	for {
		if err := workadmission.Admit(ctx, workadmission.WorkTSVectorConvert); err != nil {
			return err
		}
		var text string
		selected := false
		switch n.kind {
		case jsonbString:
			text, selected = n.text, flags&JSONTextSearchString != 0
		case jsonbNumber:
			if flags&JSONTextSearchNumeric != 0 {
				var b strings.Builder
				if err := appendNumericOutput(ctx, &b, n.number.value()); err != nil {
					return err
				}
				text, selected = b.String(), true
			}
		case jsonbBoolean:
			text, selected = "false", flags&JSONTextSearchBoolean != 0
			if n.boolean {
				text = "true"
			}
		case jsonbArray, jsonbObject:
			stack = append(stack, jsonbWalkFrame{node: n})
		case jsonbNull:
		default:
			return fmt.Errorf("invalid JSONB node")
		}
		if selected {
			if err := visit(text); err != nil {
				return err
			}
		}
		for {
			if len(stack) == 0 {
				return ctx.Err()
			}
			if err := workadmission.Admit(ctx, workadmission.WorkTSVectorConvert); err != nil {
				return err
			}
			f := &stack[len(stack)-1]
			i := f.index
			if f.node.kind == jsonbArray && i < len(f.node.items) {
				f.index++
				n = &f.node.items[i]
				break
			}
			if f.node.kind == jsonbObject && i < len(f.node.fields) {
				if flags&JSONTextSearchKey != 0 {
					if err := visit(f.node.fields[i].key); err != nil {
						return err
					}
				}
				f.index++
				n = &f.node.fields[i].value
				break
			}
			stack = stack[:len(stack)-1]
		}
	}
}
