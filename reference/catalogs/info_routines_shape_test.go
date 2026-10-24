package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinesShape(t *testing.T) {
	irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE FUNCTION ir_shape() RETURNS integer LANGUAGE SQL AS ' SELECT 1 '", 0))
		id := rsuIdentity(t, p, c, "public", "ir_shape", "")
		want := [][]any{irRow(db, "public", "ir_shape", id, "FUNCTION", "integer", "pg_catalog", "int4", "SQL", " SELECT 1 ", nil, false, false)}
		for _, s := range []fixture.Step{irProjection(b.PG, "routine_schema='public' AND routine_name='ir_shape'", want), irProjection(b.PG, "routine_name='ir_absent'", nil), irProjection(b.PG, "FALSE", nil), irProjection(b.PG, "NULL", nil)} {
			irCheck(t, p, b, c, s)
			rows, err := c.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			irMetadata(t, types, b.PG)
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			irMetadata(t, types, b.PG)
		}
		var attributes [][]any
		for i, name := range irColumns {
			base, collation, typmod := int64(19), int64(950), int64(-1)
			switch irDomains[i] {
			case "character_data":
				base = 1043
			case "yes_or_no":
				base, typmod = 1043, 7
			case "cardinal_number":
				base, collation = 23, 0
			case "time_stamp":
				base, collation, typmod = 1184, 0, 2
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", irDomains[i], "d", base, int64(-1), false, collation, int64(0), false, typmod})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,t.typtypmod FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.routines'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL", "INT4"}, attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.routines'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		s := fixture.Select("SELECT r.* FROM (SELECT 1 AS n) x LEFT JOIN information_schema.routines r ON r.routine_name='ir_absent'", irColumns, irTypes, [][]any{make([]any, 82)})
		irCheck(t, p, b, c, s)
	})
}

func TestReferenceInfoRoutinesDiagnostics(t *testing.T) {
	irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
		for _, name := range []string{"missing", "identity_arguments"} {
			d := fixture.ControlsReferenceDiagnostic("42703", `column "`+name+`" does not exist`)
			d.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT "+name+" FROM information_schema.routines", d)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		irCheck(t, p, b, c, irProjection(b.PG, "routine_name='ir_absent'", nil))
	})
}
