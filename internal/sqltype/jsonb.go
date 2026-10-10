package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// Only construction mutates these nodes. Published values share an immutable
// payload; numbers borrow the existing exact numeric provider's decimal views.
type jsonbKind uint8

const (
	jsonbNull jsonbKind = iota
	jsonbString
	jsonbNumber
	jsonbBoolean
	jsonbArray
	jsonbObject
)

type jsonbDecimal struct {
	digits          string
	exponent, scale int64
	negative        bool
}

func (n jsonbDecimal) view() numericView {
	return numericView{text: n.digits, exponent: n.exponent, negative: n.negative}
}
func (n jsonbDecimal) value() Value {
	return Value{text: n.digits, exponent: n.exponent, scale: n.scale, negative: n.negative}
}

type jsonbNode struct {
	kind    jsonbKind
	text    string
	number  jsonbDecimal
	boolean bool
	items   []jsonbNode
	fields  []jsonbEntry
}
type jsonbEntry struct {
	key     string
	value   jsonbNode
	ordinal int
}
type jsonbPayload struct{ root jsonbNode }
type jsonbFrame struct {
	node jsonbNode
	key  string
}
type jsonbBuilder struct {
	frames []jsonbFrame
	root   jsonbNode
}

func (b *jsonbBuilder) add(n jsonbNode) {
	if len(b.frames) == 0 {
		b.root = n
		return
	}
	f := &b.frames[len(b.frames)-1]
	if f.node.kind == jsonbArray {
		f.node.items = append(f.node.items, n)
	} else {
		f.node.fields = append(f.node.fields, jsonbEntry{key: f.key, value: n, ordinal: len(f.node.fields)})
	}
}
func (b *jsonbBuilder) open(kind jsonbKind) {
	b.frames = append(b.frames, jsonbFrame{node: jsonbNode{kind: kind}})
}
func (b *jsonbBuilder) close(ctx context.Context) error {
	n := b.frames[len(b.frames)-1].node
	if n.kind == jsonbObject {
		if err := sortJSONBFields(ctx, n.fields); err != nil {
			return err
		}
		fields, err := compactJSONBFields(ctx, n.fields)
		if err != nil {
			return err
		}
		n.fields = fields
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.frames = b.frames[:len(b.frames)-1]
	b.add(n)
	return nil
}

const jsonbWorkChunk = 1024

// Key comparisons must also be bounded: equal keys can have long shared prefixes.
func compareJSONBKeys(ctx context.Context, a, b string) (int, error) {
	if len(a) != len(b) {
		return len(a) - len(b), ctx.Err()
	}
	for start := 0; start < len(a); start += jsonbWorkChunk {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		end := min(start+jsonbWorkChunk, len(a))
		if c := strings.Compare(a[start:end], b[start:end]); c != 0 {
			return c, nil
		}
	}
	return 0, ctx.Err()
}

// In-place heapsort has bounded sift steps and no scratch allocation. Unlike
// a standard-library sort callback, every step can return cancellation directly.
func sortJSONBFields(ctx context.Context, fields []jsonbEntry) error {
	compare := func(a, b jsonbEntry) (int, error) {
		c, err := compareJSONBKeys(ctx, a.key, b.key)
		if err != nil || c != 0 {
			return c, err
		}
		return b.ordinal - a.ordinal, nil // Latest decoded duplicate first.
	}
	sift := func(root, end int) error {
		for root < end/2 {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := root*2 + 1
			if child+1 < end {
				c, err := compare(fields[child], fields[child+1])
				if err != nil {
					return err
				}
				if c < 0 {
					child++
				}
			}
			c, err := compare(fields[root], fields[child])
			if err != nil {
				return err
			}
			if c >= 0 {
				break
			}
			fields[root], fields[child] = fields[child], fields[root]
			root = child
		}
		return nil
	}
	for root := len(fields)/2 - 1; root >= 0; root-- {
		if err := sift(root, len(fields)); err != nil {
			return err
		}
	}
	for end := len(fields) - 1; end > 0; end-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		fields[0], fields[end] = fields[end], fields[0]
		if err := sift(0, end); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func compactJSONBFields(ctx context.Context, fields []jsonbEntry) ([]jsonbEntry, error) {
	count := 0
	for i := range fields {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if count > 0 {
			c, err := compareJSONBKeys(ctx, fields[count-1].key, fields[i].key)
			if err != nil {
				return nil, err
			}
			if c == 0 {
				continue
			}
		}
		fields[count] = fields[i]
		count++
	}
	for i := count; i < len(fields); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fields[i] = jsonbEntry{}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return fields[:count], nil
}

func jsonbRank(n *jsonbNode, root bool) int {
	if root && n.kind == jsonbArray && len(n.items) == 0 {
		return -1
	}
	return int(n.kind)
}

type jsonbPairFrame struct {
	a, b  *jsonbNode
	index int
}

// The top-level empty-array anomaly belongs only to the raw-scalar wrapper.
// Nested nodes use the ordinary type order, with counts before contents.
func compareJSONB(ctx context.Context, a, b *jsonbNode, representation bool) (int, error) {
	if a == b {
		return 0, ctx.Err()
	}
	if ar, br := jsonbRank(a, true), jsonbRank(b, true); ar != br {
		if ar < br {
			return -1, nil
		}
		return 1, nil
	}
	var local [32]jsonbPairFrame
	stack := local[:0]
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if a.kind != b.kind {
			if a.kind < b.kind {
				return -1, nil
			}
			return 1, nil
		}
		c := 0
		switch a.kind {
		case jsonbString:
			c = strings.Compare(a.text, b.text)
		case jsonbNumber:
			c = compareNumericViews(a.number.view(), b.number.view())
			if c == 0 && representation && a.number.scale != b.number.scale {
				if a.number.scale < b.number.scale {
					c = -1
				} else {
					c = 1
				}
			}
		case jsonbBoolean:
			if a.boolean != b.boolean {
				if a.boolean {
					c = 1
				} else {
					c = -1
				}
			}
		case jsonbArray:
			c = len(a.items) - len(b.items)
			if c == 0 && len(a.items) > 0 {
				stack = append(stack, jsonbPairFrame{a: a, b: b, index: 1})
				a, b = &a.items[0], &b.items[0]
				continue
			}
		case jsonbObject:
			c = len(a.fields) - len(b.fields)
			if c == 0 && len(a.fields) > 0 {
				if c = strings.Compare(a.fields[0].key, b.fields[0].key); c != 0 {
					return c, nil
				}
				stack = append(stack, jsonbPairFrame{a: a, b: b, index: 1})
				a, b = &a.fields[0].value, &b.fields[0].value
				continue
			}
		}
		if c != 0 {
			return c, nil
		}
		for {
			if len(stack) == 0 {
				return 0, ctx.Err()
			}
			f := &stack[len(stack)-1]
			i := f.index
			if f.a.kind == jsonbArray {
				if i < len(f.a.items) {
					f.index++
					a, b = &f.a.items[i], &f.b.items[i]
					break
				}
			} else if i < len(f.a.fields) {
				if c := strings.Compare(f.a.fields[i].key, f.b.fields[i].key); c != 0 {
					return c, nil
				}
				f.index++
				a, b = &f.a.fields[i].value, &f.b.fields[i].value
				break
			}
			stack = stack[:len(stack)-1]
		}
	}
}

func writeJSONBString(ctx context.Context, b *strings.Builder, text string) error {
	b.WriteByte('"')
	start := 0
	const hex = "0123456789abcdef"
	for chunk := 0; chunk < len(text); chunk += jsonbWorkChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(chunk+jsonbWorkChunk, len(text))
		for i := chunk; i < end; i++ {
			c := text[i]
			if c != '"' && c != '\\' && c >= 32 {
				continue
			}
			b.WriteString(text[start:i])
			switch c {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteByte(c)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				b.WriteString(`\u00`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&15])
			}
			start = i + 1
		}
		b.WriteString(text[start:end])
		start = end
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.WriteByte('"')
	return nil
}

