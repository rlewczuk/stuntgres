package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func xidReferenceBackends(t *testing.T, p *fixture.Pair, body func(*testing.T, fixture.Backend)) {
	t.Helper()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, b) })
	}
}

func xidReferenceRows(t *testing.T, ctx context.Context, b fixture.Backend, executor fixture.Executor, s fixture.Step) {
	t.Helper()
	raw := append([]string(nil), s.Types...)
	s.RawTypes = raw
	s.Types = append([]string(nil), s.Types...)
	for i, name := range raw {
		if b.PG && name == "XID8" {
			s.Types[i] = ""
		}
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		switch raw[column] {
		case "XID", "XID8", "CID":
			if value != nil {
				bytes, ok := value.([]byte)
				if !ok || bytes == nil {
					t.Fatalf("identifier raw value column=%d row=%d: %T %#v", column, row, value, value)
				}
				if row < len(s.Rows) && string(bytes) != s.Rows[row][column] {
					t.Fatalf("raw identifier %q want %#v", bytes, s.Rows[row][column])
				}
			}
			return true
		}
		return false
	}
	metadata := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if metadata != nil {
			metadata(t, rows)
		}
		for i, name := range raw {
			if name != "XID" && name != "XID8" && name != "CID" {
				continue
			}
			want := reflect.TypeFor[[]byte]()
			if b.PG {
				want = reflect.TypeFor[any]()
			}
			if types[i].ScanType() != want {
				t.Fatalf("%s ScanType %v want %v", name, types[i].ScanType(), want)
			}
			if _, ok := types[i].Length(); ok {
				t.Fatalf("%s exposes length", name)
			}
			if _, _, ok := types[i].DecimalSize(); ok {
				t.Fatalf("%s exposes decimal size", name)
			}
		}
	}
	fixture.RunStep(t, ctx, executor, b.Recorder, s)
}

func xidReferenceRoutes(t *testing.T, ctx context.Context, b fixture.Backend, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		route := s
		if prepared {
			route = fixture.Prepared(route)
		}
		if route.SelectRows && route.State == "" {
			xidReferenceRows(t, ctx, b, b.DB, route)
		} else {
			fixture.RunStep(t, ctx, b.DB, b.Recorder, route)
		}
	}
}

func TestReferenceXIDValuesMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE xid_values(id INT,x pg_catalog.xid,y pg_catalog.xid8,c pg_catalog.cid)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO xid_values VALUES(1,'0','0','0'),(2,'4294967295','18446744073709551615','4294967295'),(3,'2147483648','9223372036854775808','2147483648'),(4,NULL,NULL,NULL)", 4))
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT id,x,y,c FROM xid_values ORDER BY id", [][]any{{int64(1), "0", "0", "0"}, {int64(2), "4294967295", "18446744073709551615", "4294967295"}, {int64(3), "2147483648", "9223372036854775808", "2147483648"}, {int64(4), nil, nil, nil}}},
			{"SELECT id,x,y,c FROM xid_values WHERE id=4", [][]any{{int64(4), nil, nil, nil}}},
			{"SELECT id,x,y,c FROM xid_values WHERE id=99", nil},
		} {
			s := fixture.Select(tc.q, []string{"id", "x", "y", "c"}, []string{"INT4", "XID", "XID8", "CID"}, tc.rows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for _, column := range types[1:] {
					nullable, known := column.Nullable()
					if known != !b.PG || nullable != !b.PG {
						t.Fatalf("declared identifier nullable=%v known=%v postgres=%v", nullable, known, b.PG)
					}
				}
			}
			xidReferenceRoutes(t, p.Ctx, b, s)
		}
		for _, typ := range []string{"xid", "cid", "xid8"} {
			max := "4294967295"
			if typ == "xid8" {
				max = "18446744073709551615"
			}
			cases := []struct{ in, out string }{{"0", "0"}, {"+1", "1"}, {" \t\r\n\v\f+1 \t\r\n\v\f", "1"}, {"0377", "255"}, {"0xff", "255"}, {"-1", max}, {max, max}}
			if typ != "xid8" {
				cases = append(cases, struct{ in, out string }{"-2147483648", "2147483648"}, struct{ in, out string }{"18446744073709551615", "4294967295"}, struct{ in, out string }{"18446744071562067968", "2147483648"}, struct{ in, out string }{"-18446744073709551615", "1"})
			}
			for _, tc := range cases {
				q := fmt.Sprintf("SELECT '%s'::%s AS v", tc.in, typ)
				xidReferenceRoutes(t, p.Ctx, b, fixture.Select(q, []string{"v"}, []string{map[string]string{"xid": "XID", "cid": "CID", "xid8": "XID8"}[typ]}, [][]any{{tc.out}}))
			}
		}
		// Public type lookup independently proves scalar/companion identities.
		for _, fact := range []struct {
			name string
			oid  int64
		}{{"xid", 28}, {"cid", 29}, {"xid8", 5069}, {"_xid", 1011}, {"_cid", 1012}, {"_xid8", 271}} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT 'pg_catalog."+fact.name+"'::regtype::int8 AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{fact.oid}}))
		}
		var castOID, functionOID int64
		var castContext, castMethod string
		err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::int8,castfunc::int8,castcontext,castmethod FROM pg_catalog.pg_cast WHERE castsource=5069 AND casttarget=28").Scan(&castOID, &functionOID, &castContext, &castMethod)
		if err != nil {
			t.Fatal(err)
		}
		// Cast row OIDs are generated independently; fixed function and context
		// facts are exact. Native retains its established local identity range.
		if castOID <= 0 || castOID > 4294967295 || !b.PG && castOID != 20069 || functionOID != 5071 || castContext != "e" || castMethod != "f" {
			t.Fatalf("xid8->xid cast: oid=%d function=%d context=%q method=%q", castOID, functionOID, castContext, castMethod)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT oid::int8,typname,typlen FROM pg_catalog.pg_type WHERE typname IN ('xid','cid','xid8','_xid','_cid','_xid8') ORDER BY oid", []string{"oid", "typname", "typlen"}, []string{"INT8", "NAME", "INT2"}, [][]any{{int64(28), "xid", int64(4)}, {int64(29), "cid", int64(4)}, {int64(271), "_xid8", int64(-1)}, {int64(1011), "_xid", int64(-1)}, {int64(1012), "_cid", int64(-1)}, {int64(5069), "xid8", int64(8)}}))
		if b.PG {
			var oid int64
			var name string
			err := b.DB.QueryRowContext(p.Ctx, "SELECT o.oid::int8,o.opcname FROM pg_catalog.pg_opclass o JOIN pg_catalog.pg_am a ON a.oid=o.opcmethod WHERE o.opcintype='xid8'::regtype AND a.amname='btree' AND o.opcdefault").Scan(&oid, &name)
			if err != nil {
				t.Fatal(err)
			}
			// Observed on the pinned PostgreSQL 18.0 UTF8/C oracle.
			if oid != 10053 || name != "xid8_ops" {
				t.Fatalf("xid8 default btree class: oid=%d name=%q want 10053 xid8_ops", oid, name)
			}
			var hashOID int64
			err = b.DB.QueryRowContext(p.Ctx, "SELECT o.oid::int8 FROM pg_catalog.pg_opclass o JOIN pg_catalog.pg_am a ON a.oid=o.opcmethod WHERE o.opcintype='xid8'::regtype AND a.amname='hash' AND o.opcdefault").Scan(&hashOID)
			if err != nil || hashOID != 10052 {
				t.Fatalf("xid8 default hash class: oid=%d want 10052: %v", hashOID, err)
			}
		}
	})
}
