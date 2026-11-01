package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewRoutineUsageShape(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		var attributes [][]any
		for i, name := range vruColumns {
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.view_routine_usage'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.view_routine_usage'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		vruSeed(run)
		run(fixture.Exec("CREATE VIEW vru_scope.constant_v AS SELECT 1 AS x", 0))
		run(fixture.Exec("CREATE VIEW vru_scope.null_v AS SELECT NULL::integer AS x", 0))
		run(fixture.Exec("CREATE VIEW vru_scope.null_call_v AS SELECT vru_scope.null_f() AS x", 0))
		want := append(vruSeedRows(t, p, c, database), vruRow(database, "vru_scope", "null_call_v", "vru_scope", rsuIdentity(t, p, c, "vru_scope", "null_f", "")))
		for _, s := range []fixture.Step{
			vruProjection(b.PG, "table_schema='vru_scope'", want),
			vruProjection(b.PG, "table_schema='vru_scope' AND FALSE", nil),
			vruProjection(b.PG, "table_schema='vru_scope' AND specific_name IS NULL", nil),
			vruProjection(b.PG, "table_schema='vru_scope' AND table_name='absent'", nil),
			vruProjection(b.PG, "table_schema='vru_scope' AND table_name IN ('constant_v','null_v','empty_v')", nil),
		} {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				run(s)
				named := s
				named.Query = strings.Replace(s.Query, "SELECT *", "SELECT "+strings.Join(vruColumns, ","), 1)
				run(named)
				rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, s.Query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil {
					_ = closeQuery()
					t.Fatal(err)
				}
				vruMetadata(t, columns, b.PG)
				var got [][]any
				for rows.Next() {
					var cells [6]any
					if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5]); err != nil {
						_ = closeQuery()
						t.Fatal(err)
					}
					for i, cell := range cells {
						s.CheckRaw(t, i, len(got), cell)
						cells[i] = fixture.Normalize(cell)
					}
					got = append(got, append([]any(nil), cells[:]...))
				}
				if err := rows.Err(); err != nil {
					_ = closeQuery()
					t.Fatal(err)
				}
				vruCheckBag(t, got, s.Rows)
				vruMetadata(t, columns, b.PG)
				if err := closeQuery(); err != nil {
					t.Fatal(err)
				}
				vruMetadata(t, columns, b.PG)
			}
		}
		nullStep := vruProjection(b.PG, "table_schema='vru_scope' AND specific_name=$1", nil)
		nullStep.Args = []any{nil}
		run(nullStep)
		run(fixture.Prepared(nullStep))
		held := vruHeld(t, p, c, r, vruProjection(b.PG, "table_schema='vru_scope'", want).Query)
		held(vruProjection(b.PG, "table_schema='vru_scope'", want))
		var subsetRows [][]any
		for _, row := range want {
			subsetRows = append(subsetRows, []any{row[5], row[2], row[4]})
		}
		subset := fixture.Select("SELECT specific_name,table_name,specific_schema FROM information_schema.view_routine_usage WHERE table_schema='vru_scope'", []string{"specific_name", "table_name", "specific_schema"}, []string{"NAME", "NAME", "NAME"}, subsetRows)
		subset.Bag = true
		subset.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			vruMetadataNames(t, columns, subset.Columns, b.PG)
		}
		run(subset)
		run(fixture.Prepared(subset))
	})
}

func TestReferenceInfoViewRoutineUsageSourceACL(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vruSeed(run)
		run(fixture.Exec("CREATE ROLE vru_reader", 0))
		run(fixture.Exec("ALTER FUNCTION vru_scope.f() OWNER TO vru_reader", 0))
		where := "table_schema='vru_scope'"
		want := vruSeedRows(t, p, c, database)
		held := vruHeld(t, p, c, r, vruProjection(b.PG, where, want).Query)
		heldFalse := vruHeld(t, p, c, r, vruProjection(b.PG, where+" AND FALSE", nil).Query)
		run(fixture.Exec("REVOKE SELECT ON information_schema.view_routine_usage FROM PUBLIC", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vru_reader", 0))
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for view view_routine_usage")
		for _, predicate := range []string{where, where + " AND FALSE"} {
			s := fixture.ErrorDiagnostic(vruProjection(b.PG, predicate, nil).Query, d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			if predicate == where {
				held(s)
			} else {
				heldFalse(s)
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON information_schema.view_routine_usage TO PUBLIC", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vru_reader", 0))
		s := vruProjection(b.PG, where, want)
		run(s)
		run(fixture.Prepared(s))
		held(s)
		heldFalse(vruProjection(b.PG, where+" AND FALSE", nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
