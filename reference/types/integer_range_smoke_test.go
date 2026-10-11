package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntegerRangeFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(k integer NOT NULL,a int4range,b pg_catalog.int8range)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'[1,4]','(1,4]')", 1))
		bytesInput := []byte("empty")
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 2, bytesInput, "[,]")))
		for i := range bytesInput {
			bytesInput[i] = 'x'
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 3, nil, []byte(nil)))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (4,'[2147483647,)','[-9223372036854775808,9223372036854775807)')", 1))
		want := [][]any{{int64(1), "[1,5)", "[2,5)"}, {int64(2), "empty", "(,)"}, {int64(3), nil, nil}, {int64(4), "[2147483647,)", "[-9223372036854775808,9223372036854775807)"}}
		check := rangeSelect(backend, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "INT4RANGE", "INT8RANGE"}, want, []bool{false, true, true})
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(check))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, rangeSelect(backend, "SELECT a,b FROM r WHERE k=0", []string{"a", "b"}, []string{"INT4RANGE", "INT8RANGE"}, nil, []bool{true, true}))
		for _, tc := range []struct{ input, code, message, detail string }{
			{"[1,2147483648)", "22003", `value "2147483648" is out of range for type integer`, ""},
			{"[1,2147483647]", "22003", "integer out of range", ""},
			{"[1,4", "22P02", `malformed range literal: "[1,4"`, "Unexpected end of input."},
			{"[4,1)", "22000", "range lower bound must be less than or equal to range upper bound", ""},
		} {
			wantError := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Context: `unnamed portal parameter $1 = '...'`}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO r VALUES (5,'[1,4]',NULL),(6,$1,NULL)", wantError, tc.input)))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
		}
		if backend.PG {
			continue
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := backend.DB.ExecContext(canceled, "INSERT INTO r VALUES (9,'[1,4]',NULL)"); !errors.Is(err, context.Canceled) {
			t.Fatal("ordinary cancellation", err)
		}
		held, err := backend.DB.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var dump, redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		rangeConsumeHeld(t, held, check)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("smoke redump changed", err)
		}
	}
}
