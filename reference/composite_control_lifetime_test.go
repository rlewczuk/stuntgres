package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"stuntgres"
	"testing"
)

func TestReferenceCompositeControlLifetime(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE TYPE named AS (i integer,t text)`, `CREATE TABLE holder(k integer,v named)`)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES (1,'(7,x)'),(2,'(8,y)')`, 2))
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, true)
		if b.pg {
			return
		}
		parameter, err := c.PrepareContext(p.ctx, `SELECT $1::named AS v`)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		selection, err := c.PrepareContext(p.ctx, `SELECT v FROM holder ORDER BY k`)
		if err != nil {
			t.Fatal(err)
		}
		defer selection.Close()
		held, err := c.QueryContext(p.ctx, `SELECT v FROM holder ORDER BY k`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		enumReferenceMetadata(false, true)(t, held)
		writer := &catalogControlGatedWriter{started: make(chan struct{}), release: make(chan struct{})}
		done := make(chan error, 1)
		go func() { done <- stuntgres.Dump(p.ctx, p.native, writer) }()
		released, joined := false, false
		defer func() {
			if !released {
				close(writer.release)
			}
			if !joined {
				select {
				case <-done:
				case <-p.ctx.Done():
					t.Error("Dump worker failed to join")
				}
			}
		}()
		select {
		case <-writer.started:
		case <-p.ctx.Done():
			t.Fatal(p.ctx.Err())
		}
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`INSERT INTO holder VALUES (3,'(9,z)')`, 1))
		close(writer.release)
		released = true
		select {
		case err = <-done:
			joined = true
		case <-p.ctx.Done():
			t.Fatal(p.ctx.Err())
		}
		if err != nil {
			t.Fatal(err)
		}
		// Dump captures before writer IO, excluding the concurrently later row.
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(writer.output.Bytes())); err != nil {
			t.Fatal(err)
		}
		compositeReferenceRows(t, p, b, b.db, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, false)
		for _, tc := range []struct {
			stmt    *sql.Stmt
			args    []any
			message string
		}{
			{parameter, []any{"(1,a)"}, "cached plan must not change parameter type"},
			{selection, nil, "cached plan must not change result type"},
		} {
			rows, err := tc.stmt.QueryContext(p.ctx, tc.args...)
			if rows != nil {
				rows.Close()
				t.Error("old composite statement exposed rows")
			}
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message})
		}
		// Reader error and late cancellation cannot publish a staged graph.
		for _, reader := range []io.Reader{io.MultiReader(bytes.NewReader(writer.output.Bytes()), catalogControlFailingReader{err: io.ErrUnexpectedEOF}), bytes.NewReader([]byte(`CREATE TYPE bad AS (i integer); CREATE TABLE fresh(v bad); INSERT INTO fresh VALUES ('(no)');`))} {
			if err := stuntgres.Restore(p.ctx, p.native, reader); err == nil {
				t.Error("failing Restore succeeded")
			}
			compositeReferenceRows(t, p, b, b.db, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, false)
		}
		ctx, cancel := context.WithCancel(p.ctx)
		reader := &enumCancelReader{Reader: bytes.NewReader(writer.output.Bytes()), cancel: cancel}
		if err := stuntgres.Restore(ctx, p.native, reader); !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled Restore got %v", err)
		}
		cancel()
		compositeReferenceRows(t, p, b, b.db, `SELECT v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {"(8,y)"}}, false)
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader([]byte(`CREATE TYPE named AS (t text); CREATE TABLE holder(k integer,v named); INSERT INTO holder VALUES (9,'(replacement)');`))); err != nil {
			t.Fatal(err)
		}
		compositeReferenceRows(t, p, b, b.db, `SELECT v FROM holder ORDER BY k`, [][]any{{"(replacement)"}}, false)
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
		enumReferenceMetadata(false, true)(t, held)
		if held.Next() || held.Err() != nil {
			t.Fatal("held cursor tail", held.Err())
		}
	})
}
