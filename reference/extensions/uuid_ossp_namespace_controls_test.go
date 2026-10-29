package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func uuidOsspNamespaceConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	uuidOsspNamespaceInstall(t, p, c, r)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	for _, tc := range uuidOsspNamespaces {
		call := "uuid_ossp_part." + tc.name + "()"
		run(fixture.Exec("CREATE TABLE "+tc.name+"_saved(id integer,u uuid DEFAULT "+call+",g uuid GENERATED ALWAYS AS ("+call+") STORED)", 0))
		run(fixture.Exec("CREATE INDEX "+tc.name+"_index ON "+tc.name+"_saved (("+call+"))", 0))
		run(fixture.Exec("INSERT INTO "+tc.name+"_saved(id) VALUES(1),(2)", 2))
		run(fixture.Exec("INSERT INTO "+tc.name+"_saved(id,u) VALUES(3,NULL)", 1))
		run(fixture.Exec("CREATE VIEW "+tc.name+"_view AS SELECT id,"+call+" AS u FROM "+tc.name+"_saved", 0))
		uuidOsspNamespaceConsumerRead(t, p, c, r, tc.name, tc.value)
		run(fixture.Select("SELECT "+call+" AS u,count(*) FROM "+tc.name+"_saved WHERE id<3 GROUP BY "+call, []string{"u", "count"}, []string{"UUID", "INT8"}, [][]any{{tc.value, int64(2)}}))
	}
	// Existing V4 remains volatile, and is still forbidden in immutable consumers.
	d := fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")
	run(fixture.ErrorDiagnostic("CREATE TABLE namespace_bad(u uuid GENERATED ALWAYS AS (uuid_ossp_part.uuid_generate_v4()) STORED)", d))
}

func uuidOsspNamespaceConsumerRead(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, name, value string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT u,g FROM "+name+"_saved ORDER BY id", []string{"u", "g"}, []string{"UUID", "UUID"}, [][]any{{value, value}, {value, value}, {nil, value}})
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, r, s)
		s = fixture.Select("SELECT u FROM "+name+"_view ORDER BY id", []string{"u"}, []string{"UUID"}, [][]any{{value}, {value}, {value}})
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
}

func TestReferenceUUIDOsspNamespaceConsumers(t *testing.T) {
	uuidOsspNamespaceCase(t, uuidOsspNamespaceConsumers)
}

func uuidOsspNamespaceControls(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	uuidOsspNamespaceConsumers(t, p, c, r, pg)
	uuidOsspNamespaceInventory(t, p, c)
	q := "SELECT uuid_ossp_part.uuid_ns_x500() AS u"
	held, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	h := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
	fixture.RunStep(t, p.Ctx, h, r, uuidOsspNamespaceSelect(q, uuidOsspNamespaces[4].value))
	if pg {
		return
	}
	old, err := c.QueryContext(p.Ctx, "SELECT u FROM uuid_nil_saved ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	cols, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for _, want := range []any{uuidOsspNamespaces[0].value, uuidOsspNamespaces[0].value, nil} {
		if !old.Next() {
			t.Fatal("retained row missing", old.Err())
		}
		var got any
		if err := old.Scan(&got); err != nil || got != want {
			t.Fatal("retained UUID", got, want, err)
		}
	}
	if old.Next() || old.Err() != nil || len(cols) != 1 || cols[0].DatabaseTypeName() != "UUID" {
		t.Fatal("retained metadata/tail", old.Err())
	}
	old.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	post := func(target *sql.Conn, rec *fixture.Recorder) {
		uuidOsspNamespaceInventory(t, p, target)
		for _, tc := range uuidOsspNamespaces {
			uuidOsspNamespaceConsumerRead(t, p, target, rec, tc.name, tc.value)
		}
	}
	post(c, r)
	fixture.RunStep(t, p.Ctx, h, r, uuidOsspNamespaceSelect(q, uuidOsspNamespaces[4].value))
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	for _, bad := range []string{dump + "\nINVALID SQL;", strings.Replace(dump, "VERSION '1.1'", "VERSION 'missing'", 1), strings.ReplaceAll(dump, "uuid_ns_x500", "missing_namespace"), strings.Replace(dump, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1)} {
		if bad == dump {
			t.Fatal("bad restore fixture unchanged")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("bad restore accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed restore published state")
		}
		post(c, r)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("canceled restore", err)
	}
	other, target, rec := pgcryptoControlDestination(t, p, "uuid_namespace_destination")
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	post(target, rec)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO uuid_ns_dns_saved(id) VALUES(4)", 1))
	fixture.RunStep(t, p.Ctx, c, r, uuidOsspNamespaceSelect("SELECT u FROM uuid_ns_dns_saved WHERE id=4", uuidOsspNamespaces[1].value))
}

func TestReferenceUUIDOsspNamespaceControls(t *testing.T) {
	uuidOsspNamespaceCase(t, uuidOsspNamespaceControls)
}

func TestReferenceUUIDOsspNamespaceFeatureSmoke(t *testing.T) {
	uuidOsspNamespaceCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspNamespaceControls(t, p, c, r, pg)
		for _, tc := range uuidOsspNamespaces {
			q := "SELECT uuid_ossp_part." + tc.name + "() AS u"
			fixture.RunStep(t, p.Ctx, c, r, uuidOsspNamespaceSelect(q, tc.value))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(uuidOsspNamespaceSelect(q, tc.value)))
		}
	})
}
