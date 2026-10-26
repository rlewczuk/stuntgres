package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var pgOpclassColumns = []string{"oid", "opcmethod", "opcname", "opcnamespace", "opcowner", "opcfamily", "opcintype", "opcdefault", "opckeytype"}
var pgOpclassTypes = []string{"OID", "OID", "NAME", "OID", "OID", "OID", "OID", "BOOL", "OID"}

// Independent proposed REL_18_0 identities; the root confirms these against the
// pinned oracle before production. No expected value comes from native lookup.
func pgOpclassFacts() [][]any {
	var rows [][]any
	for _, f := range []struct {
		name                                 string
		input, btree, bfamily, hash, hfamily int64
	}{
		{"bool_ops", 16, 10003, 424, 10048, 2222},
		{"bytea_ops", 17, 10006, 428, 10049, 2223},
		{"char_ops", 18, 10007, 429, 10008, 431},
		{"name_ops", 19, 10028, 1994, 10029, 1995},
		{"int2_ops", 21, 1979, 1976, 10019, 1977},
		{"int4_ops", 23, 1978, 1976, 10020, 1977},
		{"int8_ops", 20, 3124, 1976, 10021, 1977},
		{"text_ops", 25, 3126, 1994, 10037, 1995},
		{"varchar_ops", 25, 10044, 1994, 10045, 1995},
		{"bpchar_ops", 1042, 10004, 426, 10005, 427},
		{"oid_ops", 26, 1981, 1989, 10031, 1990},
		{"xid8_ops", 5069, 10053, 5067, 10052, 5032},
		{"tid_ops", 27, 10050, 2789, 10055, 2227},
		{"macaddr_ops", 829, 10024, 1984, 0, 0}, // Pinned18.6 class observation; released family identity.
		{"xid_ops", 28, 0, 0, 10051, 2225},
		{"cid_ops", 29, 0, 0, 10054, 2226},
		{"float4_ops", 700, 10012, 1970, 10013, 1971},
		{"float8_ops", 701, 3123, 1970, 10014, 1971},
		{"date_ops", 1082, 3122, 434, 10011, 435},
		{"time_ops", 1083, 10038, 1996, 10039, 1997},
		{"timestamp_ops", 1114, 3128, 434, 10046, 2040},
		{"timestamptz_ops", 1184, 3127, 434, 10040, 1999},
		{"timetz_ops", 1266, 10041, 2000, 10042, 2001},
		{"interval_ops", 1186, 10022, 1982, 10023, 1983},
		{"numeric_ops", 1700, 3125, 1988, 10030, 1998},
		{"money_ops", 790, 10047, 2099, 0, 0},
		{"uuid_ops", 2950, 10065, 2968, 10066, 2969},
		{"inet_ops", 869, 10015, 1974, 0, 0},
		{"jsonb_ops", 3802, 10088, 4033, 10089, 4034},
		{"text_pattern_ops", 25, 4217, 2095, 10056, 2229},
		{"varchar_pattern_ops", 25, 4218, 2095, 10057, 2229},
		{"bpchar_pattern_ops", 1042, 4219, 2097, 10058, 2231},
		{"enum_ops", 3500, 10069, 3522, 10070, 3523},
	} {
		def := f.name != "varchar_ops" && !strings.Contains(f.name, "pattern")
		if f.btree != 0 {
			key := int64(0)
			if f.name == "name_ops" {
				key = 2275
			}
			rows = append(rows, []any{f.btree, int64(403), f.name, int64(11), int64(10), f.bfamily, f.input, def, key})
		}
		if f.hash != 0 {
			rows = append(rows, []any{f.hash, int64(405), f.name, int64(11), int64(10), f.hfamily, f.input, def, int64(0)})
		}
	}
	rows = append(rows,
		[]any{int64(10017), int64(783), "inet_ops", int64(11), int64(10), int64(3550), int64(869), false, int64(0)},
		[]any{int64(10078), int64(783), "range_ops", int64(11), int64(10), int64(3919), int64(3831), true, int64(0)},
	)
	rows = append(rows, pgOpclassBrinFacts()...)
	sort.Slice(rows, func(i, j int) bool { return rows[i][0].(int64) < rows[j][0].(int64) })
	return rows
}

func pgOpclassScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgOpclassRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want [][]any, prepared bool, args ...any) {
	t.Helper()
	s := fixture.Select(q, pgOpclassColumns, pgOpclassTypes, want, args...)
	s.Prepared = prepared
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { pgOpclassMetadata(t, r, b.PG) }
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
}

