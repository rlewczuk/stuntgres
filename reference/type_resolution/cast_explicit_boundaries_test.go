package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCastExplicitBoundaries(t *testing.T) {
	p := castReferenceSetup(t)
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT 1::int4", []string{"int4"}, []string{"INT4"}, [][]any{{int64(1)}})
		step.Prepared = prepared
		p.Step(t, step)
	}
	for _, tc := range []struct {
		q  string
		pg fixture.Step
	}{
		{"SELECT i:: FROM t", fixture.ImplicitError("SELECT i:: FROM t", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "12"})},
		{"SELECT CAST(i AS) FROM t", fixture.ImplicitError("SELECT CAST(i AS) FROM t", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "17"})},
		{"INSERT INTO t(i) VALUES(CAST(i AS int4))", fixture.ImplicitError("INSERT INTO t(i) VALUES(CAST(i AS int4))", fixture.Diagnostic{Code: "42703", Message: `column "i" does not exist`, Detail: `There is a column named "i" in table "t", but it cannot be referenced from this part of the query.`, Position: "30"})},
		{"INSERT INTO t(i) VALUES(int4(i))", fixture.ImplicitError("INSERT INTO t(i) VALUES(int4(i))", fixture.Diagnostic{Code: "42703", Message: `column "i" does not exist`, Detail: `There is a column named "i" in table "t", but it cannot be referenced from this part of the query.`, Position: "30"})},
	} {
		t.Run(tc.q, func(t *testing.T) {
			if strings.HasPrefix(tc.q, "SELECT") {
				rows, err := p.Memory.QueryContext(p.Ctx, tc.q)
				if rows != nil {
					rows.Close()
					t.Fatal("native exclusion exposed rows")
				}
				if err == nil {
					t.Fatal("native exclusion succeeded")
				}
			} else if _, err := p.Memory.ExecContext(p.Ctx, tc.q); err == nil {
				t.Fatal("native forbidden source column succeeded")
			}
			if p.Postgres != nil {
				step := tc.pg
				step.SelectRows = strings.HasPrefix(tc.q, "SELECT")
				step.ErrorStage = "execute"
				fixture.ScalarRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
				if step.Diagnostic != nil {
					step.ErrorStage = "prepare"
				}
				fixture.ScalarRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
			}
		})
	}
}

func TestReferenceCastExplicitCursorCancellation(t *testing.T) {
	p := castReferenceSetup(t)
	for _, backend := range p.Backends() {
		q := "SELECT $1::int4 FROM t"
		held, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}()
		rows, err := held.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
		}
		held.Close()
		if err == nil {
			t.Fatal("missing argument accepted")
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("adapter count acquired SQLSTATE", err)
		}
		fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT i::bool FROM t ORDER BY id", []string{"i"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}}))
	}
	t.Run("native-raw", func(t *testing.T) {
		fixture.ScalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			ctx, cancel := context.WithCancel(p.Ctx)
			cursor, err := raw.QueryContext(ctx, "SELECT i::bool FROM t ORDER BY id", nil)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			defer cursor.Close()
			cancel()
			if err := cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	})
}
