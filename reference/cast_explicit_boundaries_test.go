package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

func TestReferenceCastExplicitBoundaries(t *testing.T) {
	p := castReferenceSetup(t)
	for _, prepared := range []bool{false, true} {
		step := referenceSelect("SELECT 1::int4", []string{"int4"}, []string{"INT4"}, [][]any{{int64(1)}})
		step.prepared = prepared
		p.step(t, step)
	}
	for _, tc := range []struct {
		q  string
		pg referenceStep
	}{
		{"SELECT i:: FROM t", referenceImplicitError("SELECT i:: FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "12"})},
		{"SELECT CAST(i AS) FROM t", referenceImplicitError("SELECT CAST(i AS) FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "17"})},
		{"INSERT INTO t(i) VALUES(CAST(i AS int4))", referenceImplicitError("INSERT INTO t(i) VALUES(CAST(i AS int4))", referenceDiagnostic{Code: "42703", Message: `column "i" does not exist`, Detail: `There is a column named "i" in table "t", but it cannot be referenced from this part of the query.`, Position: "30"})},
		{"INSERT INTO t(i) VALUES(int4(i))", referenceImplicitError("INSERT INTO t(i) VALUES(int4(i))", referenceDiagnostic{Code: "42703", Message: `column "i" does not exist`, Detail: `There is a column named "i" in table "t", but it cannot be referenced from this part of the query.`, Position: "30"})},
	} {
		t.Run(tc.q, func(t *testing.T) {
			if strings.HasPrefix(tc.q, "SELECT") {
				rows, err := p.memory.QueryContext(p.ctx, tc.q)
				if rows != nil {
					rows.Close()
					t.Fatal("native exclusion exposed rows")
				}
				if err == nil {
					t.Fatal("native exclusion succeeded")
				}
			} else if _, err := p.memory.ExecContext(p.ctx, tc.q); err == nil {
				t.Fatal("native forbidden source column succeeded")
			}
			if p.postgres != nil {
				step := tc.pg
				step.selectRows = strings.HasPrefix(tc.q, "SELECT")
				step.errorStage = "execute"
				scalarRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
				if step.diagnostic != nil {
					step.errorStage = "prepare"
				}
				scalarRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(step))
			}
		})
	}
}

func TestReferenceCastExplicitCursorCancellation(t *testing.T) {
	p := castReferenceSetup(t)
	for _, backend := range p.backends() {
		q := "SELECT $1::int4 FROM t"
		held, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := held.Close(); err != nil {
				t.Error(err)
			}
		}()
		rows, err := held.QueryContext(p.ctx)
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
		scalarRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT i::bool FROM t ORDER BY id", []string{"i"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}}))
	}
	t.Run("native-raw", func(t *testing.T) {
		scalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			ctx, cancel := context.WithCancel(p.ctx)
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
