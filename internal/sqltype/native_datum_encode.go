package sqltype

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
)

// A nil data buffer measures the exact same traversal used to fill the final
// image. No per-datum byte buffers or wire-codec conversions are needed.
type nativeDatumWriter struct {
	ctx  context.Context
	data []byte
	at   int
}

func (w *nativeDatumWriter) reserve(n int) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if n < 0 || n > math.MaxInt32-w.at {
		return 0, fmt.Errorf("native datum too large")
	}
	start := w.at
	w.at += n
	if w.data != nil && w.at > len(w.data) {
		return 0, fmt.Errorf("native datum size changed")
	}
	return start, nil
}
func (w *nativeDatumWriter) word(n int, bits uint64) error {
	at, err := w.reserve(n)
	if err != nil {
		return err
	}
	if w.data != nil {
		for i := range n {
			w.data[at+i] = byte(bits)
			bits >>= 8
		}
	}
	return nil
}
func (w *nativeDatumWriter) text(s string) error {
	for start := 0; start < len(s); start += 1024 {
		end := min(start+1024, len(s))
		at, err := w.reserve(end - start)
		if err != nil {
			return err
		}
		if w.data != nil {
			copy(w.data[at:], s[start:end])
		}
	}
	return w.ctx.Err()
}
func (w *nativeDatumWriter) align(origin, alignment int) error {
	_, err := w.reserve((-(w.at - origin)) & (alignment - 1))
	return err
}
func (w *nativeDatumWriter) patch32(at int, n uint32) {
	if w.data != nil {
		binary.LittleEndian.PutUint32(w.data[at:], n)
	}
}
func (w *nativeDatumWriter) fullDatum(v Value) error {
	if v.typ.AttributeFacts().Length != -1 {
		return w.datum(v)
	}
	start, err := w.reserve(4)
	if err != nil {
		return err
	}
	if err = w.datum(v); err != nil {
		return err
	}
	w.patch32(start, uint32(w.at-start)<<2)
	return nil
}
func (w *nativeDatumWriter) datum(v Value) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if v.typ == nil || v.null {
		return fmt.Errorf("invalid native datum")
	}
	d := v.typ.BaseType()
	if d.IsArray() {
		return w.array(v)
	}
	if d.IsEnum() {
		id, ok := v.EnumMemberID()
		if !ok {
			return fmt.Errorf("invalid native enum")
		}
		return w.word(4, uint64(id))
	}
	if d.IsCube() {
		return w.cubeDatum(v)
	}
	if d.IsHstore() {
		return w.hstoreDatum(v)
	}
	switch d.id {
	case Bool:
		if v.boolean {
			return w.word(1, 1)
		}
		return w.word(1, 0)
	case Char:
		if len(v.text) > 1 {
			return fmt.Errorf("invalid native char")
		}
		if len(v.text) == 0 {
			return w.word(1, 0)
		}
		return w.word(1, uint64(v.text[0]))
	case Int2:
		return w.word(2, uint64(v.integer))
	case Int4:
		return w.word(4, uint64(v.integer))
	case Oid, Xid, Cid, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		return w.word(4, uint64(v.oid))
	case Int8, Xid8, PgLSN, Money, Time, Timestamp, Timestamptz:
		return w.word(8, uint64(v.integer))
	case Float4:
		return w.word(4, uint64(v.float4Bits()))
	case Float8:
		return w.word(8, math.Float64bits(v.floating))
	case Date:
		n := v.integer
		if n == DatePositiveInfinity {
			n = math.MaxInt32
		}
		if n == DateNegativeInfinity {
			n = math.MinInt32
		}
		return w.word(4, uint64(n))
	case Timetz:
		if err := w.word(8, uint64(v.integer)); err != nil {
			return err
		}
		return w.word(4, uint64(v.west))
	case Interval:
		if err := w.word(8, uint64(v.integer)); err != nil {
			return err
		}
		if err := w.word(4, uint64(v.days)); err != nil {
			return err
		}
		return w.word(4, uint64(v.months))
	case Tid:
		if err := w.word(2, uint64(v.integer)>>32); err != nil {
			return err
		}
		if err := w.word(2, uint64(v.integer)>>16); err != nil {
			return err
		}
		return w.word(2, uint64(v.integer))
	case Macaddr:
		for i := 5; i >= 0; i-- {
			if err := w.word(1, uint64(v.integer)>>uint(i*8)); err != nil {
				return err
			}
		}
		return nil
	case UUID:
		at, err := w.reserve(16)
		if err == nil && w.data != nil {
			copy(w.data[at:], v.uuid[:])
		}
		return err
	case Name:
		if len(v.text) > 63 {
			return fmt.Errorf("invalid native name")
		}
		at, err := w.reserve(64)
		if err == nil && w.data != nil {
			copy(w.data[at:], v.text)
		}
		return err
	case Text, VarChar, BPChar, Bytea:
		return w.text(v.text)
	case PgNodeTree:
		if !v.treeKey {
			g, err := BindGrouping(v.typ.Usage())
			if err != nil {
				return err
			}
			v, err = g.PrepareKey(w.ctx, v)
			if err != nil {
				return err
			}
		}
		return w.text(v.text)
	case Numeric:
		return w.numeric(v)
	case Inet, Cidr:
		if !v.network.valid(d.id == Cidr) {
			return fmt.Errorf("invalid native network")
		}
		family := uint64(3)
		if v.network.family == 4 {
			family = 2
		}
		if err := w.word(1, family); err != nil {
			return err
		}
		if err := w.word(1, uint64(v.network.prefix)); err != nil {
			return err
		}
		for _, b := range v.network.address[:networkWidth(v.network)] {
			if err := w.word(1, uint64(b)); err != nil {
				return err
			}
		}
		return nil
	case JSONB:
		return w.jsonbDatum(v)
	default:
		return fmt.Errorf("native datum encoding unavailable for %s", d.DiagnosticName())
	}
}
