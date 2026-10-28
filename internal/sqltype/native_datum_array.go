package sqltype

import (
	"fmt"
	"math"
)

func (w *nativeDatumWriter) array(v Value) error {
	d := v.typ.BaseType()
	if v.array == nil || d.element == nil {
		return fmt.Errorf("invalid native array")
	}
	// Origin includes the omitted outer varlena header.
	origin := w.at - 4
	dims := v.array.dimensions
	values := v.array.elements
	nullable := false
	for _, e := range values {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		nullable = nullable || e.null
	}
	if err := w.word(4, uint64(len(dims))); err != nil {
		return err
	}
	offset, err := w.reserve(4)
	if err != nil {
		return err
	}
	if err = w.word(4, uint64(d.element.id)); err != nil {
		return err
	}
	for _, dim := range dims {
		if err = w.word(4, uint64(dim.Length)); err != nil {
			return err
		}
	}
	for _, dim := range dims {
		if err = w.word(4, uint64(dim.Lower)); err != nil {
			return err
		}
	}
	if nullable {
		bitmap, err := w.reserve((len(values) + 7) / 8)
		if err != nil {
			return err
		}
		if w.data != nil {
			for i, e := range values {
				if err := w.ctx.Err(); err != nil {
					return err
				}
				if !e.null {
					w.data[bitmap+i/8] |= 1 << uint(i%8)
				}
			}
		}
	}
	if err = w.align(origin, 8); err != nil {
		return err
	}
	if nullable {
		w.patch32(offset, uint32(w.at-origin))
	}
	alignment := 1
	switch d.element.AttributeFacts().Alignment {
	case "s":
		alignment = 2
	case "i":
		alignment = 4
	case "d":
		alignment = 8
	}
	for _, e := range values {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		if !e.ValidResult(d.element.Usage()) {
			return fmt.Errorf("invalid native array element")
		}
		if e.null {
			continue
		}
		if err = w.align(origin, alignment); err != nil {
			return err
		}
		if err = w.fullDatum(e); err != nil {
			return err
		}
	}
	return w.ctx.Err()
}

func (w *nativeDatumWriter) cubeDatum(v Value) error {
	if !v.validCube() {
		return fmt.Errorf("invalid native cube")
	}
	header := uint64(v.cube.dimension)
	if v.cube.point {
		header |= 0x80000000
	}
	if err := w.word(4, header); err != nil {
		return err
	}
	for _, c := range v.cube.coordinates {
		if err := w.word(8, math.Float64bits(c)); err != nil {
			return err
		}
	}
	return w.ctx.Err()
}

func (w *nativeDatumWriter) hstoreDatum(v Value) error {
	if !v.validHstore() {
		return fmt.Errorf("invalid native hstore")
	}
	pairs := v.hstore.pairs
	if err := w.word(4, uint64(len(pairs))|0x80000000); err != nil {
		return err
	}
	entries, err := w.reserve(8 * len(pairs))
	if err != nil {
		return err
	}
	start := w.at
	for i, p := range pairs {
		if err = w.text(p.key); err != nil {
			return err
		}
		end := w.at - start
		if end > 0x3fffffff {
			return fmt.Errorf("native hstore too large")
		}
		flags := uint32(0)
		if i == 0 {
			flags = 0x80000000
		}
		w.patch32(entries+8*i, uint32(end)|flags)
		if !p.null {
			if err = w.text(p.value); err != nil {
				return err
			}
		}
		end = w.at - start
		if end > 0x3fffffff {
			return fmt.Errorf("native hstore too large")
		}
		flags = 0
		if p.null {
			flags = 0x40000000
		}
		w.patch32(entries+8*i+4, uint32(end)|flags)
	}
	return w.ctx.Err()
}
