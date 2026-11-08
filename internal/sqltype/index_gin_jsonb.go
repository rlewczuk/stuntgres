package sqltype

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
)

// ginJSONBProvider discriminates the two released jsonb GIN dictionaries. The
// tokens are an internal dictionary encoding; they are never SQL-visible.
type ginJSONBProvider uint8

const (
	ginJSONBNone ginJSONBProvider = iota
	ginJSONBOps
	ginJSONBPathOps
)

// jsonb_ops flags (REL_18 jsonb_gin.c). A token's lexeme is one flag byte plus
// the payload bytes; long payloads are replaced by eight lowercase hex digits.
const (
	jginFlagKey    byte = 0x01
	jginFlagNull   byte = 0x02
	jginFlagBool   byte = 0x03
	jginFlagNum    byte = 0x04
	jginFlagStr    byte = 0x05
	jginFlagHashed byte = 0x10
	jginMaxLength       = 125
)

const jginHexDigits = "0123456789abcdef"

// ginJSONBHash is the private deterministic 32-bit token hash (FNV-1a). The
// concrete function is an internal choice: tokens are only compared with
// themselves and collisions merely add recheck-positive candidates.
func ginJSONBHash(payload []byte) uint32 {
	hash := uint32(2166136261)
	for _, b := range payload {
		hash ^= uint32(b)
		hash *= 16777619
	}
	return hash
}

func ginJSONBRotate(hash uint32) uint32 {
	return hash<<1 | hash>>31
}

func jsonbTextToken(flag byte, text string) GinToken {
	payload := []byte(text)
	if len(payload) > jginMaxLength {
		var lexeme [9]byte
		lexeme[0] = flag | jginFlagHashed
		hash := ginJSONBHash(payload)
		for i := 8; i >= 1; i-- {
			lexeme[i] = jginHexDigits[hash&0xf]
			hash >>= 4
		}
		return GinToken{Lexeme: string(lexeme[:])}
	}
	lexeme := make([]byte, 1+len(payload))
	lexeme[0] = flag
	copy(lexeme[1:], payload)
	return GinToken{Lexeme: string(lexeme)}
}

func jsonbPayloadToken(flag byte, payload string) GinToken {
	return jsonbTextToken(flag, payload)
}

// jsonbNumberPayload reproduces numeric_normalize: render the exact numeric
// value, then strip trailing fractional zeros and the then-trailing point.
func jsonbNumberPayload(ctx context.Context, number jsonbDecimal) (string, error) {
	var b strings.Builder
	if err := appendNumericOutput(ctx, &b, number.value()); err != nil {
		return "", err
	}
	text := b.String()
	if strings.IndexByte(text, '.') >= 0 {
		text = strings.TrimRight(text, "0")
		text = strings.TrimSuffix(text, ".")
	}
	// The numeric provider has no negative zero; stay value-equality safe.
	if text == "" || text == "-0" {
		return "0", nil
	}
	return text, nil
}

func jsonbPathToken(hash uint32) GinToken {
	var lexeme [4]byte
	binary.BigEndian.PutUint32(lexeme[:], hash)
	return GinToken{Lexeme: string(lexeme[:])}
}

// ginJSONBItem is an explicit work item rather than recursion: a bounded walk
// step lets cancellation interrupt adversarial nesting.
type ginJSONBItem struct {
	node    *jsonbNode
	element bool
}

type ginJSONBPathItem struct {
	node *jsonbNode
	hash uint32
}

