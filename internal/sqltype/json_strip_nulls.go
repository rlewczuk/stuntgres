package sqltype

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

// JSONStripNullsPlan retains occurrence-owned descriptor authority.
type JSONStripNullsPlan struct{ result, target, arrays Usage }

func BindJSONStripNulls(result, target, arrays Usage) (JSONStripNullsPlan, error) {
	p := JSONStripNullsPlan{result: result, target: target, arrays: arrays}
	if !result.Valid() || !target.Valid() || !arrays.Valid() || result.Typmod != -1 || target.Typmod != -1 || arrays.Typmod != -1 || result.Type.ID() != JSON || target.Type != result.Type || arrays.Type.ID() != Bool {
		return p, fmt.Errorf("invalid JSON stripping signature")
	}
	return p, nil
}

func (p JSONStripNullsPlan) ResultUsage() Usage { return p.result }

func (p JSONStripNullsPlan) Evaluate(ctx context.Context, target, arrays Value) (Value, error) {
	if p.result.Type == nil || target.typ != p.target.Type || arrays.typ != p.arrays.Type {
		return Value{}, fmt.Errorf("foreign JSON stripping value")
	}
	if err := util.ExecutionCheckpoint(ctx, "json.strip_nulls"); err != nil {
		return Value{}, err
	}
	if target.null || arrays.null {
		return Null(p.result.Type), nil
	}
	text, err := jsonStripNulls(ctx, target.text, arrays.boolean)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: text}, nil
}

type jsonStripFrame struct {
	kind       byte
	emitted    bool
	keyPending bool
	key        string
}

// Input storage is already grammar-validated. Decode every string during
// processing, including keys whose null-valued member will be removed.
func jsonStripNulls(ctx context.Context, source string, arrays bool) (string, error) {
	l := jsonLexer{ctx: ctx, text: source, line: 1, decode: true, workClass: workadmission.WorkJSONStripNulls}
	var out strings.Builder
	var local [32]jsonStripFrame
	stack := local[:0]
	for {
		if err := workadmission.Admit(ctx, workadmission.WorkJSONStripNulls); err != nil {
			return "", err
		}
		if err := l.next(); err != nil {
			return "", err
		}
		switch l.kind {
		case 0:
			if len(stack) != 0 {
				return "", fmt.Errorf("incomplete retained JSON")
			}
			return out.String(), nil
		case ',', ':':
			continue
		case '}', ']':
			if len(stack) == 0 {
				return "", fmt.Errorf("invalid retained JSON end")
			}
			stack = stack[:len(stack)-1]
			out.WriteByte(l.kind)
			continue
		}
		var parent *jsonStripFrame
		if len(stack) > 0 {
			parent = &stack[len(stack)-1]
			if parent.kind == '{' && !parent.keyPending {
				if l.kind != 's' {
					return "", fmt.Errorf("invalid retained JSON key")
				}
				parent.key, parent.keyPending = l.decoded, true
				continue
			}
			if l.kind == 'z' && (parent.kind == '{' || arrays) {
				parent.key, parent.keyPending = "", false
				continue
			}
			if parent.emitted {
				out.WriteByte(',')
			}
			parent.emitted = true
			if parent.kind == '{' {
				if err := writeJSONBString(ctx, &out, parent.key); err != nil {
					return "", err
				}
				out.WriteByte(':')
				parent.key, parent.keyPending = "", false
			}
		}
		switch l.kind {
		case '{', '[':
			out.WriteByte(l.kind)
			stack = append(stack, jsonStripFrame{kind: l.kind})
		case 's':
			if err := writeJSONBString(ctx, &out, l.decoded); err != nil {
				return "", err
			}
		case 'n', 't', 'f', 'z':
			for start := l.start; start < l.end; {
				if err := workadmission.Admit(ctx, workadmission.WorkJSONStripNulls); err != nil {
					return "", err
				}
				end := min(start+4096, l.end)
				out.WriteString(source[start:end])
				start = end
			}
		default:
			return "", fmt.Errorf("invalid retained JSON token")
		}
	}
}
