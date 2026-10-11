package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)


func TestReferenceIntegerRangeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	want := [][]any{
		{int64(1), "[1,5)", "[2,5)"},
		{int64(2), "empty", "(,)"},
		{int64(3), nil, nil},
		{int64(4), "[-2147483648,2147483647)", "[-9223372036854775808,9223372036854775807)"},
	}
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(k integer NOT NULL,a int4range,b int8range)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE vacant(a int4range NOT NULL,b int8range)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'[1,4]','(1,4]'),(2,'empty','[,]'),(3,NULL,NULL),(4,'[-2147483648,2147483647)','[-9223372036854775808,9223372036854775807)')", 4))
		check := rangeSelect(backend, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "INT4RANGE", "INT8RANGE"}, want, []bool{false, true, true})
		vacant := rangeSelect(backend, "SELECT a,b FROM vacant", []string{"a", "b"}, []string{"INT4RANGE", "INT8RANGE"}, nil, []bool{false, true})
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, vacant)
		if backend.PG {
			continue // Native controls have no PostgreSQL API analogue.
		}
		held, err := backend.DB.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check.CheckMetadata(t, held)
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
		rangeConsumeHeld(t, held, check)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, vacant)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("redump mismatch", err, redump.String())
		}
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, s := range []fixture.Step{check, vacant, fixture.Prepared(check)} {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
		}
		redump.Reset()
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("independent Driver redump mismatch", err, redump.String())
		}
		for _, typ := range []string{"int4range", "int8range"} {
			for _, tc := range integerRangeFailures(typ) {
				// Every failed stage leaves the complete destination, its metadata,
				// and a reader captured before replacement untouched.
				held, err := target.Memory.QueryContext(target.Ctx, check.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				script := "CREATE TABLE staged(v " + typ + "); INSERT INTO staged VALUES ('[7,8)'); INSERT INTO staged VALUES (" + rangeLiteral(tc.input) + ");"
				position := utf8.RuneCountInString(script[:strings.LastIndex(script, "('")+1]) + 1
				wantError := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Position: strconv.Itoa(position)}
				err = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script))
				fixture.AssertDiagnostic(t, err, &wantError)
				rangeConsumeHeld(t, held, check)
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, vacant)
				redump.Reset()
				if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
					t.Fatal("failed Restore published schema/data", typ, tc.input, err)
				}
			}
		}
		canceled, cancel := context.WithCancel(target.Ctx)
		cancel()
		for _, call := range []func() error{
			func() error { return stuntgres.Restore(canceled, target.Native, bytes.NewReader(dump.Bytes())) },
			func() error { return stuntgres.Reset(canceled, target.Native) },
			func() error { return stuntgres.Dump(canceled, target.Native, &bytes.Buffer{}) },
		} {
			err := call()
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("control cancellation must be ordinary", err)
			}
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
		redump.Reset()
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("canceled control changed destination", err)
		}
		// Successful shape replacement must not remap an already-held reader.
		held, err = target.Memory.QueryContext(target.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE TABLE r(v text); INSERT INTO r VALUES ('replacement');")); err != nil {
			t.Fatal(err)
		}
		rangeConsumeHeld(t, held, check)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM r", []string{"v"}, []string{"TEXT"}, [][]any{{"replacement"}}))
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual native script replay", err)
			}
			pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, rangeSelect(pg, check.Query, check.Columns, check.Types, want, []bool{false, true, true}))
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, rangeSelect(pg, vacant.Query, vacant.Columns, vacant.Types, nil, []bool{false, true}))
			// Leave the schema clean for the ordinary PG branch of this same body.
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(fmt.Errorf("replay cleanup: %w", err))
			}
		}
	}
}
