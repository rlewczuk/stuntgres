package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgTypeColumns = []string{"oid", "typname", "typnamespace", "typowner", "typlen", "typbyval", "typtype", "typcategory", "typispreferred", "typisdefined", "typdelim", "typrelid", "typsubscript", "typelem", "typarray", "typinput", "typoutput", "typreceive", "typsend", "typmodin", "typmodout", "typanalyze", "typalign", "typstorage", "typnotnull", "typbasetype", "typtypmod", "typndims", "typcollation", "typdefaultbin", "typdefault", "typacl"}
var pgTypeTypes = []string{"OID", "NAME", "OID", "OID", "INT2", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "CHAR", "OID", "REGPROC", "OID", "OID", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "CHAR", "CHAR", "BOOL", "OID", "INT4", "INT4", "OID", "PG_NODE_TREE", "TEXT", "_ACLITEM"}
var pgTypeRoutineColumns = []int{12, 15, 16, 17, 18, 19, 20, 21}

const pgTypeNumericProjection = "oid,typname,typnamespace,typowner,typlen,typbyval,typtype,typcategory,typispreferred,typisdefined,typdelim,typrelid,typsubscript::oid AS typsubscript,typelem,typarray,typinput::oid AS typinput,typoutput::oid AS typoutput,typreceive::oid AS typreceive,typsend::oid AS typsend,typmodin::oid AS typmodin,typmodout::oid AS typmodout,typanalyze::oid AS typanalyze,typalign,typstorage,typnotnull,typbasetype,typtypmod,typndims,typcollation,typdefaultbin,typdefault,typacl"

func pgTypeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}
func pgTypeStep(b fixture.Backend, q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, pgTypeColumns, pgTypeTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		c, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pgTypeSavedMetadata(t, c, b.PG)
	}
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if v == nil {
			return true
		}
		expected := reflect.TypeFor[string]()
		switch pgTypeTypes[col] {
		case "OID":
			expected = reflect.TypeFor[int64]()
			if b.PG {
				expected = reflect.TypeFor[[]byte]()
			}
		case "INT2", "INT4":
			expected = reflect.TypeFor[int64]()
		case "BOOL":
			expected = reflect.TypeFor[bool]()
		case "NAME", "REGPROC", "PG_NODE_TREE", "_ACLITEM":
			if b.PG {
				expected = reflect.TypeFor[[]byte]()
			}
		}
		if reflect.TypeOf(v) != expected {
			t.Errorf("raw row=%d column=%d/%s dbtype=%s got=%T/%#v want=%v", row, col, pgTypeColumns[col], pgTypeTypes[col], v, v, expected)
		}
		return true
	}
	return s
}
func TestReferencePGTypeShape(t *testing.T) {
	pgTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		t.Run("cursors", func(t *testing.T) {
			q := "SELECT * FROM pg_catalog.pg_type WHERE oid=$1::oid"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for _, arg := range []any{int64(0), nil, int64(23)} {
				var want [][]any
				if arg == int64(23) {
					for _, r := range pgTypeBuiltinFacts {
						if r[0] == arg {
							want = [][]any{pgTypeRawFacts(r)}
						}
					}
				}
				s := pgTypeStep(b, q, want, arg)
				fixture.PgDescriptionRows(t, p, b, s)
				fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
				fixture.PgDescriptionExecutorRows(t, p, b, held, s)
			}
			fixture.PgDescriptionRows(t, p, b, pgTypeStep(b, "SELECT * FROM pg_type WHERE FALSE", nil))
			rows, err := b.DB.QueryContext(p.Ctx, q, int64(23))
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			saved, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			pgTypeSavedMetadata(t, saved, b.PG)
		})
		t.Run("relation", func(t *testing.T) {
			steps := []fixture.Step{
				fixture.Select("SELECT oid,relnamespace,reltype,relnatts,relkind,relhasindex FROM pg_class WHERE oid=1247", []string{"oid", "relnamespace", "reltype", "relnatts", "relkind", "relhasindex"}, []string{"OID", "OID", "OID", "INT2", "CHAR", "BOOL"}, [][]any{{int64(1247), int64(11), int64(71), int64(32), "r", true}}),
				fixture.Select("SELECT oid,typname,typrelid,typelem,typarray FROM pg_type WHERE oid IN (71,210) ORDER BY oid", []string{"oid", "typname", "typrelid", "typelem", "typarray"}, []string{"OID", "NAME", "OID", "OID", "OID"}, [][]any{{int64(71), "pg_type", int64(1247), int64(0), int64(210)}, {int64(210), "_pg_type", int64(0), int64(71), int64(0)}}),
				fixture.Select("SELECT * FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_type'", pgTableColumns, pgTableTypes, [][]any{pgTableRow("pg_catalog", "pg_type", pgTableViewsOwner(b), true)}),
			}
			for _, s := range steps {
				fixture.PgDescriptionRows(t, p, b, s)
				fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
			}
		})
		t.Run("attributes", func(t *testing.T) {
			ids := []int64{26, 19, 26, 26, 21, 16, 18, 18, 16, 16, 18, 26, 24, 26, 26, 24, 24, 24, 24, 24, 24, 24, 18, 18, 16, 26, 23, 23, 26, 194, 25, 1034}
			var want [][]any
			for i, name := range pgTypeColumns {
				collation := int64(0)
				if i == 1 || i == 29 || i == 30 {
					collation = 950
				}
				// pg_type.h declares aclitem typacl[1]; other attributes are scalar.
				dimensions := int64(0)
				if i == 31 {
					dimensions = 1
				}
				want = append(want, []any{int64(i + 1), name, ids[i], i < 29, int64(-1), collation, dimensions})
			}
			s := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid=1247 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, want)
			fixture.PgDescriptionRows(t, p, b, s)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		})
	})
}
