package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgLanguageDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	b := p.Backends()[0]
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE pglang_control_sentinel(id INT,words TEXT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pglang_control_sentinel VALUES(7,'kept')", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	return dump.String()
}

func pgLanguageSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	s := fixture.Select("SELECT * FROM pglang_control_sentinel", []string{"id", "words"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
}

func pgLanguageLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, dump string) {
	t.Helper()
	if b.PG {
		pgLanguageInventory(t, p, b)
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		if _, err := conn.ExecContext(p.Ctx, dump); err != nil {
			t.Fatal("PG rejected actual native dump", err)
		}
		pgLanguageSentinel(t, p, b, conn)
		pgLanguageInventory(t, p, b)
		pgLanguageCatalogLinks(t, p, b)
		return
	}
	old, err := b.DB.QueryContext(p.Ctx, pgLanguageProbe, "internal")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgLanguageMetadata(t, old, false)
	columns, err := old.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, pgLanguageProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgLanguageProbe}
	catalog := func() {
		pgLanguageInventory(t, p, b)
		pgLanguageCatalogLinks(t, p, b)
		pgLanguageHeld(t, p, b, held)
	}
	check := func() {
		catalog()
		pgLanguageSentinel(t, p, b, b.DB)
	}
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"CREATE SEQUENCE pglang_control_invalid CACHE 0;"))
	want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
	fixture.AssertDiagnostic(t, err, &want)
	check()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Reset(ctx, p.Native); !errors.Is(err, context.Canceled) {
		t.Fatal("Reset cancellation", err)
	}
	if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
		t.Fatal("Restore cancellation", err)
	}
	check()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	catalog()
	s := fixture.ImplicitError("SELECT * FROM pglang_control_sentinel", fixture.Diagnostic{Code: "42P01", Message: "relation \"pglang_control_sentinel\" does not exist", Position: "15"})
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	check()
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != dump {
		t.Fatal("deterministic restored dump", after.String(), err)
	}
	if !old.Next() {
		t.Fatal("retained populated language cursor", old.Err())
	}
	cells, dest := make([]any, 9), make([]any, 9)
	for i := range dest {
		dest[i] = &cells[i]
	}
	if err := old.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cells, pgLanguageInternal()[0]) {
		t.Fatal("retained language row", cells)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained language EOF", old.Err())
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, pgLanguageColumns) {
		t.Fatal("retained language names", columns)
	}
	pgLanguageSavedMetadata(t, types, false)
}

func TestReferencePGLanguageLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	dump := pgLanguageDump(t, p)
	pgLanguageBackends(t, p, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgLanguageLifecycle(t, p, b, dump)
	})
}
