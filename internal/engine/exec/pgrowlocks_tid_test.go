package exec

import (
	"math"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPgrowlocksLogicalTID(t *testing.T) {
	var typ *sqltype.Descriptor
	for _, builtin := range sqltype.Builtins() {
		if builtin.ID() == sqltype.Tid {
			typ = builtin
		}
	}
	maximum := uint64(math.MaxUint32) * 65535
	for _, tc := range []struct {
		version uint64
		block   uint32
		offset  uint16
	}{{1, 0, 1}, {65535, 0, 65535}, {65536, 1, 1}, {maximum, math.MaxUint32 - 1, 65535}} {
		block, offset, err := PgrowlocksLogicalTID(ids.VersionID(tc.version))
		if err != nil || block != tc.block || offset != tc.offset {
			t.Fatal(tc, block, offset, err)
		}
		value, err := sqltype.TIDValue(typ, block, offset)
		if err != nil {
			t.Fatal(err)
		}
		gotBlock, gotOffset, err := value.TID()
		if err != nil || gotBlock != block || gotOffset != offset {
			t.Fatal("TID packing", value, err)
		}
	}
	for _, version := range []uint64{0, maximum + 1, math.MaxUint64} {
		if _, _, err := PgrowlocksLogicalTID(ids.VersionID(version)); err == nil || err.Error() != "stuntgres: logical row version exceeds tid range" {
			t.Fatal("unchecked TID", version, err)
		}
	}
	for _, version := range []uint64{1, 65534, 65535, 65536, maximum - 1} {
		a, b, _ := PgrowlocksLogicalTID(ids.VersionID(version))
		c, d, _ := PgrowlocksLogicalTID(ids.VersionID(version + 1))
		if a == c && b == d {
			t.Fatal("adjacent versions collide", version)
		}
	}
}
