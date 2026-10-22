package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func parametersScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

// All values below are independently specified, not learned from native runs.
func parametersValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, name, typ string, value any, args ...any) {
	t.Helper()
	s := fixture.Select(q, []string{name}, []string{typ}, [][]any{{value}}, args...)
	s.CheckMetadata = parametersMetadata(b, name, typ)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func parametersMetadata(b fixture.Backend, name, typ string) func(*testing.T, *sql.Rows) {
	return parametersMetadataNullability(b, name, typ, false, false)
}

func parametersMetadataNullability(b fixture.Backend, name, typ string, wantNullable, wantNullableKnown bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != 1 {
			t.Fatal(cols, err)
		}
		col := cols[0]
		wantScan := reflect.TypeFor[int64]()
		if b.PG && typ == "INT4" {
			wantScan = reflect.TypeFor[int32]()
		}
		if typ == "_INT4" {
			wantScan = reflect.TypeFor[string]()
			if b.PG {
				wantScan = reflect.TypeFor[any]()
			}
		}
		length, lengthOK := col.Length()
		precision, scale, decimalOK := col.DecimalSize()
		nullable, nullableOK := col.Nullable()
		// PostgreSQL scalar OUT/FROM and array accessor contracts matched
		// independent expectations on pinned18.6 in tests-3.md/tests-4.md.
		if col.Name() != name || col.DatabaseTypeName() != typ || col.ScanType() != wantScan || lengthOK || decimalOK || nullable != wantNullable || nullableOK != wantNullableKnown {
			t.Errorf("parameter metadata: name=%q type=%q scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t; want %s/%s scan=%v, unknown length/decimal, nullable=%t/%t", col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK, name, typ, wantScan, wantNullable, wantNullableKnown)
		}
	}
}

func parametersHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, typ string, inputs [][]any, values []any) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i, args := range inputs {
		s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{values[i]}}, args...)
		s.CheckMetadata = parametersMetadata(b, "v", typ)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
	}
}

// Compare every diagnostic field, including empty optional fields. PostgreSQL
// fields and stages were observed on pinned18.6 in tests-3.md; declaration and
// call-label Positions were corrected from that report before native-red.
func parametersError(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, query bool, code, message, hint, position string) {
	t.Helper()
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Hint, want.Position = hint, position
	stage := "execute"
	if query {
		stage = "prepare"
	}
	fixture.EnumReferenceErrorChecked(t, p, c, q, query, stage, func(t *testing.T, got fixture.Diagnostic) {
		if got != want {
			t.Errorf("parameter diagnostic query=%q got=%#v want=%#v", q, got, want)
		}
	})
}

// SQL checks run identically on both engines. Only native owns controls. Each
// unchanged emitted stream is also executed on PG, not compared as source text.
func parametersReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string, check func(*sql.Conn, fixture.Backend)) {
	t.Helper()
	check(c, b)
	if b.PG {
		return
	}
	for range 2 {
		dump := parametersNativeReplayRound(t, p, b, c, check)
		if p.Postgres != nil {
			func() {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
				defer func() {
					if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true; DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
						t.Error(err)
					}
				}()
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("unchanged parameter dump replay", err)
				}
				check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
			}()
		}
	}
}

// Domain element-NULL provenance belongs to native Restore's host-authorized
// codec. Ordinary PostgreSQL SQL does not consume that native comment payload.
// Direct PostgreSQL setup still checks every independently expected value.
func parametersNativeProvenanceReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check func(*sql.Conn, fixture.Backend)) {
	t.Helper()
	check(c, b)
	if b.PG {
		return
	}
	for range 2 {
		parametersNativeReplayRound(t, p, b, c, check)
	}
}

func parametersNativeReplayRound(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check func(*sql.Conn, fixture.Backend)) []byte {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(c, b)
	return dump.Bytes()
}

const parametersNoMatchHint = "No function matches the given name and argument types. You might need to add explicit type casts."
const parametersAmbiguousHint = "Could not choose a best candidate function. You might need to add explicit type casts."
