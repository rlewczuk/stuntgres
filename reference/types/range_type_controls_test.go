package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "range_type_archive")
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE SCHEMA range_type_archive`, 0))
		run(fixture.Exec(rangeTypeClass("archive_range_ops", "btint4cmp"), 0))
		run(fixture.Exec(`CREATE TYPE public.custom_span AS RANGE(SUBTYPE=int4,SUBTYPE_OPCLASS=public.archive_range_ops,MULTIRANGE_TYPE_NAME=range_type_archive.custom_spans)`, 0))
		run(fixture.Exec(`CREATE TABLE range_data(k integer NOT NULL,r public.custom_span,m range_type_archive.custom_spans)`, 0))
		rangeTypeSeed(run)
		for _, q := range []string{
			`CREATE TYPE archive_label AS ENUM('z','a')`,
			`CREATE TYPE archive_enum_range AS RANGE(SUBTYPE=archive_label)`,
			`CREATE COLLATION archive_collation FROM pg_catalog."C"`,
			`CREATE TYPE archive_text_range AS RANGE(SUBTYPE=text,COLLATION=archive_collation)`,
			`CREATE TYPE archive_float_range AS RANGE(SUBTYPE=float8,SUBTYPE_DIFF=pg_catalog.float8mi)`,
			`CREATE TABLE archive_providers(e archive_enum_range,t archive_text_range,f archive_float_range)`,
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO archive_providers VALUES('[z,a]','[A,a]','[1.5,3.25]')`, 1))
		check := func(backend fixture.Backend, exec fixture.Executor) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, exec, backend.Recorder, rangeTypeData(backend, rangeTypeRows()))
			fixture.RunStep(t, p.Ctx, exec, backend.Recorder, rangeTypeSelect(backend, `SELECT e,t,f FROM archive_providers`, []string{"e", "t", "f"}, []string{"ARCHIVE_ENUM_RANGE", "ARCHIVE_TEXT_RANGE", "ARCHIVE_FLOAT_RANGE"}, [][]any{{"[z,a]", "[A,a]", "[1.5,3.25]"}}, []bool{true, true, true}))
			fixture.RunStep(t, p.Ctx, exec, backend.Recorder, fixture.Select(`SELECT rngsubtype='int4'::regtype,rngmultitypid='range_type_archive.custom_spans'::regtype,rngsubopc=(SELECT oid FROM pg_opclass WHERE opcnamespace='public'::regnamespace AND opcname='archive_range_ops' AND opcmethod=403),rngcanonical::oid::bigint FROM pg_range WHERE rngtypid='custom_span'::regtype`, []string{"?column?", "?column?", "?column?", "rngcanonical"}, []string{"BOOL", "BOOL", "BOOL", "INT8"}, [][]any{{true, true, true, int64(0)}}))
		}
		check(b, conn)
		if b.PG {
			return
		} // Native controls have no PostgreSQL equivalent.
		held, err := conn.QueryContext(p.Ctx, rangeTypeData(b, rangeTypeRows()).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		assertDump := func(pool *sql.DB) {
			t.Helper()
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, pool, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				t.Fatalf("dump changed: %v\n%s", err, after.String())
			}
		}
		target := fixture.NewFixture(t, false, true)
		targetBackend := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
		targetOwner, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetOwner.Close()
		fixture.RunStep(t, target.Ctx, targetOwner, target.MemoryRecord, fixture.Exec(`CREATE TEMP TABLE displaced_identity(k integer)`, 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(rangeTypeClass("displaced_ops", "int4mi"), 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("fresh destination", err)
		}
		check(targetBackend, target.Memory)
		fixture.RunStep(t, target.Ctx, targetOwner, target.MemoryRecord, fixture.Select(`SELECT k FROM displaced_identity`, []string{"k"}, []string{"INT4"}, nil))
		assertDump(target.Native)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(fixture.Exec(`INSERT INTO range_data VALUES($1,$2,$3)`, 1, int64(6), "[6,7]", "{[6,7]}")))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, rangeTypeData(targetBackend, append(rangeTypeRows(), []any{int64(6), "[6,7]", "{[6,7]}"})))
		check(b, conn) // Destination mutation must not affect the source engine.
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{`CREATE TYPE late_invalid AS RANGE(SUBTYPE=int4,SUBTYPE_OPCLASS=text_ops);`, `INSERT INTO range_data VALUES(9,'[4,1]','{}');`} {
			old, err := target.Memory.QueryContext(target.Ctx, rangeTypeData(targetBackend, rangeTypeRows()).Query)
			if err != nil {
				t.Fatal(err)
			}
			want := fixture.ControlsReferenceDiagnostic("42804", `operator class "text_ops" does not accept data type integer`)
			if strings.HasPrefix(suffix, "INSERT") {
				want = fixture.ControlsReferenceDiagnostic("22000", "range lower bound must be less than or equal to range upper bound")
				script := dump.String() + suffix
				want.Position = strconv.Itoa(utf8.RuneCountInString(script[:strings.LastIndex(script, "'[4,1]'")]) + 1)
			}
			fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String()+suffix)), &want)
			fixture.RangeConsumeHeld(t, old, rangeTypeData(targetBackend, rangeTypeRows()))
			check(targetBackend, target.Memory)
			assertDump(target.Native)
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, call := range []func() error{
			func() error { return stuntgres.Dump(canceled, p.Native, &bytes.Buffer{}) },
			func() error { return stuntgres.Reset(canceled, p.Native) },
			func() error { return stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())) },
		} {
			err := call()
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("ordinary cancellation lost", err)
			}
		}
		for _, prepared := range []bool{false, true} {
			name := "canceled_direct_span"
			if prepared {
				name = "canceled_prepared_span"
			}
			q := `CREATE TYPE public.` + name + ` AS RANGE(SUBTYPE=int4)`
			var callErr error
			if prepared {
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				_, callErr = stmt.ExecContext(canceled)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				_, callErr = conn.ExecContext(canceled, q)
			}
			var state interface{ SQLState() string }
			if !errors.Is(callErr, context.Canceled) || errors.As(callErr, &state) {
				t.Fatal("ordinary DDL cancellation lost", callErr)
			}
			rangeTypeAbsent(run, name, name+"_multirange", "_"+name, "_"+name+"_multirange")
			run(fixture.Exec(q, 0))
			run(fixture.Exec("DROP TYPE public."+name+" CASCADE", 0))
		}
		assertDump(p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.RangeConsumeHeld(t, held, rangeTypeData(b, rangeTypeRows()))
		check(b, conn)
		assertDump(p.Native)
		// Dependency closure must survive destination-provider remapping.
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(`DROP OPERATOR CLASS public.archive_range_ops USING btree CASCADE`, 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select(`SELECT typname FROM pg_type WHERE typnamespace IN ('public'::regnamespace,'range_type_archive'::regnamespace) AND typname IN ('custom_span','_custom_span','custom_spans','_custom_spans') ORDER BY typname`, []string{"typname"}, []string{"NAME"}, nil))
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA IF EXISTS range_type_archive CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual emitted SQL PostgreSQL replay", err)
			}
			check(fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}, p.Postgres)
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA range_type_archive CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
		}
	})
}
