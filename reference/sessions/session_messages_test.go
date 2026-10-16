package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSessionMessagesUnsupported(t *testing.T) {
	p := fixture.NewScenario(t)
	want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported")
	want.Position = "1"
	queries := []string{
		"LISTEN session_messages",
		"NOTIFY session_messages, 'payload'",
		"UNLISTEN session_messages",
		"UNLISTEN *",
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error("connection close:", err)
				}
			})
			if backend.PG {
				// Clean the physical session before returning it to the pool,
				// including when an assertion interrupts the command sequence.
				t.Cleanup(func() {
					if _, err := conn.ExecContext(p.Ctx, "UNLISTEN *"); err != nil {
						t.Error("UNLISTEN cleanup:", err)
					}
				})
			}
			for _, route := range []string{"exec", "prepare"} {
				t.Run(route, func(t *testing.T) {
					for _, query := range queries {
						t.Run(query, func(t *testing.T) {
							if route == "exec" {
								step := fixture.Exec(query, 0)
								if !backend.PG {
									step = fixture.ErrorDiagnostic(query, want)
								}
								fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
								return
							}

							stmt, err := conn.PrepareContext(p.Ctx, query)
							if stmt != nil {
								t.Cleanup(func() {
									if err := stmt.Close(); err != nil {
										t.Error("statement close:", err)
									}
								})
							}
							if !backend.PG {
								if stmt != nil {
									t.Fatal("unsupported Prepare exposed a statement")
								}
								fixture.AssertDiagnostic(t, err, &want)
								return
							}
							if err != nil {
								t.Fatal("Prepare:", err)
							}
							if stmt == nil {
								t.Fatal("successful Prepare returned a nil statement")
							}
							held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(query, 0))
						})
						// The command's statement cleanup has already run. Recovery
						// must use this same pinned physical connection.
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
							"SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}},
						))
					}
				})
			}
		})
	}
}
