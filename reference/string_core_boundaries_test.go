package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestReferenceStringCoreCancellationAndEncoding(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SHOW server_encoding", []string{"server_encoding"}, []string{"TEXT"}, [][]any{{"UTF8"}}))
			for _, q := range []string{"SET server_encoding='LATIN1'", "RESET server_encoding"} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceError(q, "55P02"))
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(ctx, "SELECT normalize('é')")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed cursor")
			}
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("ordinary query cancellation", err)
			}
			stmt, err := backend.db.PrepareContext(ctx, "SELECT normalize($1)")
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
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("SET search_path=public", 0))
			for _, tc := range []struct{ q, message, position, value, pgType string }{
				{"SELECT 'x'::text||current_schemas(false)", "operator does not exist: text || name[]", "17", "{x,public}", "_TEXT"},
				{"SELECT current_schemas(false)||'x'::text", "operator does not exist: name[] || text", "30", "{public,x}", "_NAME"},
			} {
				if backend.pg {
					// The opposite directions select distinct PostgreSQL result types;
					// native deliberately does not add array operators during relocation.
					step := referenceSelect(tc.q, []string{"?column?"}, []string{tc.pgType}, [][]any{{tc.value}})
					step.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 1 {
							t.Fatal("array metadata", types, err)
						}
						if types[0].ScanType() != reflect.TypeFor[any]() {
							t.Fatal("PostgreSQL array ScanType", types[0].ScanType())
						}
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
					step.nonContext = true
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
				} else {
					step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42883", Message: tc.message, Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
					step.errorStage = "prepare"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
					step.nonContext = true
					step.errorStage = "execute"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
					step.errorStage = "prepare"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
				}
			}
		})
	}
}
