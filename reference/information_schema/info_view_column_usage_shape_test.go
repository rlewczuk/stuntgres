package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewColumnUsageShape(t *testing.T) {
	vcuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		var attributes [][]any
		for i, name := range vcuColumns {
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.view_column_usage'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.view_column_usage'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		vcuSeed(run)
		run(fixture.Exec("CREATE VIEW vcu_scope.constant_v AS SELECT 1 AS id", 0))
		run(fixture.Exec("CREATE VIEW vcu_scope.null_v AS SELECT NULL::integer AS id", 0))
		for _, s := range []fixture.Step{
			vcuProjection(b.PG, "view_schema='vcu_scope'", vcuSeedRows(database)),
			vcuProjection(b.PG, "view_schema='vcu_scope' AND FALSE", nil),
			vcuProjection(b.PG, "view_schema='vcu_scope' AND column_name IS NULL", nil),
			vcuProjection(b.PG, "view_schema='vcu_scope' AND view_name='absent'", nil),
			vcuProjection(b.PG, "view_schema='vcu_scope' AND view_name IN ('constant_v','null_v')", nil),
		} {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				run(s)
				named := s
				named.Query = strings.Replace(s.Query, "SELECT *", "SELECT "+strings.Join(vcuColumns, ","), 1)
				run(named)
				rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, s.Query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = closeQuery() })
				columns, err := rows.ColumnTypes()
				if err != nil {
					_ = closeQuery()
					t.Fatal(err)
				}
				vcuMetadata(t, columns, b.PG)
				count := 0
				for rows.Next() {
					var cells [7]any
					if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5], &cells[6]); err != nil {
						_ = closeQuery()
						t.Fatal(err)
					}
					for i, cell := range cells {
						s.CheckRaw(t, i, count, cell)
					}
					count++
				}
				if err := rows.Err(); err != nil || count != len(s.Rows) {
					_ = closeQuery()
					t.Fatalf("rows=%d error=%v", count, err)
				}
				vcuMetadata(t, columns, b.PG) // Cached metadata survives EOF.
				if err := closeQuery(); err != nil {
					t.Fatal(err)
				}
				vcuMetadata(t, columns, b.PG) // And explicit Close.
			}
		}
		nilStep := vcuProjection(b.PG, "view_schema='vcu_scope' AND column_name=$1", nil)
		nilStep.Args = []any{nil}
		run(nilStep)
		run(fixture.Prepared(nilStep))
		subset := fixture.Select("SELECT column_name,view_name,table_name FROM information_schema.view_column_usage WHERE view_schema='vcu_scope'", []string{"column_name", "view_name", "table_name"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"a", "v", "t"}, {"b", "v", "t"}, {"a", "v", "u"}, {"a", "w", "v"}})
		subset.Bag = true
		subset.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			vcuMetadataNames(t, columns, subset.Columns, b.PG)
		}
		run(subset)
		run(fixture.Prepared(subset))
	})
}

func TestReferenceInfoViewColumnUsageDiagnostics(t *testing.T) {
	vcuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.view_column_usage", d)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		run(fixture.Exec("CREATE TABLE vcu_diag_t(a integer)", 0))
		run(fixture.Exec("CREATE VIEW vcu_diag_v AS SELECT a FROM vcu_diag_t", 0))
		unchanged := vcuProjection(b.PG, "view_schema='public' AND view_name='vcu_diag_v'", [][]any{vcuRow(database, "public", "vcu_diag_v", "public", "vcu_diag_t", "a")})
		held := vcuHeld(t, p, c, r, unchanged.Query)
		var definition string
		if err := c.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE schemaname='public' AND viewname='vcu_diag_v'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		check := func() {
			run(unchanged)
			run(fixture.Prepared(unchanged))
			held(unchanged)
			run(fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='public' AND viewname='vcu_diag_v'", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
		}
		d = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table vcu_diag_t because other objects depend on it")
		d.Detail = "view vcu_diag_v depends on table vcu_diag_t"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("DROP TABLE vcu_diag_t RESTRICT", d))
		check()
		q := "CREATE OR REPLACE VIEW vcu_diag_v AS SELECT missing FROM vcu_diag_t"
		d = fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "missing")]) + 1)
		run(fixture.ErrorDiagnostic(q, d))
		check()
	})
}
