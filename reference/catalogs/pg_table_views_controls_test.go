package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTableViewsControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgtv_control_owner"})
	fixture.SchemaReferenceCleanup(t, "pgtv_control_schema")
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	for _, q := range []string{"CREATE ROLE pgtv_control_owner", "CREATE SCHEMA pgtv_control_schema AUTHORIZATION pgtv_control_owner", "SET SESSION AUTHORIZATION pgtv_control_owner", "CREATE TABLE pgtv_control_schema.pgtv_never(n INT)", "CREATE TABLE pgtv_control_schema.pgtv_current(n INT UNIQUE)", "CREATE TABLE pgtv_control_schema.pgtv_former(n INT)", "CREATE INDEX pgtv_former_index ON pgtv_control_schema.pgtv_former(n)", "DROP INDEX pgtv_control_schema.pgtv_former_index", "CREATE VIEW pgtv_control_schema.pgtv_control_view AS SELECT n FROM pgtv_control_schema.pgtv_current", "RESET SESSION AUTHORIZATION"} {
		fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec("INSERT INTO pgtv_control_schema.pgtv_current VALUES(9)", 1))
	dump := fixture.ViewDump(t, p)
	if strings.Contains(string(dump), "pg_tables") || strings.Contains(string(dump), "pg_views") || strings.Contains(string(dump), "pgtv_former_index") {
		t.Fatal("derived summaries/history scaffold leaked into Dump", string(dump))
	}
	query := "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv_control_schema' ORDER BY tablename"
	expected := func(history bool) [][]any {
		return [][]any{pgTableRow("pgtv_control_schema", "pgtv_current", "pgtv_control_owner", true), pgTableRow("pgtv_control_schema", "pgtv_former", "pgtv_control_owner", history), pgTableRow("pgtv_control_schema", "pgtv_never", "pgtv_control_owner", false)}
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			check := func(db fixture.Executor, history bool) {
				fixture.RunStep(t, p.Ctx, db, b.Recorder, pgTableViewSelect(b, query, pgTableColumns, pgTableTypes, expected(history)))
			}
			data := func(db fixture.Executor) {
				fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Select("SELECT n FROM pgtv_control_schema.pgtv_control_view", []string{"n"}, []string{"INT4"}, [][]any{{int64(9)}}))
			}
			builtin := func(db fixture.Executor) {
				fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Select("SELECT viewname FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND "+pgTableViewsNames("viewname", pgViewBuiltinNames)+" ORDER BY viewname", []string{"viewname"}, []string{"NAME"}, [][]any{{"pg_indexes"}, {"pg_prepared_xacts"}, {"pg_tables"}, {"pg_views"}}))
			}
			if b.PG {
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE ROLE pgtv_control_owner", 0))
				if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG rejected actual native SQL Dump", err)
				}
				check(conn, false)
				data(conn)
				builtin(conn)
				return
			}
			held := fixture.ViewHold(t, p, b, query)
			check(b.DB, true)
			check(held, true)
			data(b.DB)
			retainedQuery := "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv_control_schema' AND tablename='pgtv_former'"
			old, err := b.DB.QueryContext(p.Ctx, retainedQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			types, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(dump)+"CREATE SEQUENCE pgtv_bad CACHE 0;")); err == nil {
				t.Fatal("invalid Restore succeeded")
			} else {
				want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
				fixture.AssertDiagnostic(t, err, &want)
			}
			check(b.DB, true)
			data(b.DB)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(b.DB, true)
			data(b.DB)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTableViewSelect(b, query, pgTableColumns, pgTableTypes, nil))
			builtin(b.DB)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(b.DB, false)
			check(held, false)
			data(b.DB)
			builtin(b.DB)
			pgViewSummary(t, p, b, b.DB, "pgtv_control_schema", "pgtv_control_view", "pgtv_control_owner", "pgtv_current")
			if redump := fixture.ViewDump(t, p); !bytes.Equal(redump, dump) {
				t.Fatal("logical redump changed", string(dump), string(redump))
			}
			pgTableRetained(t, old, types, pgTableRow("pgtv_control_schema", "pgtv_former", "pgtv_control_owner", true))
		})
	}
}
