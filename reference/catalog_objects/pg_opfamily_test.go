package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func pgOpfamilyMetadata(t *testing.T, rows *sql.Rows, pg, nullable bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, pgOpfamilyColumns) {
		t.Fatal("columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 5 {
		t.Fatal("column types", types, err)
	}
	for i, c := range types {
		scan := reflect.TypeFor[int64]()
		if i == 2 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != columns[i] || c.DatabaseTypeName() != pgOpfamilyTypes[i] || c.ScanType() != scan {
			t.Fatal("column descriptor", i, c.Name(), c.DatabaseTypeName(), c.ScanType())
		}
		if n, known := c.Nullable(); n != (!pg && nullable) || known == pg {
			t.Fatal("nullability", i, n, known)
		}
		if n, known := c.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("decimal size", i, precision, scale, known)
		}
	}
}

func pgOpfamilyConsume(t *testing.T, rows *sql.Rows) [][]any {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var oid, method, namespace, owner int64
		var name string
		if err := rows.Scan(&oid, &method, &name, &namespace, &owner); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{oid, method, name, namespace, owner})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func pgOpfamilyShapes(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	for _, name := range []string{"pg_catalog.pg_opfamily", "pg_opfamily"} {
		for _, prepared := range []bool{false, true} {
			pgOpfamilyRows(t, p, b, e, "SELECT * FROM "+name+" WHERE oid=1976", pgOpfamilyFacts()[:1], prepared)
			for _, predicate := range []string{"oid=0", "FALSE"} {
				pgOpfamilyRows(t, p, b, e, "SELECT * FROM "+name+" WHERE "+predicate, nil, prepared)
			}
		}
	}
}

func TestReferencePGOpfamilyShape(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"opfamily_shape_reader"})
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgOpfamilyShapes(t, p, b, b.DB)
		rows, err := b.DB.QueryContext(p.Ctx, pgOpfamilyProbe, int64(1976))
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		pgOpfamilyMetadata(t, rows, b.PG, false)
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if got := pgOpfamilyConsume(t, rows); !reflect.DeepEqual(got, pgOpfamilyFacts()[:1]) {
			t.Fatal("captured rows", got)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for i, c := range types {
			if c.Name() != pgOpfamilyColumns[i] || c.DatabaseTypeName() != pgOpfamilyTypes[i] {
				t.Fatal("retained metadata", i, c)
			}
		}
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE opfamily_shape_reader", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE opfamily_shape_reader", 0))
		defer c.ExecContext(context.Background(), "RESET ROLE")
		pgOpfamilyShapes(t, p, b, c)
	})
}

func pgOpfamilyIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		pgOpfamilyRows(t, p, b, b.DB, pgOpfamilyWitnessQuery, pgOpfamilyFacts(), prepared)
		s := fixture.Select("SELECT f.oid,a.amname,n.nspname,r.oid FROM pg_opfamily f JOIN pg_am a ON a.oid=f.opfmethod JOIN pg_namespace n ON n.oid=f.opfnamespace JOIN pg_roles r ON r.oid=f.opfowner WHERE f.oid IN (1976,1977,1994) ORDER BY f.oid", []string{"oid", "amname", "nspname", "oid"}, []string{"OID", "NAME", "NAME", "OID"}, [][]any{{int64(1976), "btree", "pg_catalog", int64(10)}, {int64(1977), "hash", "pg_catalog", int64(10)}, {int64(1994), "btree", "pg_catalog", int64(10)}})
		s.Prepared = prepared
		fixture.PgDescriptionRows(t, p, b, s)
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, pgOpfamilyProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgOpfamilyProbe}
	for _, route := range []struct {
		e        fixture.Executor
		prepared bool
	}{{b.DB, false}, {b.DB, true}, {held, false}} {
		for _, tc := range []struct {
			arg  any
			want [][]any
		}{{int64(1976), pgOpfamilyFacts()[:1]}, {int64(1977), pgOpfamilyFacts()[1:2]}, {nil, nil}, {int64(0), nil}, {int64(1994), pgOpfamilyFacts()[2:]}} {
			pgOpfamilyRows(t, p, b, route.e, pgOpfamilyProbe, tc.want, route.prepared, tc.arg)
		}
	}
	const query = "SELECT * FROM pg_catalog.pg_opfamily WHERE opfname=$1 AND opfnamespace=11 AND opfmethod=$2::oid"
	methodStmt, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer methodStmt.Close()
	methodHeld := fixture.HeldStatement{Executor: b.DB, Statement: methodStmt, SQL: query}
	for _, route := range []struct {
		e        fixture.Executor
		prepared bool
	}{{b.DB, false}, {b.DB, true}, {methodHeld, false}} {
		for _, tc := range []struct {
			name   string
			method any
			want   [][]any
		}{{"integer_ops", int64(403), pgOpfamilyFacts()[:1]}, {"integer_ops", int64(405), pgOpfamilyFacts()[1:2]}, {"integer_ops", nil, nil}, {"text_ops", int64(405), [][]any{{int64(1995), int64(405), "text_ops", int64(11), int64(10)}}}, {"integer_ops", int64(403), pgOpfamilyFacts()[:1]}} {
			pgOpfamilyRows(t, p, b, route.e, query, tc.want, route.prepared, tc.name, tc.method)
		}
	}
}

