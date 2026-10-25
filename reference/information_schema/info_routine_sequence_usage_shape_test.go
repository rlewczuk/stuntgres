package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutineSequenceUsageShape(t *testing.T) {
	rsuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		empty := rsuProjection(b.PG, "routine_name='rsu_shape_r'", nil)
		run(empty)
		run(fixture.Prepared(empty))
		var attributes [][]any
		for i, name := range rsuColumns {
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.routine_sequence_usage'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.routine_sequence_usage'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		run(fixture.Exec("CREATE SEQUENCE rsu_shape_s", 0))
		run(fixture.Exec("CREATE FUNCTION rsu_shape_r() RETURNS bigint RETURN nextval('public.rsu_shape_s')", 0))
		id := rsuIdentity(t, p, c, "public", "rsu_shape_r", "")
		s := rsuProjection(b.PG, "routine_name='rsu_shape_r'", [][]any{rsuRow(database, "public", "rsu_shape_r", id, "public", "rsu_shape_s")})
		run(s)
		run(fixture.Prepared(s))
		for _, step := range []fixture.Step{s, rsuProjection(b.PG, "FALSE", nil)} {
			for _, prepared := range []bool{false, true} {
				rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, step.Query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer func() {
						if err := closeQuery(); err != nil {
							t.Error(err)
						}
					}()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					rsuMetadata(t, columns, b.PG)
					count := 0
					for rows.Next() {
						values := make([]any, 9)
						dest := make([]any, 9)
						for i := range values {
							dest[i] = &values[i]
						}
						if err := rows.Scan(dest...); err != nil {
							t.Fatal(err)
						}
						for i, v := range values {
							step.CheckRaw(t, i, count, v)
						}
						count++
					}
					if err := rows.Err(); err != nil || count != len(step.Rows) {
						t.Fatalf("rows=%d error=%v", count, err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					rsuMetadata(t, columns, b.PG)
				}()
			}
		}
	})
}

func TestReferenceInfoRoutineSequenceUsageDiagnostics(t *testing.T) {
	rsuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, name := range []string{"missing", "schema_catalog"} {
			d := fixture.ControlsReferenceDiagnostic("42703", `column "`+name+`" does not exist`)
			d.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT "+name+" FROM information_schema.routine_sequence_usage", d)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		run(fixture.Exec("CREATE SEQUENCE rsu_diag_s", 0))
		run(fixture.Exec("CREATE FUNCTION rsu_diag_r() RETURNS bigint RETURN nextval('public.rsu_diag_s')", 0))
		id := rsuIdentity(t, p, c, "public", "rsu_diag_r", "")
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop sequence rsu_diag_s because other objects depend on it")
		d.Detail = "function rsu_diag_r() depends on sequence rsu_diag_s"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("DROP SEQUENCE rsu_diag_s RESTRICT", d))
		run(rsuProjection(b.PG, "routine_name='rsu_diag_r'", [][]any{rsuRow(database, "public", "rsu_diag_r", id, "public", "rsu_diag_s")}))
	})
}
