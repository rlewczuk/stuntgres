package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var pgAttributeColumns = []string{"attrelid", "attname", "atttypid", "attlen", "attnum", "atttypmod", "attndims", "attbyval", "attalign", "attstorage", "attcompression", "attnotnull", "atthasdef", "atthasmissing", "attidentity", "attgenerated", "attisdropped", "attislocal", "attinhcount", "attcollation", "attstattarget", "attacl", "attoptions", "attfdwoptions", "attmissingval"}
var pgAttributeTypes = []string{"OID", "NAME", "OID", "INT2", "INT2", "INT4", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "INT2", "OID", "INT2", "_ACLITEM", "_TEXT", "_TEXT", "ANYARRAY"}

func pgAttributeScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

func pgAttributeID(t *testing.T, p *fixture.Pair, db fixture.Executor, relation string) int64 {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, "SELECT '"+relation+"'::regclass::oid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id int64
	if !rows.Next() {
		t.Fatal("missing relation identity", rows.Err())
	}
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("relation identity multiplicity", rows.Err())
	}
	if id <= 0 || id > 4294967295 {
		t.Fatal("invalid relation identity", id)
	}
	return id
}

// Each scalar supplied here is an independent release expectation. The helper
// only fills the documented fresh-column defaults, never reads native metadata.
func pgAttributeRow(rel int64, name string, oid, length, number, typmod, rank int64, byval bool, align, storage string, nn bool, collation int64) []any {
	return []any{rel, name, oid, length, number, typmod, rank, byval, align, storage, "", nn, false, false, "", "", false, true, int64(0), collation, nil, nil, nil, nil, nil}
}

func pgAttributeSystemRows(rel int64) [][]any {
	return [][]any{
		pgAttributeRow(rel, "tableoid", 26, 4, -6, -1, 0, true, "i", "p", true, 0),
		pgAttributeRow(rel, "cmax", 29, 4, -5, -1, 0, true, "i", "p", true, 0),
		pgAttributeRow(rel, "xmax", 28, 4, -4, -1, 0, true, "i", "p", true, 0),
		pgAttributeRow(rel, "cmin", 29, 4, -3, -1, 0, true, "i", "p", true, 0),
		pgAttributeRow(rel, "xmin", 28, 4, -2, -1, 0, true, "i", "p", true, 0),
		pgAttributeRow(rel, "ctid", 27, 6, -1, -1, 0, false, "s", "p", true, 0),
	}
}

func pgAttributeCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	want := make([][]any, len(s.Rows))
	for i, row := range s.Rows {
		want[i] = append([]any(nil), row...)
		for j, v := range row {
			if b.PG && s.Types[j] == "OID" && v != nil {
				want[i][j] = strconv.FormatInt(v.(int64), 10)
			}
		}
	}
	s.Rows = want
	fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
}

func pgAttributeRead(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, id int64, predicate string, want [][]any) {
	t.Helper()
	q := "SELECT * FROM pg_catalog.pg_attribute WHERE attrelid=$1" + predicate + " ORDER BY attnum"
	s := fixture.Select(q, pgAttributeColumns, pgAttributeTypes, want, id)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgAttributeMetadata(t, rows, b.PG) }
	pgAttributeCheck(t, p, b, db, s)
	pgAttributeCheck(t, p, b, db, fixture.Prepared(s))
}

func pgAttributeMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, pgAttributeColumns) {
		t.Fatal("25 labels", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 25 {
		t.Fatal("25 descriptors", types, err)
	}
	for i, typ := range types {
		if typ.Name() != pgAttributeColumns[i] || typ.DatabaseTypeName() != pgAttributeTypes[i] {
			t.Fatalf("attribute %d descriptor %s/%s want %s/%s", i+1, typ.Name(), typ.DatabaseTypeName(), pgAttributeColumns[i], pgAttributeTypes[i])
		}
		if nullable, known := typ.Nullable(); (!pg && (!known || nullable != (i >= 20))) || (pg && known) {
			t.Fatal("nullability", i, nullable, known)
		}
		scan := reflect.TypeFor[string]()
		switch pgAttributeTypes[i] {
		case "OID", "INT2", "INT4":
			scan = reflect.TypeFor[int64]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "ANYARRAY":
			scan = reflect.TypeFor[any]()
		}
		if pg {
			switch pgAttributeTypes[i] {
			case "INT2":
				scan = reflect.TypeFor[int16]()
			case "INT4":
				scan = reflect.TypeFor[int32]()
			case "BOOL":
			default:
				scan = reflect.TypeFor[any]()
			}
		}
		if typ.ScanType() != scan {
			t.Fatal("ScanType", i, typ.ScanType(), scan)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if n, scale, known := typ.DecimalSize(); n != 0 || scale != 0 || known {
			t.Fatal("decimal", i, n, scale, known)
		}
	}
	if !pg {
		names[0] = "corrupted"
		again, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(again, pgAttributeColumns) {
			t.Fatal("owned labels", again, err)
		}
	}
}

// Preserve the original released NAME-to-TEXT inquiry and its exact native cast
// exclusion, then independently exercise metadata without that adjacent codec.
func pgAttributeNameCastWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, original fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		step := original
		step.Prepared = prepared
		if !b.PG {
			step = fixture.ImplicitError(original.Query, fixture.Diagnostic{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: fixture.Position(strings.Index(original.Query, "::text") + 1)})
			step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
			if prepared {
				step.ErrorStage = "prepare"
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
	}
	raw := original
	raw.Query = strings.Replace(original.Query, "attname::text AS name", "attname AS name", 1)
	raw.Query = strings.Replace(raw.Query, "::regclass AND", "::regclass::oid AND", 1)
	raw.Types = append([]string(nil), original.Types...)
	raw.Types[0] = "NAME"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, raw)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(raw))
}

