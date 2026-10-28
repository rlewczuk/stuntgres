package sqltype

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"stuntgres/internal/sqlstate"
)

// Snapshot is immutable and contains only top-level full transaction IDs.
// Visible is not a completion test for subtransaction IDs.
type Snapshot struct {
	xmin, xmax uint64
	members    []uint64
}

// IsSnapshot identifies scalar snapshot payloads, not coercion compatibility.
func IsSnapshot(id ID) bool { return id == PgSnapshot || id == TxidSnapshot }

func NewSnapshot(xmin, xmax uint64, members []uint64) (*Snapshot, error) {
	if uint32(xmin) == 0 || uint32(xmax) == 0 || xmax < xmin {
		return nil, fmt.Errorf("invalid snapshot bounds")
	}
	for i, id := range members {
		if id < xmin || id >= xmax || i > 0 && id <= members[i-1] {
			return nil, fmt.Errorf("invalid snapshot members")
		}
	}
	owned := append([]uint64(nil), members...)
	return &Snapshot{xmin, xmax, owned}, nil
}
func (s *Snapshot) Xmin() uint64        { return s.xmin }
func (s *Snapshot) Xmax() uint64        { return s.xmax }
func (s *Snapshot) Len() int            { return len(s.members) }
func (s *Snapshot) Member(i int) uint64 { return s.members[i] }
func (s *Snapshot) Visible(id uint64) bool {
	if id < s.xmin {
		return true
	}
	if id >= s.xmax {
		return false
	}
	i := sort.Search(len(s.members), func(i int) bool { return s.members[i] >= id })
	return i == len(s.members) || s.members[i] != id
}
func (s *Snapshot) String() string {
	var b strings.Builder
	b.WriteString(strconv.FormatUint(s.xmin, 10))
	b.WriteByte(':')
	b.WriteString(strconv.FormatUint(s.xmax, 10))
	b.WriteByte(':')
	for i, id := range s.members {
		if i != 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(id, 10))
	}
	return b.String()
}
func SnapshotValue(d *Descriptor, s *Snapshot) (Value, error) {
	if d == nil || !IsSnapshot(d.id) || !d.Concrete() || s == nil || uint32(s.xmin) == 0 || uint32(s.xmax) == 0 || s.xmax < s.xmin {
		return Value{}, fmt.Errorf("invalid snapshot value")
	}
	return Value{typ: d, snapshot: s}, nil
}
func (v Value) Snapshot() (*Snapshot, error) {
	if v.typ == nil || !IsSnapshot(v.typ.BaseType().id) || v.null || v.snapshot == nil {
		return nil, fmt.Errorf("invalid snapshot value")
	}
	return v.snapshot, nil
}

// scanSnapshotUint matches base-ten strtou64, including unsigned negation and
// saturation on magnitude overflow. Delimiters, not trailing whitespace, end it.
func scanSnapshotUint(text string, pos *int) (uint64, bool) {
	i := *pos
	for i < len(text) && strings.ContainsRune(" \t\n\r\v\f", rune(text[i])) {
		i++
	}
	negative := false
	if i < len(text) && (text[i] == '+' || text[i] == '-') {
		negative = text[i] == '-'
		i++
	}
	start := i
	var value uint64
	overflow := false
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		digit := uint64(text[i] - '0')
		if value > (^uint64(0)-digit)/10 {
			overflow = true
		} else if !overflow {
			value = value*10 + digit
		}
		i++
	}
	if i == start {
		return 0, false
	}
	if overflow {
		value = ^uint64(0)
	} else if negative {
		value = -value
	}
	*pos = i
	return value, true
}
func (d *Descriptor) snapshotInput(text string) (Value, error) {
	bad := func() (Value, error) {
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type pg_snapshot: \"" + text + "\""}
	}
	pos := 0
	xmin, ok := scanSnapshotUint(text, &pos)
	if !ok || pos == len(text) || text[pos] != ':' {
		return bad()
	}
	pos++
	xmax, ok := scanSnapshotUint(text, &pos)
	if !ok || uint32(xmin) == 0 || uint32(xmax) == 0 || xmax < xmin || pos == len(text) || text[pos] != ':' {
		return bad()
	}
	pos++
	var members []uint64
	for pos < len(text) {
		id, ok := scanSnapshotUint(text, &pos)
		if !ok || id < xmin || id >= xmax || len(members) > 0 && id < members[len(members)-1] {
			return bad()
		}
		if len(members) == 0 || id != members[len(members)-1] {
			members = append(members, id)
		}
		if pos == len(text) {
			break
		}
		if text[pos] != ',' {
			return bad()
		}
		pos++
	}
	return Value{typ: d, snapshot: &Snapshot{xmin, xmax, members}}, nil
}
