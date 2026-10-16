package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func identityMemberMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, id uint32, want []identityMember) {
	t.Helper()
	expected := make([][]any, len(want))
	for i, m := range want {
		expected[i] = []any{m.xid, m.mode}
	}
	s := fixture.Select("SELECT * FROM pg_get_multixact_members(multixid => $1::xid)", []string{"xid", "mode"}, []string{"XID", "TEXT"}, expected, strconv.FormatUint(uint64(id), 10))
	s.Bag = true
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column != 0 {
			return false
		}
		bytes, ok := value.([]byte)
		if !ok || bytes == nil {
			t.Fatalf("XID payload %T %v", value, value)
		}
		if _, err := strconv.ParseUint(string(bytes), 10, 32); err != nil {
			t.Fatal(err)
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		wantScan := reflect.TypeFor[[]byte]()
		if b.PG {
			wantScan = reflect.TypeFor[any]()
		}
		if types[0].ScanType() != wantScan {
			t.Fatalf("XID ScanType %v want %v", types[0].ScanType(), wantScan)
		}
		if _, ok := types[0].Length(); ok {
			t.Fatal("XID exposed length")
		}
		if _, _, ok := types[0].DecimalSize(); ok {
			t.Fatal("XID exposed decimal size")
		}
	}
	identityBoth(t, p, b, x, s)
	s.Query = "SELECT m.owner,m.kind FROM (VALUES($1::xid)) AS v(mx) CROSS JOIN LATERAL pg_get_multixact_members(v.mx) AS m(owner,kind)"
	s.Columns = []string{"owner", "kind"}
	identityBoth(t, p, b, x, s)
	ordinals := make([][]any, len(want))
	for i := range want {
		ordinals[i] = []any{int64(i + 1)}
	}
	identityBoth(t, p, b, x, fixture.Select("SELECT n FROM pg_get_multixact_members($1::xid) WITH ORDINALITY AS m(xid,mode,n) ORDER BY n", []string{"n"}, []string{"INT8"}, ordinals, strconv.FormatUint(uint64(id), 10)))
}
