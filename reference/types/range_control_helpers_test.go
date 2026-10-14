package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type rangeStagedFailure struct {
	script string
	diagnostic fixture.Diagnostic
}

// Native control APIs have no PostgreSQL equivalent. Their actual emitted SQL
// is replayed on the oracle with the same independent rangeSelect contracts.
func rangeControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, checks []fixture.Step, nullable [][]bool, failures []rangeStagedFailure) {
	t.Helper()
	if b.PG { return }
	check := checks[0]
	held, err := b.DB.QueryContext(p.Ctx, check.Query)
	if err != nil { t.Fatal(err) }
	defer held.Close()
	check.CheckMetadata(t, held)
	metadata, err := held.ColumnTypes()
	if err != nil { t.Fatal(err) }
	var dump bytes.Buffer
	// Pin the control pool to its styled session: Dump must use ISO literals
	// even when its own live temporal presentation is non-ISO.
	p.Native.SetMaxOpenConns(1)
	if _, err := p.Native.ExecContext(p.Ctx, "SET DateStyle = 'German, DMY'"); err != nil { t.Fatal(err) }
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
	if _, err := p.Native.ExecContext(p.Ctx, "SET DateStyle = 'ISO, MDY'"); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal("same-engine Restore", err) }
	rangeConsumeHeld(t, held, check)
	rangeRetainedMetadata(t, metadata, check, nullable[0])
	for _, step := range checks { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("same-engine redump", err, redump.String()) }
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal("Reset/Restore", err) }
	for _, step := range checks { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal("fresh Driver Restore", err) }
	for _, step := range checks {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(step))
	}
	redump.Reset()
	if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("fresh Driver redump", err, redump.String()) }
	for _, failure := range failures {
		held, err := target.Memory.QueryContext(target.Ctx, check.Query)
		if err != nil { t.Fatal(err) }
		defer held.Close()
		check.CheckMetadata(t, held)
		metadata, err := held.ColumnTypes()
		if err != nil { t.Fatal(err) }
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, bytes.NewBufferString(failure.script)), &failure.diagnostic)
		rangeConsumeHeld(t, held, check)
		rangeRetainedMetadata(t, metadata, check, nullable[0])
		for _, step := range checks { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step) }
		redump.Reset()
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("failed staged Restore published declarations/data", failure.script, err) }
	}
	canceled, cancel := context.WithCancel(target.Ctx); cancel()
	for _, call := range []func() error{
		func() error { return stuntgres.Reset(canceled, target.Native) },
		func() error { return stuntgres.Restore(canceled, target.Native, bytes.NewReader(dump.Bytes())) },
		func() error { return stuntgres.Dump(canceled, target.Native, &bytes.Buffer{}) },
	} {
		err := call()
		var state interface{ SQLState() string }
		if !errors.Is(err, context.Canceled) || errors.As(err, &state) { t.Fatal("control cancellation crossed SQL boundary", err) }
	}
	for _, step := range checks { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step) }
	held, err = target.Memory.QueryContext(target.Ctx, check.Query)
	if err != nil { t.Fatal(err) }
	defer held.Close()
	check.CheckMetadata(t, held)
	metadata, err = held.ColumnTypes()
	if err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewBufferString("CREATE TABLE r(v text NOT NULL); INSERT INTO r VALUES ('replacement');")); err != nil { t.Fatal(err) }
	rangeConsumeHeld(t, held, check)
	rangeRetainedMetadata(t, metadata, check, nullable[0])
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM r", []string{"v"}, []string{"TEXT"}, [][]any{{"replacement"}}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil { t.Fatal("actual emitted SQL PostgreSQL replay", err, dump.String()) }
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		for i, step := range checks {
			s := rangeSelect(pg, step.Query, step.Columns, step.Types, step.Rows, nullable[i])
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, s)
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Prepared(s))
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
	}
}

func rangeRetainedMetadata(t *testing.T, metadata []*sql.ColumnType, check fixture.Step, nullable []bool) {
	t.Helper()
	for i, typ := range metadata {
		if typ.Name() != check.Columns[i] || typ.DatabaseTypeName() != check.Types[i] {
			t.Fatal("retained replacement metadata", typ)
		}
		if !rangeTransportType(check.Types[i]) { continue }
		if typ.ScanType() != reflect.TypeFor[string]() { t.Fatal("retained range ScanType", typ) }
		n, known := typ.Nullable()
		if nullable == nil {
			if n || known { t.Fatal("retained computed range nullable", n, known) }
		} else if !known || n != nullable[i] {
			t.Fatal("retained declared range nullable", n, known)
		}
		if n, known := typ.Length(); n != 0 || known { t.Fatal("retained range length", n, known) }
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known { t.Fatal("retained range precision", precision, scale, known) }
	}
}