func TestReferencePGOpfamilyIdentity(t *testing.T) { pgOpclassScenario(t, pgOpfamilyIdentity) }

func pgOpfamilyMembership(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		var classes [][]any
		for _, method := range []int64{403, 405} {
			family := int64(1976)
			if method == 405 {
				family = 1977
			}
			for _, tc := range []struct {
				name string
				typ  int64
			}{{"int8_ops", 20}, {"int2_ops", 21}, {"int4_ops", 23}} {
				classes = append(classes, []any{method, tc.name, tc.typ, family, method})
			}
		}
		steps := []fixture.Step{
			fixture.Select("SELECT c.opcmethod,c.opcname,c.opcintype,c.opcfamily,f.opfmethod FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE c.opcnamespace=11 AND c.opcmethod IN (403,405) AND c.opcname IN ('int2_ops','int4_ops','int8_ops') ORDER BY c.opcmethod,c.opcintype", []string{"opcmethod", "opcname", "opcintype", "opcfamily", "opfmethod"}, []string{"OID", "NAME", "OID", "OID", "OID"}, classes),
			fixture.Select("SELECT a.amopfamily,a.amopstrategy,a.amopopr,a.amoppurpose,a.amopmethod,a.amopsortfamily FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE a.amopfamily IN (1976,1977) AND a.amoplefttype=23 AND a.amoprighttype=23 ORDER BY a.amopfamily,a.amopstrategy", []string{"amopfamily", "amopstrategy", "amopopr", "amoppurpose", "amopmethod", "amopsortfamily"}, []string{"OID", "INT2", "OID", "CHAR", "OID", "OID"}, [][]any{{int64(1976), int64(1), int64(97), "s", int64(403), int64(0)}, {int64(1976), int64(2), int64(523), "s", int64(403), int64(0)}, {int64(1976), int64(3), int64(96), "s", int64(403), int64(0)}, {int64(1976), int64(4), int64(525), "s", int64(403), int64(0)}, {int64(1976), int64(5), int64(521), "s", int64(403), int64(0)}, {int64(1977), int64(1), int64(96), "s", int64(405), int64(0)}}),
			fixture.Select("SELECT a.amopfamily,a.amoplefttype,a.amoprighttype,a.amopstrategy,a.amopopr,a.amopmethod,o.oprleft,o.oprright FROM pg_amop a JOIN pg_operator o ON o.oid=a.amopopr WHERE a.oid=10010", []string{"amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amopopr", "amopmethod", "oprleft", "oprright"}, []string{"OID", "OID", "OID", "INT2", "OID", "OID", "OID", "OID"}, [][]any{{int64(1976), int64(21), int64(20), int64(1), int64(1864), int64(403), int64(21), int64(20)}}),
			fixture.Select("SELECT a.amprocfamily,a.amproclefttype,a.amprocrighttype,a.amprocnum,p.oid,p.proname FROM pg_amproc a JOIN pg_proc p ON p.oid=a.amproc::oid JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE (a.amprocfamily=1976 AND ((a.amproclefttype=21 AND a.amprocrighttype=20) OR (a.amproclefttype=23 AND a.amprocrighttype=23)) AND a.amprocnum=1) OR (a.amprocfamily=1977 AND a.amproclefttype=23 AND a.amprocrighttype=23 AND a.amprocnum IN (1,2)) ORDER BY a.amprocfamily,a.amproclefttype,a.amprocrighttype,a.amprocnum", []string{"amprocfamily", "amproclefttype", "amprocrighttype", "amprocnum", "oid", "proname"}, []string{"OID", "OID", "OID", "INT2", "OID", "NAME"}, [][]any{{int64(1976), int64(21), int64(20), int64(1), int64(2192), "btint28cmp"}, {int64(1976), int64(23), int64(23), int64(1), int64(351), "btint4cmp"}, {int64(1977), int64(23), int64(23), int64(1), int64(450), "hashint4"}, {int64(1977), int64(23), int64(23), int64(2), int64(425), "hashint4extended"}}),
			fixture.Select("SELECT a.amopfamily,a.amopmethod,a.amopopr,a.amopsortfamily,f.opfmethod FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopsortfamily WHERE a.oid=10319", []string{"amopfamily", "amopmethod", "amopopr", "amopsortfamily", "opfmethod"}, []string{"OID", "OID", "OID", "OID", "OID"}, [][]any{{int64(1029), int64(783), int64(517), int64(1970), int64(403)}}),
		}
		for _, s := range steps {
			s.Prepared = prepared
			fixture.PgDescriptionRows(t, p, b, s)
		}
	}
}

func TestReferencePGOpfamilyMembership(t *testing.T) {
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgOpfamilyMembership(t, p, b)
		pgOpfamilyIndexLifecycle(t, p, b)
	})
}
