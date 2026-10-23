package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewTableUsageShape(t *testing.T) {
	vtuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		var attributes [][]any
		for i, name := range vtuColumns {
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.view_table_usage'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.view_table_usage'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		vtuSeed(run)
		run(fixture.Exec("CREATE VIEW vtu_scope.constant_v AS SELECT 1 AS id", 0))
		run(fixture.Exec("CREATE VIEW vtu_scope.null_v AS SELECT NULL::integer AS id", 0))
		for _, s := range []fixture.Step{
			vtuProjection(b.PG, "view_schema='vtu_scope'", vtuSeedRows(database)),
			vtuProjection(b.PG, "view_schema='vtu_scope' AND FALSE", nil),
			vtuProjection(b.PG, "view_schema='vtu_scope' AND table_name IS NULL", nil),
			vtuProjection(b.PG, "view_schema='vtu_scope' AND view_name='absent'", nil),
			vtuProjection(b.PG, "view_schema='vtu_scope' AND view_name IN ('constant_v','null_v')", nil),
		} {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				run(s)
				named := s
				named.Query = strings.Replace(s.Query, "SELECT *", "SELECT "+strings.Join(vtuColumns, ","), 1)
				run(named)
				rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, s.Query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				vtuMetadata(t, columns, b.PG)
				count := 0
				for rows.Next() {
					var cells [6]any
					if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5]); err != nil {
						t.Fatal(err)
					}
					for i, cell := range cells {
						s.CheckRaw(t, i, count, cell)
					}
					count++
				}
				if err := rows.Err(); err != nil || count != len(s.Rows) {
					t.Fatalf("rows=%d error=%v", count, err)
				}
				if err := closeQuery(); err != nil {
					t.Fatal(err)
				}
				vtuMetadata(t, columns, b.PG)
			}
		}
		nilStep := vtuProjection(b.PG, "view_schema='vtu_scope' AND table_name=$1", nil)
		nilStep.Args = []any{nil}
		run(nilStep)
		run(fixture.Prepared(nilStep))
	})
}

func TestReferenceInfoViewTableUsageDiagnostics(t *testing.T) {
	vtuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.view_table_usage", d)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		run(fixture.Exec("CREATE TABLE vtu_diag_t(a integer)", 0))
		run(fixture.Exec("CREATE VIEW vtu_diag_v AS SELECT a FROM vtu_diag_t", 0))
		unchanged := vtuProjection(b.PG, "view_schema='public' AND view_name='vtu_diag_v'", [][]any{vtuRow(database, "public", "vtu_diag_v", "public", "vtu_diag_t")})
		d = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table vtu_diag_t because other objects depend on it")
		d.Detail = "view vtu_diag_v depends on table vtu_diag_t"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("DROP TABLE vtu_diag_t RESTRICT", d))
		run(unchanged)
		q := "CREATE OR REPLACE VIEW vtu_diag_v AS SELECT missing FROM vtu_diag_t"
		d = fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "missing")]) + 1)
		run(fixture.ErrorDiagnostic(q, d))
		run(unchanged)
		run(fixture.Prepared(unchanged))
	})
}
