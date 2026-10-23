package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewsShape(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE iv_shape_base(id integer,note text)", 0))
		run(fixture.Exec("CREATE VIEW iv_shape AS SELECT id,note FROM iv_shape_base", 0))
		want := [][]any{ivRow(database, "public", "iv_shape", " SELECT id,\n    note\n   FROM iv_shape_base;", "NONE", "YES", "YES")}
		held := ivHeld(t, p, b, c, ivSelect)
		s := ivStep(b.PG, ivSelect, want, "public", "iv_shape")
		run(s)
		run(fixture.Prepared(s))
		held(s)
		q := "SELECT table_catalog,table_schema,table_name,view_definition,check_option,is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema=$1 AND table_name=$2"
		run(ivStep(b.PG, q, want, "public", "iv_shape"))
		run(fixture.Prepared(ivStep(b.PG, q, want, "public", "iv_shape")))
		for _, args := range [][]any{{"public", "iv_absent"}, {"iv_absent", "iv_shape"}, {"public", nil}} {
			s := ivStep(b.PG, ivSelect, nil, args...)
			run(s)
			run(fixture.Prepared(s))
			held(s)
		}
		for _, where := range []string{"false", "table_schema='public' AND table_name IS NULL"} {
			s := ivStep(b.PG, "SELECT * FROM information_schema.views WHERE "+where, nil)
			run(s)
			run(fixture.Prepared(s))
			ivHeld(t, p, b, c, s.Query)(s)
		}
		q = "SELECT v.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.views v ON v.table_schema='public' AND v.table_name='iv_absent'"
		// Outer extension makes every output nullable, independent of base rows.
		s = ivStep(b.PG, q, [][]any{make([]any, 10)})
		run(s)
		run(fixture.Prepared(s))
		q = `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='views' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		logical := make([][]any, 10)
		for i, name := range ivColumns {
			domain, base, oid, modifier := "sql_identifier", "name", int64(19), int64(-1)
			if i >= 3 {
				domain, base, oid = "character_data", "varchar", 1043
			}
			if i >= 5 {
				domain, modifier = "yes_or_no", 7
			}
			logical[i] = []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, int64(950), int64(-1), int64(0), false, false, modifier}
		}
		s = fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
		run(s)
		run(fixture.Prepared(s))
		q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='views' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
		run(s)
		run(fixture.Prepared(s))
	})
}

func TestReferenceInfoViewsDiagnostics(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT missing FROM information_schema.views", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"})
			s.SelectRows, s.ErrorStage = true, "execute"
			if prepared {
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
