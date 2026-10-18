package fixture

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// RegOperatorLiteral quotes one SQL string literal for operator-alias input.
func RegOperatorLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// RegOperatorRows asserts symbolic and OID cells, raw transport and full metadata.
func RegOperatorRows(q, alias, text string, oid int64, pg bool, args ...any) Step {
	var expectedOID any = oid
	if pg {
		expectedOID = fmt.Sprint(oid)
	}
	s := Select(q, []string{"v", "o"}, []string{strings.ToUpper(alias), "OID"}, [][]any{{text, expectedOID}}, args...)
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if col == 0 {
			RegtypeRaw(t, pg, v)
		} else {
			OidReferenceValue(t, pg, "OID", v)
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		RegOperatorMetadata(t, r, pg, []string{"v", "o"}, []string{strings.ToUpper(alias), "OID"})
	}
	return s
}

// RegOperatorMetadata asserts the operator-alias cursor's complete type contract.
func RegOperatorMetadata(t *testing.T, r *sql.Rows, pg bool, names, families []string) {
	t.Helper()
	ts, e := r.ColumnTypes()
	if e != nil {
		t.Fatal(e)
	}
	if len(ts) != len(names) {
		t.Fatal("metadata width", len(ts), len(names))
	}
	for i, c := range ts {
		scan := reflect.TypeFor[string]()
		if families[i] == "OID" {
			scan = reflect.TypeFor[int64]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		n, nk := c.Nullable()
		l, lk := c.Length()
		pr, sc, dk := c.DecimalSize()
		if c.Name() != names[i] || c.DatabaseTypeName() != families[i] || c.ScanType() != scan || n || nk || l != 0 || lk || pr != 0 || sc != 0 || dk {
			t.Fatalf("metadata %d name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d,%d/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), n, nk, l, lk, pr, sc, dk)
		}
	}
}

// RegOperatorContext retains middleware recorder provenance for alias input routes.
func RegOperatorContext(p *Pair, b Backend) context.Context {
	return context.WithValue(p.Ctx, RecorderKey{}, b.Recorder)
}

// RegOperatorError describes a complete operator-alias query diagnostic.
func RegOperatorError(q, code, message, hint, position, portal string, args ...any) Step {
	d := ControlsReferenceDiagnostic(code, message)
	d.Hint = hint
	d.Position = position
	d.Context = portal
	s := ErrorDiagnostic(q, d, args...)
	s.SelectRows = true
	return s
}

// RegOperatorDiagnosticRoutes checks literal, inferred and runtime-text input
// through direct, fresh-Prepare and held-statement routes with exact error stages.
func RegOperatorDiagnosticRoutes(t *testing.T, p *Pair, b Backend, c *sql.Conn, alias, input, code, message, hint string) {
	t.Helper()
	ctx := RegOperatorContext(p, b)
	literal := "SELECT " + RegOperatorLiteral(input) + "::" + alias
	s := RegOperatorError(literal, code, message, hint, "8", "")
	RunStep(t, ctx, c, b.Recorder, s)
	s = Prepared(s)
	s.ErrorStage = "prepare"
	RunStep(t, ctx, c, b.Recorder, s)
	q := "SELECT $1::" + alias
	stmt, e := c.PrepareContext(ctx, q)
	if e != nil {
		t.Fatal("parameter Prepare", e)
	}
	defer stmt.Close()
	portal := "unnamed portal parameter $1 = '...'"
	if input == "" {
		portal = "unnamed portal parameter $1 = ''"
	}
	s = RegOperatorError(q, code, message, hint, "", portal, input)
	s.ErrorStage = "execute"
	RunStep(t, ctx, c, b.Recorder, s)
	RunStep(t, ctx, HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
	s = RegOperatorError("SELECT $1::text::"+alias, code, message, hint, "", "", input)
	s.ErrorStage = "execute"
	RunStep(t, ctx, c, b.Recorder, s)
	RunStep(t, ctx, c, b.Recorder, Prepared(s))
}