type jsonbWalkFrame struct {
	node  *jsonbNode
	index int
}

func jsonbOutput(ctx context.Context, root *jsonbNode) (string, error) {
	var b strings.Builder
	var local [32]jsonbWalkFrame
	stack := local[:0]
	n := root
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch n.kind {
		case jsonbNull:
			b.WriteString("null")
		case jsonbString:
			if err := writeJSONBString(ctx, &b, n.text); err != nil {
				return "", err
			}
		case jsonbNumber:
			b.WriteString(numericOutput(n.number.value()))
		case jsonbBoolean:
			if n.boolean {
				b.WriteString("true")
			} else {
				b.WriteString("false")
			}
		case jsonbArray:
			b.WriteByte('[')
			if len(n.items) > 0 {
				stack = append(stack, jsonbWalkFrame{node: n, index: 1})
				n = &n.items[0]
				continue
			}
			b.WriteByte(']')
		case jsonbObject:
			b.WriteByte('{')
			if len(n.fields) > 0 {
				if err := writeJSONBString(ctx, &b, n.fields[0].key); err != nil {
					return "", err
				}
				b.WriteString(": ")
				stack = append(stack, jsonbWalkFrame{node: n, index: 1})
				n = &n.fields[0].value
				continue
			}
			b.WriteByte('}')
		default:
			return "", fmt.Errorf("invalid JSONB node")
		}
		for {
			if len(stack) == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				return b.String(), nil
			}
			f := &stack[len(stack)-1]
			i := f.index
			if f.node.kind == jsonbArray {
				if i < len(f.node.items) {
					b.WriteString(", ")
					f.index++
					n = &f.node.items[i]
					break
				}
				b.WriteByte(']')
			} else {
				if i < len(f.node.fields) {
					b.WriteString(", ")
					if err := writeJSONBString(ctx, &b, f.node.fields[i].key); err != nil {
						return "", err
					}
					b.WriteString(": ")
					f.index++
					n = &f.node.fields[i].value
					break
				}
				b.WriteByte('}')
			}
			stack = stack[:len(stack)-1]
		}
	}
}

