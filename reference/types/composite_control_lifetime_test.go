package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeControlLifetime(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE named AS (i integer,t text)`, `CREATE TABLE holder(k integer,v named)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'(7,x)'),(2,'(8,y)')`, 2))
		fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, true)
		if b.PG {
			return
		}
		parameter, err := c.PrepareContext(p.Ctx, `SELECT $1::named AS v`)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		selection, err := c.PrepareContext(p.Ctx, `SELECT v FROM holder ORDER BY k`)
		if err != nil {
			t.Fatal(err)
		}
		defer selection.Close()
		held, err := c.QueryContext(p.Ctx, `SELECT v FROM holder ORDER BY k`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		fixture.EnumReferenceMetadata(false, true)(t, held)
		writer := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
		done := make(chan error, 1)
		go func() { done <- stuntgres.Dump(p.Ctx, p.Native, writer) }()
		released, joined := false, false
		defer func() {
			if !released {
				close(writer.Release)
			}
			if !joined {
				select {
				case <-done:
				case <-p.Ctx.Done():
					t.Error("Dump worker failed to join")
				}
			}
		}()
		select {
		case <-writer.Started:
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (3,'(9,z)')`, 1))
		close(writer.Release)
		released = true
		select {
		case err = <-done:
			joined = true
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		if err != nil {
			t.Fatal(err)
		}
		// Dump captures before writer IO, excluding the concurrently later row.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(writer.Output.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.CompositeReferenceRows(t, p, b, b.DB, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, false)
		for _, tc := range []struct {
			stmt    *sql.Stmt
			args    []any
			message string
		}{
			{parameter, []any{"(1,a)"}, "cached plan must not change parameter type"},
			{selection, nil, "cached plan must not change result type"},
		} {
			rows, err := tc.stmt.QueryContext(p.Ctx, tc.args...)
			if rows != nil {
				rows.Close()
				t.Error("old composite statement exposed rows")
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message})
		}
		// Reader error and late cancellation cannot publish a staged graph.
		for _, reader := range []io.Reader{io.MultiReader(bytes.NewReader(writer.Output.Bytes()), fixture.CatalogControlFailingReader{Err: io.ErrUnexpectedEOF}), bytes.NewReader([]byte(`CREATE TYPE bad AS (i integer); CREATE TABLE fresh(v bad); INSERT INTO fresh VALUES ('(no)');`))} {
			if err := stuntgres.Restore(p.Ctx, p.Native, reader); err == nil {
				t.Error("failing Restore succeeded")
			}
			fixture.CompositeReferenceRows(t, p, b, b.DB, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, false)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		reader := &fixture.EnumCancelReader{Reader: bytes.NewReader(writer.Output.Bytes()), Cancel: cancel}
		if err := stuntgres.Restore(ctx, p.Native, reader); !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled Restore got %v", err)
		}
		cancel()
		fixture.CompositeReferenceRows(t, p, b, b.DB, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, false)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader([]byte(`CREATE TYPE named AS (t text); CREATE TABLE holder(k integer,v named); INSERT INTO holder VALUES (9,'(replacement)');`))); err != nil {
			t.Fatal(err)
		}
		fixture.CompositeReferenceRows(t, p, b, b.DB, `SELECT v FROM holder ORDER BY k`, [][]any{{"(replacement)"}}, false)
		for _, want := range []string{"(7,x)", "(8,y)"} {
			if !held.Next() {
				t.Fatal("held cursor missing row", held.Err())
			}
			var got any
			if err := held.Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("held got %#v want %q", got, want)
			}
		}
		fixture.EnumReferenceMetadata(false, true)(t, held)
		if held.Next() || held.Err() != nil {
			t.Fatal("held cursor tail", held.Err())
		}
	})
}