func TestReferencePGAttributeShape(t *testing.T) {
	// Independent matrices own their fixture before either backend starts.
	for i, q := range []string{"SELECT * FROM pg_catalog.pg_attribute WHERE FALSE", "SELECT * FROM pg_attribute WHERE attrelid=4294967295::oid"} {
		t.Run("empty-"+strconv.Itoa(i+1), func(t *testing.T) {
			pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				s := fixture.Select(q, pgAttributeColumns, pgAttributeTypes, nil)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgAttributeMetadata(t, rows, b.PG) }
				for _, plain := range []bool{false, true} {
					s.NonContext = plain
					pgAttributeCheck(t, p, b, b.DB, s)
					pgAttributeCheck(t, p, b, b.DB, fixture.Prepared(s))
				}
				s.NonContext = false
				held := fixture.ViewHold(t, p, b, q)
				pgAttributeCheck(t, p, b, held, s)
				pgAttributeCheck(t, p, b, held, s)
			})
		})
	}
	tableCase := func(name string, body func(*testing.T, *fixture.Pair, fixture.Backend, int64)) {
		t.Run(name, func(t *testing.T) {
			pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				pgClassExec(t, p, b, b.DB, "CREATE TABLE public.pa18_shape(a integer)")
				id := pgAttributeID(t, p, b.DB, "public.pa18_shape")
				body(t, p, b, id)
			})
		})
	}
	tableCase("populated", func(t *testing.T, p *fixture.Pair, b fixture.Backend, id int64) {
		pgAttributeRead(t, p, b, b.DB, id, " AND attnum>0", [][]any{pgAttributeRow(id, "a", 23, 4, 1, -1, 0, true, "i", "p", false, 0)})
	})
	// Filter each catalog input before joining: this witnesses null extension of
	// the same relation's absent attribute, not a database-wide attribute product.
	for i, q := range []string{
		"SELECT attmissingval AS missing FROM pg_attribute WHERE attrelid=$1 AND attnum>0",
		"SELECT missing FROM (SELECT attmissingval AS missing FROM pg_attribute WHERE attrelid=$1 AND attnum>0) s",
		"SELECT (SELECT attmissingval FROM pg_attribute WHERE attrelid=$1 AND attnum=0) AS missing",
		"SELECT absent.attmissingval AS missing FROM (SELECT attrelid FROM pg_catalog.pg_attribute WHERE attrelid=$1 AND attnum=1) present LEFT JOIN (SELECT attrelid,attnum,attmissingval FROM pg_catalog.pg_attribute WHERE attrelid=$1 AND attnum=0) absent ON absent.attrelid=present.attrelid AND absent.attnum=0",
	} {
		tableCase("anyarray-"+strconv.Itoa(i+1), func(t *testing.T, p *fixture.Pair, b fixture.Backend, id int64) {
			s := fixture.Select(q, []string{"missing"}, []string{"ANYARRAY"}, [][]any{{nil}}, id)
			pgAttributeCheck(t, p, b, b.DB, s)
			pgAttributeCheck(t, p, b, b.DB, fixture.Prepared(s))
			held := fixture.ViewHold(t, p, b, q)
			pgAttributeCheck(t, p, b, held, s)
		})
	}
	tableCase("null-aggregates", func(t *testing.T, p *fixture.Pair, b fixture.Backend, id int64) {
		pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT COUNT(attmissingval) AS present FROM pg_attribute WHERE attrelid=$1", []string{"present"}, []string{"INT8"}, [][]any{{int64(0)}}, id))
		pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT attmissingval IS NULL AS absent FROM pg_attribute WHERE attrelid=$1 AND attnum=1", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}, id))
	})
	tableCase("rejected-ctas", func(t *testing.T, p *fixture.Pair, b fixture.Backend, id int64) {
		rejected := fixture.ImplicitError("CREATE TABLE pa18_shape_missing_copy AS SELECT attmissingval AS a FROM pg_attribute WHERE attrelid=$1 AND attnum=1", fixture.Diagnostic{Code: "42P16", Message: "column \"a\" has pseudo-type anyarray"}, id)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, rejected)
		pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT relname FROM pg_class WHERE relname='pa18_shape_missing_copy'", []string{"relname"}, []string{"NAME"}, nil))
	})
	t.Run("builtin-identities", func(t *testing.T) {
		pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT oid,reltype FROM pg_class WHERE oid=1249", []string{"oid", "reltype"}, []string{"OID", "OID"}, [][]any{{int64(1249), int64(75)}}))
			pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT 'pg_catalog.pg_attribute[]'::regtype::oid AS array_type", []string{"array_type"}, []string{"OID"}, [][]any{{int64(270)}}))
		})
	})
}

func TestReferencePGAttributeIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pa18_identity_a")
	fixture.SchemaReferenceCleanup(t, "pa18_identity_b")
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE SCHEMA pa18_identity_a", "CREATE SCHEMA pa18_identity_b", "CREATE TABLE pa18_identity_a.pa18_same(\"Mixed Label\" integer NOT NULL)", "CREATE TABLE pa18_identity_b.pa18_same(\"Mixed Label\" text)")
		a := pgAttributeID(t, p, b.DB, "pa18_identity_a.pa18_same")
		z := pgAttributeID(t, p, b.DB, "pa18_identity_b.pa18_same")
		if a == z {
			t.Fatal("distinct schema relations share an identity")
		}
		pgAttributeRead(t, p, b, b.DB, a, " AND attnum>0", [][]any{pgAttributeRow(a, "Mixed Label", 23, 4, 1, -1, 0, true, "i", "p", true, 0)})
		pgAttributeRead(t, p, b, b.DB, z, " AND attnum>0", [][]any{pgAttributeRow(z, "Mixed Label", 25, -1, 1, -1, 0, false, "i", "x", false, 100)})
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, owner, "CREATE TABLE public.pg_attribute(shadow integer)", "SET search_path TO public,pg_catalog")
		pgAttributeCheck(t, p, b, owner, fixture.Select("SELECT * FROM pg_attribute", []string{"shadow"}, []string{"INT4"}, nil))
		pgAttributeRead(t, p, b, owner, a, " AND attnum>0", [][]any{pgAttributeRow(a, "Mixed Label", 23, 4, 1, -1, 0, true, "i", "p", true, 0)})
	})
}

func TestReferencePGAttributeBuiltins(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Condensed catalogs retain their existing represented-field coverage.
		for _, tc := range []struct {
			rel   string
			names []string
			rows  [][]any
		}{
			{"pg_tablespace", []string{"oid", "spcname", "spcowner", "spcacl", "spcoptions"}, [][]any{{"oid", int64(1), int64(26), true, int64(0)}, {"spcname", int64(2), int64(19), true, int64(950)}, {"spcowner", int64(3), int64(26), true, int64(0)}, {"spcacl", int64(4), int64(1034), false, int64(0)}, {"spcoptions", int64(5), int64(1009), false, int64(950)}}},
			{"pg_proc", []string{"oid", "proname", "pronamespace", "proowner", "pronargs", "prorettype", "provolatile", "proisstrict", "proparallel"}, [][]any{{"oid", int64(1), int64(26), true, int64(0)}, {"proname", int64(2), int64(19), true, int64(950)}, {"pronamespace", int64(3), int64(26), true, int64(0)}, {"proowner", int64(4), int64(26), true, int64(0)}, {"proisstrict", int64(13), int64(16), true, int64(0)}, {"provolatile", int64(15), int64(18), true, int64(0)}, {"proparallel", int64(16), int64(18), true, int64(0)}, {"pronargs", int64(17), int64(21), true, int64(0)}, {"prorettype", int64(19), int64(26), true, int64(0)}}},
			{"pg_attrdef", []string{"adbin"}, [][]any{{"adbin", int64(4), int64(194), true, int64(950)}}},
			{"pg_constraint", []string{"conbin"}, [][]any{{"conbin", int64(28), int64(194), false, int64(950)}}},
			{"pg_class", []string{"relpartbound"}, [][]any{{"relpartbound", int64(34), int64(194), false, int64(950)}}},
		} {
			q := "SELECT attname,attnum,atttypid,attnotnull,attcollation FROM pg_attribute WHERE attrelid='pg_catalog." + tc.rel + "'::regclass::oid AND attname IN ('" + strings.Join(tc.names, "','") + "') ORDER BY attnum"
			pgAttributeCheck(t, p, b, b.DB, fixture.Select(q, []string{"attname", "attnum", "atttypid", "attnotnull", "attcollation"}, []string{"NAME", "INT2", "OID", "BOOL", "OID"}, tc.rows))
		}
		want := make([][]any, 25)
		for i, name := range pgAttributeColumns {
			want[i] = []any{name, int64(i + 1), i < 20}
		}
		pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT attname,attnum,attnotnull FROM pg_attribute WHERE attrelid=1249 AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "attnotnull"}, []string{"NAME", "INT2", "BOOL"}, want))
	})
}