// Hash structures, not rendered text: numeric display scale is immaterial.
func hashJSONB(ctx context.Context, root *jsonbNode, h uint64) (uint64, error) {
	byteHash := func(b byte) { h = (h ^ uint64(b)) * 1099511628211 }
	integerHash := func(n uint64) {
		for range 8 {
			byteHash(byte(n))
			n >>= 8
		}
	}
	textHash := func(s string) error {
		integerHash(uint64(len(s)))
		for i := range len(s) {
			if i&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			byteHash(s[i])
		}
		return nil
	}
	var local [32]jsonbWalkFrame
	stack := local[:0]
	n := root
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		byteHash(byte(n.kind))
		switch n.kind {
		case jsonbString:
			if err := textHash(n.text); err != nil {
				return 0, err
			}
		case jsonbNumber:
			if n.number.negative {
				byteHash(1)
			} else {
				byteHash(0)
			}
			integerHash(uint64(n.number.exponent))
			if err := textHash(n.number.digits); err != nil {
				return 0, err
			}
		case jsonbBoolean:
			if n.boolean {
				byteHash(1)
			} else {
				byteHash(0)
			}
		case jsonbArray:
			integerHash(uint64(len(n.items)))
			if len(n.items) > 0 {
				stack = append(stack, jsonbWalkFrame{node: n, index: 1})
				n = &n.items[0]
				continue
			}
		case jsonbObject:
			integerHash(uint64(len(n.fields)))
			if len(n.fields) > 0 {
				if err := textHash(n.fields[0].key); err != nil {
					return 0, err
				}
				stack = append(stack, jsonbWalkFrame{node: n, index: 1})
				n = &n.fields[0].value
				continue
			}
		}
		for {
			if len(stack) == 0 {
				return h, ctx.Err()
			}
			f := &stack[len(stack)-1]
			i := f.index
			if f.node.kind == jsonbArray {
				if i < len(f.node.items) {
					f.index++
					n = &f.node.items[i]
					break
				}
			} else if i < len(f.node.fields) {
				if err := textHash(f.node.fields[i].key); err != nil {
					return 0, err
				}
				f.index++
				n = &f.node.fields[i].value
				break
			}
			stack = stack[:len(stack)-1]
		}
	}
}
