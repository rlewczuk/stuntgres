package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCoreCancellationAndEncoding(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW server_encoding", []string{"server_encoding"}, []string{"TEXT"}, [][]any{{"UTF8"}}))
			for _, q := range []string{"SET server_encoding='LATIN1'", "RESET server_encoding"} {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Error(q, "55P02"))
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := backend.DB.QueryContext(ctx, "SELECT normalize('é')")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed cursor")
			}
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("ordinary query cancellation", err)
			}
			stmt, err := backend.DB.PrepareContext(ctx, "SELECT normalize($1)")
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled Prepare exposed statement")
			}
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("ordinary Prepare cancellation", err)
			}
		})
	}
}

func TestReferenceStringCoreArrayBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET search_path=public", 0))
			for _, tc := range []struct{ q, message, position, value, pgType string }{
				{"SELECT 'x'::text||current_schemas(false)", "operator does not exist: text || name[]", "17", "{x,public}", "_TEXT"},
				{"SELECT current_schemas(false)||'x'::text", "operator does not exist: name[] || text", "30", "{public,x}", "_NAME"},
			} {
				if backend.PG {
					// The opposite directions select distinct PostgreSQL result types;
					// native deliberately does not add array operators during relocation.
					step := fixture.Select(tc.q, []string{"?column?"}, []string{tc.pgType}, [][]any{{tc.value}})
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 1 {
							t.Fatal("array metadata", types, err)
						}
						if types[0].ScanType() != reflect.TypeFor[any]() {
							t.Fatal("PostgreSQL array ScanType", types[0].ScanType())
						}
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
					step.NonContext = true
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
				} else {
					step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: tc.message, Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					step.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
					step.NonContext = true
					step.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					step.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
				}
			}
		})
	}
}
