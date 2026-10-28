package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitstringStoredFormatReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE bs_format_default(v TEXT DEFAULT format('%s',B'001'),h TEXT DEFAULT format('%s',X'0f'),e TEXT DEFAULT format('%s',B''))", 0))
	p.Step(t, fixture.Exec("INSERT INTO bs_format_default DEFAULT VALUES", 1))
	p.Step(t, fixture.Exec("CREATE VIEW bs_format_view AS SELECT format('%s',B'001') AS v,format('%s',X'0f') AS h,format('%s',B'') AS e FROM bs_format_default", 0))
	check := func(backend fixture.Backend, count int) {
		want := make([][]any, count)
		for i := range count {
			want[i] = []any{"001", "00001111", ""}
		}
		for _, table := range []string{"bs_format_default", "bs_format_view"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT v,h,e FROM "+table, []string{"v", "h", "e"}, []string{"TEXT", "TEXT", "TEXT"}, want)
				s.Prepared, s.Bag = prepared, true
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.FormatReferenceMetadata(t, backend.PG, rows, true) }
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
		}
	}
	for _, backend := range p.Backends() {
		check(backend, 1)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{"B'001'", "B'00001111'", "B''"} {
		if !strings.Contains(dump.String(), literal) {
			t.Fatal("missing stored bit literal", literal, dump.String())
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("native replay of stored bit format", err, dump.String())
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("stored format redump changed", redump.String(), dump.String())
	}
	// BIT literals already carry their concrete identity. A serializer must not
	// manufacture explicit BIT casts that the intentionally bounded binder rejects.
	if strings.Contains(dump.String(), "CAST(B'") {
		t.Fatal("Dump manufactures BIT cast", dump.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW bs_format_view;DROP TABLE bs_format_default"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL replay of actual native Dump", err, dump.String())
		}
	}
	for _, backend := range p.Backends() {
		check(backend, 1)
		// Stored values alone do not prove that replay preserved the defaults.
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO bs_format_default DEFAULT VALUES", 1)))
		check(backend, 2)
	}
}
