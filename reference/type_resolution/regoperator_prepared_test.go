package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegOperatorPreparedLifetime(t *testing.T) {
	regoperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, alias := range []string{"regoper", "regoperator"} {
			func() {
				q := "SELECT $1::" + alias + " AS v,$1::" + alias + "::oid AS o"
				stmt, e := c.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				defer func() {
					if e := stmt.Close(); e != nil {
						t.Errorf("statement close: %v", e)
					}
				}()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				input, text, bad := "||/", "||/", "||//"
				oid := int64(597)
				if alias == "regoperator" {
					input, text, bad, oid = "-(NONE,int4)", "-(NONE,integer)", "++(int4,int4)", 558
				}
				for _, value := range []any{input, []byte(input), oidReferenceValuer{value: input}} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RegOperatorRows(q, alias, text, oid, b.PG, value))
				}
				s := fixture.RegOperatorError(q, "42883", "operator does not exist: "+bad, "", "", "unnamed portal parameter $1 = '...'", bad)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RegOperatorRows(q, alias, text, oid, b.PG, input))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v", "o"}, []string{map[string]string{"regoper": "REGOPER", "regoperator": "REGOPERATOR"}[alias], "OID"}, [][]any{{nil, nil}}, nil))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v", "o"}, []string{map[string]string{"regoper": "REGOPER", "regoperator": "REGOPERATOR"}[alias], "OID"}, [][]any{{nil, nil}}, []byte(nil)))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RegOperatorRows(q, alias, text, oid, b.PG, input))
			}()
		}
		q := "SELECT '+(int4,int4)'::regoperator AS v,'+(int4,int4)'::regoperator::oid AS o"
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := stmt.Close(); e != nil {
				t.Errorf("statement close: %v", e)
			}
		}()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RegOperatorRows(q, "regoperator", "+(integer,integer)", 551, b.PG))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO public,pg_catalog", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RegOperatorRows(q, "regoperator", "+(integer,integer)", 551, b.PG))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RegOperatorRows("SELECT $1::text::regoperator AS v,$1::text::regoperator::oid AS o", "regoperator", "+(integer,integer)", 551, b.PG, "+(int,int)"))
	})
}

func TestReferenceRegOperatorPreparedAnalysis(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			query := "SELECT '551'::regtype AS t,'551'::regoper AS r,'551'::regoperator AS s,$1::text::regoperator AS runtime"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			step := fixture.Select(query, []string{"t", "r", "s", "runtime"}, []string{"REGTYPE", "REGOPER", "REGOPERATOR", "REGOPERATOR"}, [][]any{{"551", "pg_catalog.+", "+(integer,integer)", "-(NONE,integer)"}}, "-(NONE,int4)")
			step.CheckRaw = func(t *testing.T, col, row int, v any) bool {
				fixture.RegtypeRaw(t, b.PG, v)
				return true
			}
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				fixture.RegOperatorMetadata(t, rows, b.PG, step.Columns, step.Types)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			rows, err := stmt.QueryContext(p.Ctx, "-(NONE,int4)")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			metadata, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal("held row missing", rows.Err())
			}
			raw := make([]any, 4)
			if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3]); err != nil {
				t.Fatal(err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("held cursor changed cardinality or failed", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO public,pg_catalog", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			bad := fixture.RegOperatorError(query, "42883", "operator does not exist: ++(int4,int4)", "", "", "", "++(int4,int4)")
			bad.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			// The old cursor and metadata remain their own finalized publication,
			// not a view onto a successful replacement or failed attempt.
			want := []string{"551", "pg_catalog.+", "+(integer,integer)", "-(NONE,integer)"}
			for i, value := range raw {
				fixture.RegtypeRaw(t, b.PG, value)
				var text string
				switch value := value.(type) {
				case string:
					text = value
				case []byte:
					text = string(value)
				default:
					t.Fatalf("held cell %d has unexpected transport %T", i, value)
				}
				if text != want[i] || metadata[i].Name() != step.Columns[i] || metadata[i].DatabaseTypeName() != step.Types[i] {
					t.Fatalf("held publication %d text=%q metadata=%s/%s", i, text, metadata[i].Name(), metadata[i].DatabaseTypeName())
				}
			}
		})
	}
}