func pgOpclassMetadata(t *testing.T, r *sql.Rows, pg bool) {
	t.Helper()
	cols, err := r.Columns()
	if err != nil || !reflect.DeepEqual(cols, pgOpclassColumns) {
		t.Fatal(cols, err)
	}
	types, err := r.ColumnTypes()
	if err != nil || len(types) != 9 {
		t.Fatal(types, err)
	}
	for i, c := range types {
		scan := reflect.TypeFor[int64]()
		if i == 2 {
			scan = reflect.TypeFor[string]()
		}
		if i == 7 {
			scan = reflect.TypeFor[bool]()
		}
		if pg && i != 7 {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != cols[i] || c.DatabaseTypeName() != pgOpclassTypes[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %q %q %v; want %q %q %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), cols[i], pgOpclassTypes[i], scan)
		}
		if n, k := c.Nullable(); n || k == pg {
			t.Fatal("nullability", i, n, k)
		}
		if n, k := c.Length(); n != 0 || k {
			t.Fatal("length", i, n, k)
		}
		if a, z, k := c.DecimalSize(); a != 0 || z != 0 || k {
			t.Fatal("decimal", i, a, z, k)
		}
	}
}

func pgOpclassInventory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	facts := pgOpclassFacts()
	q := "SELECT * FROM pg_catalog.pg_opclass"
	if b.PG {
		var predicates []string
		for _, r := range facts {
			predicates = append(predicates, fmt.Sprintf("(opcmethod=%d AND opcname='%s')", r[1], r[2]))
		}
		q += " WHERE " + strings.Join(predicates, " OR ")
	}
	q += " ORDER BY oid"
	for _, prepared := range []bool{false, true} {
		pgOpclassRows(t, p, b, b.DB, q, facts, prepared)
	}
	seenID, seenKey, defaults := map[any]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range facts {
		key := fmt.Sprintf("%v/%v/%v", r[1], r[2], r[3])
		if seenID[r[0]] || seenKey[key] {
			t.Fatal("duplicate expected identity", r)
		}
		seenID[r[0]], seenKey[key] = true, true
		if r[7].(bool) {
			key = fmt.Sprintf("%v/%v", r[1], r[6])
			if defaults[key] {
				t.Fatal("duplicate default", r)
			}
			defaults[key] = true
		}
	}
	if len(facts) != 130 {
		t.Fatal("inventory", len(facts))
	}
	for _, name := range []string{"array_ops", "record_ops", "range_ops", "multirange_ops", "bit_ops", "varbit_ops", "tsquery_ops", "tsvector_ops"} {
		q := "SELECT opcname FROM pg_catalog.pg_opclass WHERE opcname='" + name + "' AND opcmethod=403"
		want := [][]any{}
		if b.PG {
			want = [][]any{{name}}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q, []string{"opcname"}, []string{"NAME"}, want))
	}
	if !b.PG {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT opcname FROM pg_opclass WHERE opcmethod NOT IN (403,405,783,3580)", []string{"opcname"}, []string{"NAME"}, nil))
	}
}

func TestReferencePGOpclassInventory(t *testing.T) { pgOpclassScenario(t, pgOpclassInventory) }

func TestReferencePGOpclassShape(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"opclass_shape_reader"})
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_opclass WHERE oid=0", "SELECT * FROM pg_opclass WHERE FALSE"} {
			for _, prepared := range []bool{false, true} {
				pgOpclassRows(t, p, b, b.DB, q, nil, prepared)
			}
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid,reltype,relnatts,relkind,relhasindex FROM pg_class WHERE oid=2616", []string{"oid", "reltype", "relnatts", "relkind", "relhasindex"}, []string{"OID", "OID", "INT2", "CHAR", "BOOL"}, [][]any{{int64(2616), int64(10013), int64(9), "r", true}}))
		owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE ROLE opclass_shape_reader", 0))
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE opclass_shape_reader", 0))
		defer reader.ExecContext(p.Ctx, "RESET ROLE")
		for _, prepared := range []bool{false, true} {
			pgOpclassRows(t, p, b, reader, "SELECT * FROM pg_opclass WHERE oid=1978", [][]any{{int64(1978), int64(403), "int4_ops", int64(11), int64(10), int64(1976), int64(23), true, int64(0)}}, prepared)
		}
	})
}

func TestReferencePGOpclassMetadata(t *testing.T) {
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var want [][]any
		for i, name := range pgOpclassColumns {
			typ := int64(26)
			collation := int64(0)
			if i == 2 {
				typ = 19
				collation = 950
			}
			if i == 7 {
				typ = 16
			}
			want = append(want, []any{int64(i + 1), name, typ, true, collation, int64(-1)})
		}
		s := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,attcollation,atttypmod FROM pg_attribute WHERE attrelid=2616 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "attcollation", "atttypmod"}, []string{"INT2", "NAME", "OID", "BOOL", "OID", "INT4"}, want)
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		s = fixture.Select("SELECT o.opcname,o.opcintype,o.opcintype::regtype::text AS input_type,n.nspname FROM pg_opclass o JOIN pg_namespace n ON n.oid=o.opcnamespace WHERE o.oid=1978", []string{"opcname", "opcintype", "input_type", "nspname"}, []string{"NAME", "OID", "TEXT", "NAME"}, [][]any{{"int4_ops", int64(23), "integer", "pg_catalog"}})
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
	})
}