// walkJSONBOps emits one flag+payload key per object key and per scalar leaf.
// Object keys are indexed at every depth; array string elements are keys while
// object string values are ordinary STR values. Empty containers emit nothing.
func walkJSONBOps(ctx context.Context, root *jsonbNode, emit func(GinToken) error) error {
	if root == nil {
		return fmt.Errorf("invalid JSONB GIN payload")
	}
	stack := make([]ginJSONBItem, 0, 8)
	stack = append(stack, ginJSONBItem{node: root, element: true})
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := item.node
		switch node.kind {
		case jsonbObject:
			for i := range node.fields {
				if err := ctx.Err(); err != nil {
					return err
				}
				field := &node.fields[i]
				if err := emit(jsonbTextToken(jginFlagKey, field.key)); err != nil {
					return err
				}
				stack = append(stack, ginJSONBItem{node: &field.value})
			}
		case jsonbArray:
			for i := range node.items {
				stack = append(stack, ginJSONBItem{node: &node.items[i], element: true})
			}
		case jsonbString:
			flag := jginFlagStr
			if item.element {
				flag = jginFlagKey
			}
			if err := emit(jsonbTextToken(flag, node.text)); err != nil {
				return err
			}
		case jsonbNull:
			if err := emit(jsonbPayloadToken(jginFlagNull, "")); err != nil {
				return err
			}
		case jsonbBoolean:
			payload := "f"
			if node.boolean {
				payload = "t"
			}
			if err := emit(jsonbPayloadToken(jginFlagBool, payload)); err != nil {
				return err
			}
		case jsonbNumber:
			payload, err := jsonbNumberPayload(ctx, node.number)
			if err != nil {
				return err
			}
			if err := emit(jsonbPayloadToken(jginFlagNum, payload)); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// walkJSONBPathOps emits one 4-byte path hash per scalar leaf. Every object key
// on the path is mixed into the hash; arrays add nothing to it.
func walkJSONBPathOps(ctx context.Context, root *jsonbNode, emit func(GinToken) error) error {
	if root == nil {
		return fmt.Errorf("invalid JSONB GIN payload")
	}
	stack := make([]ginJSONBPathItem, 0, 8)
	stack = append(stack, ginJSONBPathItem{node: root})
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := item.node
		switch node.kind {
		case jsonbObject:
			for i := range node.fields {
				if err := ctx.Err(); err != nil {
					return err
				}
				field := &node.fields[i]
				hash := ginJSONBRotate(item.hash) ^ ginJSONBHash([]byte(field.key))
				stack = append(stack, ginJSONBPathItem{node: &field.value, hash: hash})
			}
		case jsonbArray:
			for i := range node.items {
				stack = append(stack, ginJSONBPathItem{node: &node.items[i], hash: item.hash})
			}
		case jsonbNull:
			if err := emit(jsonbPathToken(ginJSONBRotate(item.hash) ^ 0x01)); err != nil {
				return err
			}
		case jsonbBoolean:
			tmp := uint32(0x04)
			if node.boolean {
				tmp = 0x02
			}
			if err := emit(jsonbPathToken(ginJSONBRotate(item.hash) ^ tmp)); err != nil {
				return err
			}
		case jsonbString:
			if err := emit(jsonbPathToken(ginJSONBRotate(item.hash) ^ ginJSONBHash([]byte(node.text)))); err != nil {
				return err
			}
		case jsonbNumber:
			payload, err := jsonbNumberPayload(ctx, node.number)
			if err != nil {
				return err
			}
			if err := emit(jsonbPathToken(ginJSONBRotate(item.hash) ^ ginJSONBHash([]byte(payload)))); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// walkJSONB dispatches the shared jsonb dictionary grammar by provider. The
// immutable GinExtraction and the live GinBinding query path both use it, so the
// walk lives in exactly one place and neither owns the other.
func walkJSONB(ctx context.Context, provider ginJSONBProvider, root *jsonbNode, emit func(GinToken) error) error {
	if provider == ginJSONBPathOps {
		return walkJSONBPathOps(ctx, root, emit)
	}
	return walkJSONBOps(ctx, root, emit)
}

func jsonbTextQuery(v Value) bool {
	return v.typ != nil && v.typ.BaseType().id == Text
}

func jsonbTextArrayQuery(v Value) bool {
	if v.typ == nil || v.array == nil || !v.typ.BaseType().IsArray() {
		return false
	}
	element := v.typ.BaseType().Element()
	return element != nil && element.BaseType().id == Text
}

// ExtractJSONBQuery is the strategy-aware jsonb query entry point. The
// column-typed ExtractQueryWithEnvironment cannot serve text/text[] operands.
func (p GinBinding) ExtractJSONBQuery(ctx context.Context, strategy int16, v Value, emit func(GinToken) error) (bool, error) {
	return p.ExtractJSONBQueryWithEnvironment(ctx, strategy, v, emit, InputEnvironment{})
}

// ExtractJSONBQueryWithEnvironment enters the selected class's role 3 once and
// emits the query dictionary tokens. allNonNull is the GIN search-mode ALL
// signal; strategy 15/16 stay conservative supersets because jsonpath
// extraction is not implemented, and the scalar jsonpath matcher rechecks.
func (p GinBinding) ExtractJSONBQueryWithEnvironment(ctx context.Context, strategy int16, v Value, emit func(GinToken) error, input InputEnvironment) (allNonNull bool, err error) {
	if p.jsonb == ginJSONBNone {
		return false, fmt.Errorf("stuntgres: foreign GIN jsonb query")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch strategy {
	case 7, 15, 16:
	case 9, 10, 11:
		if p.jsonb == ginJSONBPathOps {
			return false, fmt.Errorf("stuntgres: invalid GIN jsonb strategy")
		}
	default:
		return false, fmt.Errorf("stuntgres: invalid GIN jsonb strategy")
	}
	// A strict operator function is never called for SQL NULL: no tokens and no
	// ALL search mode, so the probe returns no candidates.
	if v.null {
		return false, ctx.Err()
	}
	body, err := enterIndexSupport(input, p.Class.Support(3), 3)
	if err != nil {
		return false, err
	}
	defer body.Leave()
	switch strategy {
	case 7:
		if v.typ != p.Usage.Type || v.jsonb == nil {
			return false, fmt.Errorf("stuntgres: foreign GIN jsonb query")
		}
		count := 0
		err := walkJSONB(ctx, p.jsonb, &v.jsonb.root, func(token GinToken) error {
			count++
			return emit(token)
		})
		return count == 0, err
	case 9:
		if !jsonbTextQuery(v) {
			return false, fmt.Errorf("stuntgres: foreign GIN jsonb query")
		}
		return false, emit(jsonbTextToken(jginFlagKey, v.text))
	case 10, 11:
		if !jsonbTextArrayQuery(v) {
			return false, fmt.Errorf("stuntgres: foreign GIN jsonb query")
		}
		count := 0
		for i := range v.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			element := v.ArrayElement(i)
			if element.IsNull() {
				continue
			}
			if element.typ == nil || element.typ.BaseType().id != Text {
				return false, fmt.Errorf("stuntgres: foreign GIN jsonb query")
			}
			if err := emit(jsonbTextToken(jginFlagKey, element.text)); err != nil {
				return false, err
			}
			count++
		}
		// Empty ?& matches every non-null row; empty ?| matches nothing.
		return strategy == 11 && count == 0, ctx.Err()
	default: // 15 @? and 16 @@
		return true, ctx.Err()
	}
}