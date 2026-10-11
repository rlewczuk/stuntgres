package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Domains are already admitted by generic concrete-base composition. Their
// destination-context input must replay actual Dump output without range casts.
func TestReferenceIntegerRangeDomainControls(t *testing.T) {
	p := fixture.NewScenario(t)
	want := [][]any{
		{int64(1), "[1,5)", "[2,5)"},
		{int64(2), "empty", "(,)"},
		{int64(3), nil, nil},
		{int64(4), "[-2147483648,2147483647)", "[-9223372036854775808,9223372036854775807)"},
		{int64(5), nil, nil},
	}
	for _, backend := range p.Backends() {
		for _, query := range []string{
			"CREATE DOMAIN public.span4 AS int4range",
			"CREATE DOMAIN public.span8 AS pg_catalog.int8range",
			"CREATE DOMAIN public.chain4 AS public.span4",
			"CREATE DOMAIN public.chain8 AS public.span8",
			"CREATE TABLE r(k integer NOT NULL,a public.span4,b public.span8)",
			"CREATE TABLE vacant(a public.span4 NOT NULL,b public.span8)",
			"CREATE TABLE nested(k integer NOT NULL,a public.chain4,b public.chain8)",
			"CREATE TABLE required(k integer NOT NULL,a public.chain4 NOT NULL,b public.chain8 NOT NULL)",
		} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(query, 0))
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'[1,4]','(1,4]'),(2,'empty','[,]'),(3,NULL,NULL),(4,'[-2147483648,2147483647)','[-9223372036854775808,9223372036854775807)')", 4))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 5, nil, []byte(nil))))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO nested VALUES (1,'[1,4]','(1,4]')", 1))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO nested VALUES ($1,$2,$3)", 1, 2, nil, []byte(nil))))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO required VALUES (1,'[1,4]','(1,4]')", 1))
		check := rangeSelect(backend, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "INT4RANGE", "INT8RANGE"}, want, []bool{false, true, true})
		vacant := rangeSelect(backend, "SELECT a,b FROM vacant", []string{"a", "b"}, []string{"INT4RANGE", "INT8RANGE"}, nil, []bool{false, true})
		nestedWant := [][]any{{int64(1), "[1,5)", "[2,5)"}, {int64(2), nil, nil}}
		nested := rangeSelect(backend, "SELECT k,a,b FROM nested ORDER BY k", check.Columns, check.Types, nestedWant, []bool{false, true, true})
		requiredWant := [][]any{{int64(1), "[1,5)", "[2,5)"}}
		required := rangeSelect(backend, "SELECT k,a,b FROM required ORDER BY k", check.Columns, check.Types, requiredWant, []bool{false, false, false})
		for _, tc := range []struct {
			column, detail string
			a, b           any
		}{
			{"a", "Failing row contains (2, null, [1,5)).", nil, "[1,4]"},
			{"b", "Failing row contains (2, [1,5), null).", "[1,4]", nil},
		} {
			wantError := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "` + tc.column + `" of relation "required" violates not-null constraint`, Detail: tc.detail, Schema: "public", Table: "required", Column: tc.column}
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{
					"INSERT INTO required VALUES (2,$1,$2)",
					"INSERT INTO required VALUES (0,'[7,8)','[7,8)'),(2,$1,$2)",
				} {
					s := fixture.ErrorDiagnostic(query, wantError, tc.a, tc.b)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, required)
				}
			}
		}
		for _, step := range []fixture.Step{nested, fixture.Prepared(nested), required, fixture.Prepared(required)} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
		}
		for _, step := range []fixture.Step{check, fixture.Prepared(check), vacant, fixture.Prepared(vacant)} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
		}
		if backend.PG {
			continue // PostgreSQL has no native process-local control API.
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
		// Keep a populated target during replacement, not only Reset-to-empty.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("same-engine range domain Restore of actual Dump", err)
		}
		rangeConsumeHeld(t, held, check)
		for _, step := range []fixture.Step{check, fixture.Prepared(check), vacant} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
		}
		for _, step := range []fixture.Step{nested, fixture.Prepared(nested), required, fixture.Prepared(required)} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("same-engine range domain redump changed", err, redump.String())
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("range domain Reset/Restore", err)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE allocator_spacer(v integer)", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("independent Driver range domain Restore", err)
		}
		for _, step := range []fixture.Step{check, fixture.Prepared(check), vacant, fixture.Prepared(vacant)} {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step)
		}
		for _, step := range []fixture.Step{nested, fixture.Prepared(nested), required, fixture.Prepared(required)} {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step)
		}
		redump.Reset()
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("fresh Driver range domain redump changed", err, redump.String())
		}
		// A later constraint failure must not publish its staged domain graph
		// or consume the previously captured nested-domain reader.
		for _, column := range []string{"a", "b"} {
			held, err := target.Memory.QueryContext(target.Ctx, nested.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			nested.CheckMetadata(t, held)
			a, b, detail := "NULL", "'[1,4]'", "Failing row contains (2, null, [1,5))."
			if column == "b" {
				a, b, detail = "'[1,4]'", "NULL", "Failing row contains (2, [1,5), null)."
			}
			script := "CREATE DOMAIN public.span4 AS int4range; CREATE DOMAIN public.span8 AS int8range; CREATE DOMAIN public.chain4 AS public.span4; CREATE DOMAIN public.chain8 AS public.span8; CREATE TABLE required(k integer NOT NULL,a public.chain4 NOT NULL,b public.chain8 NOT NULL); INSERT INTO required VALUES (1,'[7,8)','[7,8)'); INSERT INTO required VALUES (2," + a + "," + b + ");"
			wantError := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "` + column + `" of relation "required" violates not-null constraint`, Detail: detail, Schema: "public", Table: "required", Column: column}
			fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, bytes.NewBufferString(script)), &wantError)
			rangeConsumeHeld(t, held, nested)
			for _, step := range []fixture.Step{check, nested, required, vacant} {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step)
			}
			redump.Reset()
			if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("late NOT NULL Restore changed domain declarations/rows", err, redump.String())
			}
		}
		held, err = target.Memory.QueryContext(target.Ctx, nested.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		nested.CheckMetadata(t, held)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewBufferString("CREATE TABLE nested(v text NOT NULL); INSERT INTO nested VALUES ('replacement');")); err != nil {
			t.Fatal(err)
		}
		rangeConsumeHeld(t, held, nested)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM nested", []string{"v"}, []string{"TEXT"}, [][]any{{"replacement"}}))
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual emitted range-domain script PostgreSQL replay", err)
			}
			pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
			for _, step := range []fixture.Step{
				rangeSelect(pg, check.Query, check.Columns, check.Types, want, []bool{false, true, true}),
				rangeSelect(pg, vacant.Query, vacant.Columns, vacant.Types, nil, []bool{false, true}),
				rangeSelect(pg, nested.Query, nested.Columns, nested.Types, nestedWant, []bool{false, true, true}),
				rangeSelect(pg, required.Query, required.Columns, required.Types, requiredWant, []bool{false, false, false}),
			} {
				fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, step)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
