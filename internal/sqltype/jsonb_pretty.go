package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// jsonbPrettyLine appends indentation without constructing per-line strings.
func jsonbPrettyLine(ctx context.Context, b *strings.Builder, depth int) error {
	const spaces = "                                                                "
	b.WriteByte('\n')
	for remaining := depth * 4; remaining > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(remaining, len(spaces))
		b.WriteString(spaces[:n])
		remaining -= n
	}
	return ctx.Err()
}

// jsonbPrettyOutput reads the canonical immutable payload, never the cached
// compact text. A request owns the only output builder; errors publish nothing.
func jsonbPrettyOutput(ctx context.Context, root *jsonbNode) (string, error) {
	if root == nil {
		return "", fmt.Errorf("invalid JSONB root")
	}
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
			if err := appendNumericOutput(ctx, &b, n.number.value()); err != nil {
				return "", err
			}
		case jsonbBoolean:
			if n.boolean {
				b.WriteString("true")
			} else {
				b.WriteString("false")
			}
		case jsonbArray, jsonbObject:
			opening, closing := byte('['), byte(']')
			count := len(n.items)
			if n.kind == jsonbObject {
				opening, closing, count = '{', '}', len(n.fields)
			}
			b.WriteByte(opening)
			if count > 0 {
				stack = append(stack, jsonbWalkFrame{node: n, index: 1})
				if err := jsonbPrettyLine(ctx, &b, len(stack)); err != nil {
					return "", err
				}
				if n.kind == jsonbObject {
					if err := writeJSONBString(ctx, &b, n.fields[0].key); err != nil {
						return "", err
					}
					b.WriteString(": ")
					n = &n.fields[0].value
				} else {
					n = &n.items[0]
				}
				continue
			}
			if err := jsonbPrettyLine(ctx, &b, len(stack)); err != nil {
				return "", err
			}
			b.WriteByte(closing)
		default:
			return "", fmt.Errorf("invalid JSONB node")
		}
		for {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if len(stack) == 0 {
				return b.String(), nil
			}
			f := &stack[len(stack)-1]
			i := f.index
			count, closing := len(f.node.items), byte(']')
			if f.node.kind == jsonbObject {
				count, closing = len(f.node.fields), '}'
			}
			if i < count {
				b.WriteByte(',')
				if err := jsonbPrettyLine(ctx, &b, len(stack)); err != nil {
					return "", err
				}
				f.index++
				if f.node.kind == jsonbObject {
					if err := writeJSONBString(ctx, &b, f.node.fields[i].key); err != nil {
						return "", err
					}
					b.WriteString(": ")
					n = &f.node.fields[i].value
				} else {
					n = &f.node.items[i]
				}
				break
			}
			if err := jsonbPrettyLine(ctx, &b, len(stack)-1); err != nil {
				return "", err
			}
			b.WriteByte(closing)
			stack = stack[:len(stack)-1]
		}
	}
}
