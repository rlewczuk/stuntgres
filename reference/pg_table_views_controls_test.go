package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferencePGTableViewsControls(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgtv_control_owner"})
	schemaReferenceCleanup(t, "pgtv_control_schema")
	p := newReferenceScenario(t)
	native := p.backends()[0]
	for _, q := range []string{"CREATE ROLE pgtv_control_owner", "CREATE SCHEMA pgtv_control_schema AUTHORIZATION pgtv_control_owner", "SET SESSION AUTHORIZATION pgtv_control_owner", "CREATE TABLE pgtv_control_schema.pgtv_never(n INT)", "CREATE TABLE pgtv_control_schema.pgtv_current(n INT UNIQUE)", "CREATE TABLE pgtv_control_schema.pgtv_former(n INT)", "CREATE INDEX pgtv_former_index ON pgtv_control_schema.pgtv_former(n)", "DROP INDEX pgtv_control_schema.pgtv_former_index", "CREATE VIEW pgtv_control_schema.pgtv_control_view AS SELECT n FROM pgtv_control_schema.pgtv_current", "RESET SESSION AUTHORIZATION"} {
		referenceRunStep(t, p.ctx, native.db, native.recorder, referenceExec(q, 0))
	}
	referenceRunStep(t, p.ctx, native.db, native.recorder, referenceExec("INSERT INTO pgtv_control_schema.pgtv_current VALUES(9)", 1))
	dump := referenceViewDump(t, p)
	if strings.Contains(string(dump), "pg_tables") || strings.Contains(string(dump), "pg_views") || strings.Contains(string(dump), "pgtv_former_index") {
		t.Fatal("derived summaries/history scaffold leaked into Dump", string(dump))
	}
	query := "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv_control_schema' ORDER BY tablename"
	expected := func(history bool) [][]any {
		return [][]any{pgTableRow("pgtv_control_schema", "pgtv_current", "pgtv_control_owner", true), pgTableRow("pgtv_control_schema", "pgtv_former", "pgtv_control_owner", history), pgTableRow("pgtv_control_schema", "pgtv_never", "pgtv_control_owner", false)}
	}
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			check := func(db referenceExecutor, history bool) {
				referenceRunStep(t, p.ctx, db, b.recorder, pgTableViewSelect(b, query, pgTableColumns, pgTableTypes, expected(history)))
			}
			data := func(db referenceExecutor) {
				referenceRunStep(t, p.ctx, db, b.recorder, referenceSelect("SELECT n FROM pgtv_control_schema.pgtv_control_view", []string{"n"}, []string{"INT4"}, [][]any{{int64(9)}}))
			}
			builtin := func(db referenceExecutor) {
				referenceRunStep(t, p.ctx, db, b.recorder, referenceSelect("SELECT viewname FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND "+pgTableViewsNames("viewname", pgViewBuiltinNames)+" ORDER BY viewname", []string{"viewname"}, []string{"NAME"}, [][]any{{"pg_indexes"}, {"pg_prepared_xacts"}, {"pg_tables"}, {"pg_views"}}))
			}
			if b.pg {
				conn := sequenceCoreConn(t, p.ctx, b.db)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("CREATE ROLE pgtv_control_owner", 0))
				if _, err := conn.ExecContext(p.ctx, string(dump)); err != nil {
					t.Fatal("PG rejected actual native SQL Dump", err)
				}
				check(conn, false)
				data(conn)
				builtin(conn)
				return
			}
			held := referenceViewHold(t, p, b, query)
			check(b.db, true)
			check(held, true)
			data(b.db)
			retainedQuery := "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv_control_schema' AND tablename='pgtv_former'"
			old, err := b.db.QueryContext(p.ctx, retainedQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			types, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(string(dump)+"CREATE SEQUENCE pgtv_bad CACHE 0;")); err == nil {
				t.Fatal("invalid Restore succeeded")
			} else {
				want := controlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
				referenceAssertDiagnostic(t, err, &want)
			}
			check(b.db, true)
			data(b.db)
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(b.db, true)
			data(b.db)
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, held, b.recorder, pgTableViewSelect(b, query, pgTableColumns, pgTableTypes, nil))
			builtin(b.db)
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(b.db, false)
			check(held, false)
			data(b.db)
			builtin(b.db)
			pgViewSummary(t, p, b, b.db, "pgtv_control_schema", "pgtv_control_view", "pgtv_control_owner", "pgtv_current")
			if redump := referenceViewDump(t, p); !bytes.Equal(redump, dump) {
				t.Fatal("logical redump changed", string(dump), string(redump))
			}
			pgTableRetained(t, old, types, pgTableRow("pgtv_control_schema", "pgtv_former", "pgtv_control_owner", true))
		})
	}
}
