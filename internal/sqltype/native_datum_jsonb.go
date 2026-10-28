package sqltype

import "fmt"

type nativeJSONFrame struct {
	node                         *jsonbNode
	start, entries, count, index int
	parentEntry, parentStart     int
	scalar                       bool
}

// Container frames replace recursion: deeply nested immutable JSONB consumes a
// bounded explicit frame per actual nesting level, with cancellation each step.
func (w *nativeDatumWriter) jsonbDatum(v Value) error {
	if v.jsonb == nil {
		return fmt.Errorf("invalid native JSONB")
	}
	root := &v.jsonb.root
	scalar := root.kind != jsonbArray && root.kind != jsonbObject
	stack := []nativeJSONFrame{{node: root, scalar: scalar, parentEntry: -1}}
	origin := w.at
	for len(stack) > 0 {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		f := &stack[len(stack)-1]
		if f.count == 0 && f.entries == 0 {
			f.start = w.at
			count := 1
			header := uint64(0x50000001)
			if !f.scalar {
				if f.node.kind == jsonbArray {
					count = len(f.node.items)
					header = 0x40000000 | uint64(count)
				} else {
					count = 2 * len(f.node.fields)
					header = 0x20000000 | uint64(len(f.node.fields))
				}
			}
			if count > 0x0fffffff {
				return fmt.Errorf("native JSONB too large")
			}
			f.count = count
			if err := w.word(4, header); err != nil {
				return err
			}
			at, err := w.reserve(4 * count)
			if err != nil {
				return err
			}
			f.entries = at
		}
		if f.index == f.count {
			end := w.at
			entry, start := f.parentEntry, f.parentStart
			stack = stack[:len(stack)-1]
			if entry >= 0 {
				if end-start > 0x0fffffff {
					return fmt.Errorf("native JSONB too large")
				}
				w.patch32(entry, 0x50000000|uint32(end-start))
			}
			continue
		}
		i := f.index
		f.index++
		entry := f.entries + 4*i
		start := w.at
		n := f.node
		if !f.scalar {
			if n.kind == jsonbArray {
				n = &n.items[i]
			} else {
				fields := len(n.fields)
				if i < fields {
					if err := w.text(n.fields[i].key); err != nil {
						return err
					}
					if w.at-start > 0x0fffffff {
						return fmt.Errorf("native JSONB key too large")
					}
					w.patch32(entry, uint32(w.at-start))
					continue
				}
				n = &n.fields[i-fields].value
			}
		}
		kind := uint32(0)
		switch n.kind {
		case jsonbNull:
			kind = 0x40000000
		case jsonbBoolean:
			kind = 0x20000000
			if n.boolean {
				kind = 0x30000000
			}
		case jsonbString:
			if err := w.text(n.text); err != nil {
				return err
			}
		case jsonbNumber:
			kind = 0x10000000
			if err := w.align(origin, 4); err != nil {
				return err
			}
			numericStart, err := w.reserve(4)
			if err != nil {
				return err
			}
			if err = w.numeric(n.number.value()); err != nil {
				return err
			}
			w.patch32(numericStart, uint32(w.at-numericStart)<<2)
		case jsonbArray, jsonbObject:
			if err := w.align(origin, 4); err != nil {
				return err
			}
			stack = append(stack, nativeJSONFrame{node: n, parentEntry: entry, parentStart: start})
			continue
		default:
			return fmt.Errorf("invalid native JSONB node")
		}
		if w.at-start > 0x0fffffff {
			return fmt.Errorf("native JSONB value too large")
		}
		w.patch32(entry, kind|uint32(w.at-start))
	}
	return w.ctx.Err()
}
