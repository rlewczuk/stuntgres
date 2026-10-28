package sqltype

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// StatisticsMCVItem is a checked native construction input, not SQL input.
// Dictionary indexes address only non-NULL values; NULL indexes must be zero.
type StatisticsMCVItem struct {
	Nulls                    [8]bool
	Indexes                  [8]uint16
	Frequency, BaseFrequency float64
}

// StatisticsMCVBuild freezes a native linux/amd64 MCV image. Logical dimension
// identities remain intact while physical layout follows their current bases.
func StatisticsMCVBuild(ctx context.Context, descriptor *Descriptor, usages []Usage, dictionaries [8][]Value, items []StatisticsMCVItem) (Value, error) {
	invalid := func() (Value, error) { return Value{}, fmt.Errorf("invalid MCV construction") }
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	width := len(usages)
	if descriptor == nil || descriptor.id != PgMCVList || descriptor.statisticsProducerOwner != descriptor || !descriptor.Concrete() || width < 1 || width > 8 || len(items) < 1 || len(items) > 10000 {
		return invalid()
	}
	var sizes, aligned [8]int
	var facts [8]PostgreSQLAttributeFacts
	total := 14 + 24*width + len(items)*(3*width+16)
	for d, u := range usages {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if u.Type == nil {
			return invalid()
		}
		if _, err := BindGrouping(u); err != nil {
			return Value{}, err
		}
		facts[d] = u.Type.AttributeFacts()
		if len(dictionaries[d]) > 65535 {
			return invalid()
		}
		for _, v := range dictionaries[d] {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if v.IsNull() || !v.ValidResult(u) {
				return invalid()
			}
			w := nativeDatumWriter{ctx: ctx}
			if err := w.datum(v); err != nil {
				return Value{}, err
			}
			n := w.at
			f := facts[d]
			if f.Length > 0 && n != int(f.Length) {
				return invalid()
			}
			sizes[d] += n
			if f.Length < 0 {
				sizes[d] += 4
			}
			if !f.ByValue {
				allocation := n
				if f.Length == -1 {
					allocation += 4
				}
				aligned[d] += (allocation + 7) &^ 7
			}
			if sizes[d] > math.MaxInt32 || aligned[d] > math.MaxInt32 {
				return invalid()
			}
		}
		total += sizes[d]
		if total > math.MaxInt32 {
			return invalid()
		}
	}
	for d := width; d < 8; d++ {
		if len(dictionaries[d]) != 0 {
			return invalid()
		}
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if math.IsNaN(item.Frequency) || math.IsInf(item.Frequency, 0) || item.Frequency <= 0 || item.Frequency > 1 || math.IsNaN(item.BaseFrequency) || math.IsInf(item.BaseFrequency, 0) || item.BaseFrequency < 0 || item.BaseFrequency > 1 {
			return invalid()
		}
		for d := range 8 {
			if d >= width {
				if item.Nulls[d] || item.Indexes[d] != 0 {
					return invalid()
				}
				continue
			}
			if item.Nulls[d] {
				if item.Indexes[d] != 0 {
					return invalid()
				}
			} else if int(item.Indexes[d]) >= len(dictionaries[d]) {
				return invalid()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	data := make([]byte, total)
	put32 := func(at int, n uint32) { binary.LittleEndian.PutUint32(data[at:], n) }
	put32(0, 0xE1A651C2)
	put32(4, 1)
	put32(8, uint32(len(items)))
	binary.LittleEndian.PutUint16(data[12:], uint16(width))
	at := 14 + 24*width
	for d, u := range usages {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		put32(14+4*d, uint32(u.Type.id))
		p := 14 + 4*width + 20*d
		f := facts[d]
		put32(p, uint32(len(dictionaries[d])))
		put32(p+4, uint32(sizes[d]))
		put32(p+8, uint32(aligned[d]))
		put32(p+12, uint32(int32(f.Length)))
		if f.ByValue {
			data[p+16] = 1
		}
		for _, v := range dictionaries[d] {
			start := at
			if f.Length < 0 {
				at += 4
			}
			w := nativeDatumWriter{ctx: ctx, data: data, at: at}
			if err := w.datum(v); err != nil {
				return Value{}, err
			}
			at = w.at
			if f.Length < 0 {
				put32(start, uint32(at-start-4))
			}
		}
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		for d := range width {
			if item.Nulls[d] {
				data[at+d] = 1
			}
		}
		binary.LittleEndian.PutUint64(data[at+width:], math.Float64bits(item.Frequency))
		binary.LittleEndian.PutUint64(data[at+width+8:], math.Float64bits(item.BaseFrequency))
		for d := range width {
			binary.LittleEndian.PutUint16(data[at+width+16+2*d:], item.Indexes[d])
		}
		at += 3*width + 16
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	result, err := StatisticsMCVValue(descriptor, data)
	if err != nil {
		return Value{}, err
	}
	if err = ctx.Err(); err != nil {
		return Value{}, err
	}
	return result, nil
}

// StatisticsRetainedTreeValue restores an already prepared structural key only.
// Callers must hold destination catalog authority; this is not a node parser,
// ordinary input codec, or authority to reconstruct expression trees.
func StatisticsRetainedTreeValue(usage Usage, text string) (Value, error) {
	d := usage.Type
	if d == nil || d.id != PgNodeTree || d.statisticsProducerOwner != d || d.IsDomain() || !d.Concrete() || usage != d.Usage() || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return Value{}, fmt.Errorf("invalid retained statistics tree")
	}
	return Value{typ: d, text: text, treeKey: true}, nil
}
