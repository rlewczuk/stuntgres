package sqltype

import (
	"context"
	"fmt"
	"math/bits"
	"strconv"

	"stuntgres/internal/sqlstate"
)

// GinPageKind selects a released pageinspect validation boundary, not a physical
// page acquisition operation. Opaque inspection deliberately accepts every flag.
type GinPageKind uint8

const (
	GinMetapage GinPageKind = iota
	GinOpaquePage
	GinLeafPage
)

// GinPage retains immutable BYTEA bytes. Fixed fields are read once; segment
// collections are owned by LeafItems. It never opens or reconstructs a relation.
type GinPage struct {
	raw           string
	kind          GinPageKind
	new           bool
	lower         int
	rightlink     uint32
	maxoff, flags uint16
	meta          [10]int64
}
type GinLeafItem struct {
	First  BtreeTID
	NBytes int16
	TIDs   []BtreeTID
}

func ReadGinPage(raw string, kind GinPageKind) (GinPage, error) {
	if kind > GinLeafPage {
		return GinPage{}, nativeLayoutError()
	}
	fail := func(message, detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: message, Detail: detail}
	}
	if len(raw) != 8192 {
		return GinPage{}, fail("invalid page size", fmt.Sprintf("Expected 8192 bytes, got %d.", len(raw)))
	}
	p := GinPage{raw: raw, kind: kind, new: native16(raw[14:]) == 0}
	if p.new {
		return p, nil
	}
	special := int(native16(raw[16:]))
	size := uint16((native16(raw[18:]) & 0xff00) - uint16(special))
	if size != 8 {
		message := "input page is not a valid GIN data leaf page"
		if kind == GinMetapage {
			message = "input page is not a valid GIN metapage"
		}
		return GinPage{}, fail(message, fmt.Sprintf("Expected special size 8, got %d.", size))
	}
	if special > len(raw)-8 {
		return GinPage{}, nativeLayoutError()
	}
	opaque := raw[special:]
	p.rightlink = native32(opaque)
	p.maxoff = native16(opaque[4:])
	p.flags = native16(opaque[6:])
	switch kind {
	case GinMetapage:
		if p.flags != 8 {
			return GinPage{}, fail("input page is not a GIN metapage", fmt.Sprintf("Flags %04X, expected 0008", p.flags))
		}
		p.meta = [10]int64{int64(native32(raw[24:])), int64(native32(raw[28:])), int64(int32(native32(raw[32:]))), int64(native32(raw[36:])), int64(native64(raw[40:])), int64(native32(raw[48:])), int64(native32(raw[52:])), int64(native32(raw[56:])), int64(native64(raw[64:])), int64(int32(native32(raw[72:])))}
	case GinLeafPage:
		if p.flags != 0x83 {
			return GinPage{}, fail("input page is not a compressed GIN data leaf page", fmt.Sprintf("Flags %04X, expected 0083", p.flags))
		}
		p.lower = int(native16(raw[12:]))
	}
	return p, nil
}
func (p GinPage) IsNew() bool     { return p.new }
func (p GinPage) Meta() [10]int64 { return p.meta }
func (p GinPage) Opaque() (uint32, uint16, []string) {
	names := [...]string{"data", "leaf", "deleted", "meta", "list", "list_fullrow", "incomplete_split", "compressed"}
	count := bits.OnesCount16(p.flags & 0xff)
	if p.flags&0xff00 != 0 {
		count++
	}
	flags := make([]string, 0, count)
	for i, name := range names {
		if p.flags&(1<<i) != 0 {
			flags = append(flags, name)
		}
	}
	if unknown := p.flags & 0xff00; unknown != 0 {
		flags = append(flags, strconv.FormatUint(uint64(unknown), 16))
	}
	return p.rightlink, p.maxoff, flags
}
func (p GinPage) LeafItems(ctx context.Context) ([]GinLeafItem, error) {
	var items []GinLeafItem
	for at := 32; ; {
		item, next, present, err := p.LeafItem(ctx, at)
		if err != nil {
			return nil, err
		}
		if !present {
			return items, nil
		}
		items = append(items, item)
		at = next
	}
}

// LeafItem decodes only the selected compressed segment. Later malformed
// segments are not inspected until their actual value-per-call invocation.
func (p GinPage) LeafItem(ctx context.Context, at int) (GinLeafItem, int, bool, error) {
	if err := ctx.Err(); err != nil {
		return GinLeafItem{}, at, false, err
	}
	if p.kind != GinLeafPage {
		return GinLeafItem{}, at, false, nativeLayoutError()
	}
	if p.new {
		return GinLeafItem{}, at, false, nil
	}
	if p.lower < 32 || p.lower > len(p.raw) || at < 32 || at > p.lower {
		return GinLeafItem{}, at, false, nativeLayoutError()
	}
	if at == p.lower {
		return GinLeafItem{}, at, false, nil
	}
	remaining := p.lower - at
	if remaining < 8 {
		return GinLeafItem{}, at, false, nativeLayoutError()
	}
	segment := p.raw[at:p.lower]
	nbytes := int(native16(segment[6:]))
	if nbytes > remaining-8 {
		return GinLeafItem{}, at, false, nativeLayoutError()
	}
	aligned := (nbytes + 1) &^ 1
	if aligned > remaining-8 {
		return GinLeafItem{}, at, false, nativeLayoutError()
	}
	first := btreeTID(segment)
	tids := make([]BtreeTID, 1, nbytes+1)
	tids[0] = first
	item := GinLeafItem{First: first, NBytes: int16(nbytes), TIDs: tids}
	current := uint64(first.Block)<<11 | uint64(first.Offset)
	payload := segment[8 : 8+nbytes]
	for pos := 0; pos < len(payload); {
		if err := ctx.Err(); err != nil {
			return GinLeafItem{}, at, false, err
		}
		delta := uint64(0)
		// REL_18_6 uses the full final octet; continuation is
		// assertion-only and the accumulator is unsigned until output narrowing.
		for octetIndex := range 7 {
			if pos == len(payload) {
				return GinLeafItem{}, at, false, nativeLayoutError()
			}
			octet := payload[pos]
			pos++
			if octetIndex == 6 {
				delta |= uint64(octet) << 42
				break
			}
			delta |= uint64(octet&0x7f) << (uint(octetIndex) * 7)
			if octet&0x80 == 0 {
				break
			}
		}
		current += delta
		item.TIDs = append(item.TIDs, BtreeTID{Block: uint32(current >> 11), Offset: uint16(current & 0x7ff)})
	}
	return item, at + 8 + aligned, true, nil
}
