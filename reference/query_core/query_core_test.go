package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func queryCoreReferenceSetup(t *testing.T) *fixture.Pair {
	p := fixture.NewScenario(t)
	queryCoreSeed(t, p)
	return p
}

func TestReferenceQueryCoreHiddenSortAndHeldFrames(t *testing.T) {
	p := queryCoreReferenceSetup(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, prepared := range []bool{false, true} {
			func() {
				var stmt *sql.Stmt
				var rows *sql.Rows
				var err error
				query := "SELECT id FROM t ORDER BY v"
				if prepared {
					stmt, err = db.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = db.QueryContext(p.Ctx, query)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, []string{"id"}) {
					t.Fatal(names, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
					t.Fatal(types, err)
				}
				got := []int64{}
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					got = append(got, id)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if stmt != nil {
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if len(got) != 4 || !((got[0] == 1 && got[1] == 4) || (got[0] == 4 && got[1] == 1)) || got[2] != 3 || got[3] != 2 {
					t.Fatal("sort peers/hidden key", got)
				}
			}()
		}
		func() {
			stmt, err := db.PrepareContext(p.Ctx, "SELECT id FROM t WHERE v=$1 ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for i, arg := range []any{1, 2, nil} {
				want := [][]any{{int64(1)}, {int64(4)}}
				if i == 1 {
					want = [][]any{{int64(3)}}
				}
				if i == 2 {
					want = [][]any{}
				}
				step := fixture.Select("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, want, arg)
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					metadata, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.ScalarAssertMetadata(t, backend.PG, metadata)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{db, stmt, step.Query}, backend.Recorder, step)
			}
			rows, err := stmt.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("missing actual arguments exposed rows")
			}
			fixture.TransactionOrdinary(t, err, "sql: expected 1 arguments, got 0")
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	// Presence, not width, controls cardinality on both public backends.
	p.Step(t, fixture.Exec("CREATE TABLE zero(v INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO zero VALUES(1),(2)", 2))
	p.Step(t, fixture.Exec("ALTER TABLE zero DROP COLUMN v", 0))
	step := fixture.Select("SELECT * FROM zero", []string{}, []string{}, [][]any{{}, {}})
	step.Bag = true
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
}

func TestReferenceQueryCoreDiagnosticsAndParameterBoundary(t *testing.T) {
	p := queryCoreReferenceSetup(t)
	for _, population := range []string{"populated", "empty"} {
		if population == "empty" {
			p.Step(t, fixture.Exec("DELETE FROM t", 4))
		}
		for _, tc := range []struct {
			q    string
			want fixture.Diagnostic
		}{
			{"SELECT missing FROM empty", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
			{"SELECT missing FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
			{"SELECT /*é*/ missing FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
			{"SELECT id FROM t WHERE missing=1", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "24"}},
			{"SELECT id FROM t WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
			{"SELECT id FROM t WHERE s", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type text", Position: "24"}},
			{"SELECT $0 FROM t", fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $0", Position: "8"}},
			{"SELECT id FROM t WHERE id=TRUE", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "26", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{"SELECT id FROM t WHERE 'truth'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "24"}},
			{"SELECT id FROM t WHERE", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
			{"SELECT $1 FROM t WHERE id=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
			{"SELECT $1 FROM t WHERE f=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
			{"SELECT $1 FROM t WHERE v=$1 ORDER BY id", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
			{"SELECT $1 FROM t WHERE id=$1 ORDER BY id", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
			{"SELECT $1 FROM t WHERE f=$1 ORDER BY id", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
			{"SELECT $1 FROM t WHERE $1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
		} {
			step := fixture.ImplicitError(tc.q, tc.want)
			step.SelectRows = true
			step.ErrorStage = "execute"
			if tc.want.Code == "42P08" {
				step.Args = []any{1}
				if tc.want.Detail == "boolean versus text" {
					step.Args = []any{true}
				}
			}
			if population == "empty" && tc.want.Code == "42P08" {
				step.Args = nil
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
				if p.Postgres != nil {
					pg := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "8"})
					pg.SelectRows, pg.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pg)
				}
			} else {
				p.Step(t, step)
			}
			step.ErrorStage = "prepare"
			p.Step(t, fixture.Prepared(step))
		}
		gap := fixture.ImplicitError("SELECT $2 FROM t", fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"})
		gap.SelectRows = true
		gap.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, gap)
		simple := fixture.ImplicitError("SELECT $2 FROM t", fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "8"})
		simple.SelectRows = true
		simple.ErrorStage = "execute"
		if p.Postgres != nil {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, simple)
		}
		gap.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(gap))
	}
}

func TestReferenceQueryCoreMissingRelationPositionRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	// No t exists. Binding must fail before execution and preserve the actual
	// FROM identifier occurrence, counting characters rather than UTF8 bytes.
	for _, tc := range []struct {
		name, query, position string
	}{
		{"ascii", "SELECT id FROM t", "16"},
		{"utf8", "SELECT /*é*/ id FROM t", "22"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := fixture.ImplicitError(tc.query, fixture.Diagnostic{
				Code: "42P01", Message: `relation "t" does not exist`, Position: tc.position,
			})
			step.SelectRows = true
			step.ErrorStage = "execute"
			t.Run("query", func(t *testing.T) { p.Step(t, step) })
			step.ErrorStage = "prepare"
			t.Run("prepare", func(t *testing.T) { p.Step(t, fixture.Prepared(step)) })
		})
	}
}
