package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCharacterSourceValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct {
		first, second string
		want          [][]any
	}{
		{"CAST('b  ' AS text)", "CAST('a ' AS varchar)", [][]any{{"a "}, {"b  "}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS text)", [][]any{{"a "}, {"b  "}}},
		{"CAST('b  ' AS text)", "CAST('a ' AS char(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a ' AS char(2))", "CAST('b  ' AS text)", [][]any{{"a"}, {"b"}}},
		{"CAST('b  ' AS char(3))", "CAST('a ' AS varchar)", [][]any{{"a"}, {"b"}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS char(3))", [][]any{{"a "}, {"b"}}},
		{"CAST('b' AS char(2))", "CAST('a' AS char(3))", [][]any{{"a"}, {"b"}}},
		{"CAST('b ' AS varchar(2))", "CAST('a ' AS varchar(2))", [][]any{{"a "}, {"b "}}},
	} {
		for _, prepared := range []bool{false, true} {
			p.Step(t, fixture.Exec("CREATE TABLE dst(v TEXT)", 0))
			s := fixture.Exec("INSERT INTO dst VALUES("+tc.first+"),("+tc.second+") ORDER BY 1", 2)
			s.Prepared = prepared
			for _, backend := range p.Backends() {
				if backend.PG {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				} else {
					fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
			p.Step(t, fixture.Select("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"TEXT"}, tc.want))
			for _, backend := range p.Backends() {
				fixture.CharacterMetadata(t, p, backend.DB, "SELECT v FROM dst", prepared, !backend.PG, []string{"v"}, []string{"TEXT"}, []int64{-1}, []fixture.CharacterNullability{{true, true}})
			}
			p.Step(t, fixture.Exec("DROP TABLE dst", 0))
		}
	}
}

func TestReferenceCharacterSourceValuesHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, source := range []string{"bpchar", "varchar"} {
		for _, prepared := range []bool{false, true} {
			p.Step(t, fixture.Exec("CREATE TABLE dst(v TEXT)", 0))
			q := "INSERT INTO dst VALUES(CAST($1 AS text)),(CAST($2 AS " + source + ")) ORDER BY 1"
			for _, backend := range p.Backends() {
				func() {
					var stmt *sql.Stmt
					var executor fixture.Executor = backend.DB
					if prepared {
						var err error
						stmt, err = backend.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
					}
					for _, args := range [][]any{{"b  ", "a "}, {nil, "c "}} {
						s := fixture.Exec(q, 2, args...)
						if backend.PG {
							fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
						} else {
							fixture.InsertSelectRun(t, p.Ctx, executor, backend.Recorder, s)
						}
					}
				}()
			}
			want := [][]any{{"a "}, {"b  "}, {"c "}, {nil}}
			if source == "bpchar" {
				want = [][]any{{"a"}, {"b  "}, {"c"}, {nil}}
			}
			p.Step(t, fixture.Select("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"TEXT"}, want))
			p.Step(t, fixture.Exec("DROP TABLE dst", 0))
		}
	}
}

func TestReferenceCharacterSourceValuesAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE dst(v VARCHAR(2))", 0))
	p.Step(t, fixture.Exec("INSERT INTO dst VALUES('s')", 1))
	q := "INSERT INTO dst VALUES(CAST('aa' AS text)),(CAST('zzx' AS varchar)) ORDER BY 1"
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			var result sql.Result
			var err error
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			if prepared {
				stmt, prepareErr := backend.DB.PrepareContext(ctx, q)
				if prepareErr != nil {
					t.Fatal("assignment rejected at Prepare", prepareErr)
				}
				result, err = stmt.ExecContext(ctx)
				stmt.Close()
			} else {
				result, err = backend.DB.ExecContext(ctx, q)
			}
			if result != nil {
				t.Fatal("failed source insert exposed result")
			}
			fixture.State(t, err, "22001")
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22001", Message: "value too long for type character varying(2)"})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"VARCHAR"}, [][]any{{"s"}}))
		}
	}
}

func TestReferenceCharacterSourceValuesCrossTypmod(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct {
		first, second string
		want          [][]any
	}{
		{"CAST('b' AS char(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(2))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(2))", "CAST('a' AS char(2))", [][]any{{"a "}, {"b "}}},
		{"CAST('b' AS varchar(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(3))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(3))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(2))", "NULL", [][]any{{"b "}, {nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			p.Step(t, fixture.Exec("CREATE TABLE dst(v BPCHAR)", 0))
			s := fixture.Exec("INSERT INTO dst VALUES("+tc.first+"),("+tc.second+") ORDER BY 1", 2)
			s.Prepared = prepared
			for _, backend := range p.Backends() {
				if backend.PG {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				} else {
					fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
			p.Step(t, fixture.Select("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"BPCHAR"}, tc.want))
			for _, backend := range p.Backends() {
				fixture.CharacterMetadata(t, p, backend.DB, "SELECT v FROM dst", prepared, !backend.PG, []string{"v"}, []string{"BPCHAR"}, []int64{-1}, []fixture.CharacterNullability{{true, true}})
			}
			p.Step(t, fixture.Exec("DROP TABLE dst", 0))
		}
	}
}
