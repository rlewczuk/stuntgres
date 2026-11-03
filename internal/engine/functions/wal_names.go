package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// WALNameMetadata is immutable cluster startup arithmetic input, copied at bind
// time. It confers no physical WAL or runtime authority.
type WALNameMetadata struct {
	SegmentSize uint32
	Timeline    uint32
}

func (m WALNameMetadata) Validate() error {
	if m.SegmentSize < 1<<20 || m.SegmentSize > 1<<30 || m.SegmentSize&(m.SegmentSize-1) != 0 {
		return fmt.Errorf("stuntgres: invalid WAL name segment size")
	}
	return nil
}

// FileName uses PG18's containing segment, including the new segment at an
// exact boundary. All arithmetic remains unsigned through the full LSN range.
func (m WALNameMetadata) FileName(lsn uint64) (string, uint32, error) {
	if err := m.Validate(); err != nil {
		return "", 0, err
	}
	segment := lsn / uint64(m.SegmentSize)
	perLog := uint64(1) << 32 / uint64(m.SegmentSize)
	words := [3]uint32{m.Timeline, uint32(segment / perLog), uint32(segment % perLog)}
	var name [24]byte
	const hex = "0123456789ABCDEF"
	for i, word := range words {
		for j := 7; j >= 0; j-- {
			name[i*8+j] = hex[word&15]
			word >>= 4
		}
	}
	return string(name[:]), uint32(lsn & uint64(m.SegmentSize-1)), nil
}

// Split accepts every 24-byte ASCII hex name, including noncanonical segment
// words. It never normalizes the input used by the released diagnostic.
func (m WALNameMetadata) Split(name string) (uint64, int64, error) {
	if err := m.Validate(); err != nil {
		return 0, 0, err
	}
	var words [3]uint32
	valid := len(name) == 24
	if valid {
		for i := range len(name) {
			c := name[i]
			var digit byte
			switch {
			case c >= '0' && c <= '9':
				digit = c - '0'
			case c >= 'A' && c <= 'F':
				digit = c - 'A' + 10
			case c >= 'a' && c <= 'f':
				digit = c - 'a' + 10
			default:
				valid = false
			}
			if !valid {
				break
			}
			words[i/8] = words[i/8]<<4 | uint32(digit)
		}
	}
	if !valid {
		return 0, 0, sqlError("22023", "invalid WAL file name \""+name+"\"")
	}
	return uint64(words[1])*((uint64(1)<<32)/uint64(m.SegmentSize)) + uint64(words[2]), int64(words[0]), nil
}

type WALNameKind uint8

const (
	WALFileName WALNameKind = iota
	WALFileNameOffset
	WALSplitFileName
)

var walNameNames = [...]string{"pg_walfile_name", "pg_walfile_name_offset", "pg_split_walfile_name"}

func (k WALNameKind) Name() string {
	if int(k) >= len(walNameNames) {
		return ""
	}
	return walNameNames[k]
}
func (k WALNameKind) input() sqltype.ID {
	if k == WALSplitFileName {
		return sqltype.Text
	}
	return sqltype.PgLSN
}
func (k WALNameKind) fields() ([2]string, [2]sqltype.ID) {
	if k == WALSplitFileName {
		return [2]string{"segment_number", "timeline_id"}, [2]sqltype.ID{sqltype.Numeric, sqltype.Int8}
	}
	return [2]string{"file_name", "file_offset"}, [2]sqltype.ID{sqltype.Text, sqltype.Int4}
}

// WALName retains an owned typed recipe, not an engine/settings pointer.
type WALName struct {
	expr.Info
	Kind     WALNameKind
	Arg      expr.Expr
	Metadata WALNameMetadata
}

func (n WALName) Validate() error {
	if err := n.Metadata.Validate(); err != nil {
		return err
	}
	if n.Kind.Name() == "" || n.Arg == nil || n.Usage.Type == nil || !n.Usage.Type.Concrete() || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid WAL name recipe")
	}
	u := n.Arg.ResultUsage()
	if u.Type == nil || u.Type.ID() != n.Kind.input() || !u.Type.Concrete() || u != u.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid WAL name operand")
	}
	if n.Kind == WALFileName {
		if n.Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid WAL name result")
		}
		return nil
	}
	t := n.Usage.Type
	if t.ID() != 2249 || !t.IsComposite() || t.CompositeLen() != 2 {
		return fmt.Errorf("stuntgres: invalid WAL name record")
	}
	labels, types := n.Kind.fields()
	for i := range 2 {
		m := t.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != labels[i] || m.Usage.Type == nil || m.Usage.Type.ID() != types[i] || !m.Usage.Type.Concrete() || m.Usage != m.Usage.Type.Usage() {
			return fmt.Errorf("stuntgres: invalid WAL name record field")
		}
	}
	return nil
}

func (n WALName) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Arg.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	if n.Kind == WALSplitFileName {
		name, err := value.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		segment, timeline, err := n.Metadata.Split(name)
		if err != nil {
			return sqltype.Value{}, err
		}
		fields := make([]sqltype.Value, 2)
		fields[0], err = sqltype.NumericUint64Value(n.Usage.Type.CompositeMember(0).Usage.Type, segment)
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[1], err = sqltype.Integer(n.Usage.Type.CompositeMember(1).Usage.Type, timeline)
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.OwnedCompositeValue(n.Usage.Type, fields)
	}
	lsn, err := value.PGLSN()
	if err != nil {
		return sqltype.Value{}, err
	}
	name, offset, err := n.Metadata.FileName(lsn)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind == WALFileName {
		return sqltype.String(n.Usage.Type, name), nil
	}
	fields := make([]sqltype.Value, 2)
	fields[0] = sqltype.String(n.Usage.Type.CompositeMember(0).Usage.Type, name)
	fields[1], err = sqltype.Integer(n.Usage.Type.CompositeMember(1).Usage.Type, int64(offset))
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.OwnedCompositeValue(n.Usage.Type, fields)
}

func (n WALName) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
