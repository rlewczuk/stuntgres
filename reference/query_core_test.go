//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func queryCoreReferenceSetup(t *testing.T) *referencePair {
	p := newReferencePair(t)
	queryCoreSeed(t, p)
	return p
}

func TestReferenceQueryCoreHiddenSortAndHeldFrames(t *testing.T) {
	p := queryCoreReferenceSetup(t)
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		for _, prepared := range []bool{false, true} {
			var stmt *sql.Stmt
			var rows *sql.Rows
			var err error
			query := "SELECT id FROM t ORDER BY v"
			if prepared {
				stmt, err = db.PrepareContext(p.ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = stmt.QueryContext(p.ctx)
			} else {
				rows, err = db.QueryContext(p.ctx, query)
			}
			if err != nil {
				t.Fatal(err)
			}
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
			rows.Close()
			if stmt != nil {
				stmt.Close()
			}
			if len(got) != 4 || !((got[0] == 1 && got[1] == 4) || (got[0] == 4 && got[1] == 1)) || got[2] != 3 || got[3] != 2 {
				t.Fatal("sort peers/hidden key", got)
			}
		}
		stmt, err := db.PrepareContext(p.ctx, "SELECT id FROM t WHERE v=$1 ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		for i, arg := range []any{1, 2, nil} {
			want := [][]any{{int64(1)}, {int64(4)}}
			if i == 1 {
				want = [][]any{{int64(3)}}
			}
			if i == 2 {
				want = [][]any{}
			}
			rows, err := stmt.QueryContext(p.ctx, arg)
			if err != nil {
				t.Fatal(err)
			}
			got := [][]any{}
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				got = append(got, []any{id})
			}
			if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(got, want, err)
			}
			rows.Close()
		}
		stmt.Close()
	}
	// Presence, not width, controls cardinality on both public backends.
	p.step(t, referenceExec("CREATE TABLE zero(v INT)", 0))
	p.step(t, referenceExec("INSERT INTO zero VALUES(1),(2)", 2))
	p.step(t, referenceExec("ALTER TABLE zero DROP COLUMN v", 0))
	step := referenceSelect("SELECT * FROM zero", []string{}, []string{}, [][]any{{}, {}})
	step.bag = true
	p.step(t, step)
	p.step(t, referencePrepared(step))
}

func TestReferenceQueryCoreDiagnosticsAndParameterBoundary(t *testing.T) {
	p := queryCoreReferenceSetup(t)
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{"SELECT missing FROM empty", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}},
		{"SELECT /*é*/ missing FROM t", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
		{"SELECT id FROM t WHERE missing=1", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "24"}},
		{"SELECT id FROM t WHERE 1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
		{"SELECT id FROM t WHERE id=TRUE", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "26", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE 'truth'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "24"}},
		{"SELECT id FROM t WHERE", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
		{"SELECT $1 FROM t WHERE id=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
		{"SELECT $1 FROM t WHERE f=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
	} {
		step := referenceImplicitError(tc.q, tc.want)
		step.selectRows = true
		step.errorStage = "execute"
		if tc.want.Code == "42P08" {
			step.args = []any{1}
			if tc.want.Detail == "boolean versus text" {
				step.args = []any{true}
			}
		}
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
	gap := referenceImplicitError("SELECT $2 FROM t", referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"})
	gap.selectRows = true
	gap.errorStage = "execute"
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, gap)
	simple := referenceImplicitError("SELECT $2 FROM t", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "8"})
	simple.selectRows = true
	simple.errorStage = "execute"
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, simple)
	gap.errorStage = "prepare"
	p.step(t, referencePrepared(gap))
}

func TestReferenceQueryCoreMissingRelationPositionRegression(t *testing.T) {
	p := newReferencePair(t)
	// No t exists. Binding must fail before execution and preserve the actual
	// FROM identifier occurrence, counting characters rather than UTF8 bytes.
	for _, tc := range []struct {
		name, query, position string
	}{
		{"ascii", "SELECT id FROM t", "16"},
		{"utf8", "SELECT /*é*/ id FROM t", "22"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := referenceImplicitError(tc.query, referenceDiagnostic{
				Code: "42P01", Message: `relation "t" does not exist`, Position: tc.position,
			})
			step.selectRows = true
			step.errorStage = "execute"
			t.Run("query", func(t *testing.T) { p.step(t, step) })
			step.errorStage = "prepare"
			t.Run("prepare", func(t *testing.T) { p.step(t, referencePrepared(step)) })
		})
	}
}
