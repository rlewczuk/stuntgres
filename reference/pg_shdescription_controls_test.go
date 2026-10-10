package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferencePGShDescriptionControls(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgsd_control_role"})
	schemaReferenceCleanup(t, "pgsd_controls")
	p := newReferenceScenario(t)
	var nativeDump string
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			pgDescriptionPreserveSharedRole(t, p, backend)
			run := func(q string) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			run("CREATE SCHEMA pgsd_controls")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_control_role", "pgsd_controls")
			bootstrap := pgDescriptionSharedRoleID(t, p, backend)
			for _, q := range []string{"COMMENT ON ROLE postgres IS 'shared bootstrap control'", "COMMENT ON ROLE pgsd_control_role IS 'shared named control'", "CREATE TABLE pgsd_controls.sentinel(a INT)", "COMMENT ON TABLE pgsd_controls.sentinel IS 'local control'", "CREATE VIEW pgsd_controls.shared_view AS SELECT objoid,classoid,description,shobj_description(objoid,'pg_authid') AS inquiry FROM pg_catalog.pg_shdescription WHERE objoid='" + pgDescriptionOIDText(id) + "'::oid AND classoid=1260"} {
				run(q)
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO pgsd_controls.sentinel VALUES(7)", 1))
			checkShared := func() {
				t.Helper()
				pgShDescriptionCheck(t, p, backend, backend.db, bootstrap, new("shared bootstrap control"))
				pgShDescriptionCheck(t, p, backend, backend.db, id, new("shared named control"))
			}
			checkLocal := func() {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT a FROM pgsd_controls.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				view := referenceSelect("SELECT * FROM pgsd_controls.shared_view", []string{"objoid", "classoid", "description", "inquiry"}, []string{"OID", "OID", "TEXT", "TEXT"}, [][]any{{id, int64(1260), "shared named control", "shared named control"}})
				pgDescriptionRows(t, p, backend, view)
				pgDescriptionRows(t, p, backend, referencePrepared(view))
				localID := pgDescriptionRelationID(t, p, backend, "sentinel")
				pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{localID, int64(1259), int64(0), "local control"}}, localID))
			}
			checkShared()
			checkLocal()
			old, err := backend.db.QueryContext(p.ctx, "SELECT description FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", id)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal(metadata, err)
			}
			run("COMMENT ON ROLE pgsd_control_role IS 'replacement'")
			pgShDescriptionCheck(t, p, backend, backend.db, id, new("replacement"))
			run("COMMENT ON ROLE pgsd_control_role IS 'shared named control'")
			// No native control call is outside this guarded phase. PostgreSQL
			// independently checks the same SQL state, not an invented control API.
			if !backend.pg {
				dump := commentInquiryDump(t, p)
				for _, forbidden := range []string{"COMMENT ON ROLE", "shared bootstrap control", "shared named control", "INSERT INTO \"pg_catalog\".\"pg_shdescription\""} {
					if strings.Contains(dump, forbidden) {
						t.Fatal("shared state leaked into local Dump", forbidden, dump)
					}
				}
				if !strings.Contains(dump, "local control") {
					t.Fatal("local comment omitted", dump)
				}
				unchanged := func() {
					t.Helper()
					checkShared()
					checkLocal()
					if commentInquiryDump(t, p) != dump {
						t.Fatal("failed control changed complete local state")
					}
				}
				for _, tc := range []struct{ suffix, code, message string }{
					{"COMMENT ON ROLE pgsd_control_role IS 'forbidden';", "0A000", "restore accepts only database-local COMMENT targets"},
					{"COMMENT ON COLUMN pgsd_controls.sentinel.missing IS 'late';", "42703", "column \"missing\" of relation \"pgsd_controls.sentinel\" does not exist"},
				} {
					err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump+tc.suffix))
					want := controlsReferenceDiagnostic(tc.code, tc.message)
					referenceAssertDiagnostic(t, err, &want)
					unchanged()
				}
				canceled, cancel := context.WithCancel(p.ctx)
				cancel()
				if err := stuntgres.Reset(canceled, p.native); !errors.Is(err, context.Canceled) {
					t.Fatal("Reset cancellation", err)
				}
				unchanged()
				if err := stuntgres.Dump(canceled, p.native, &strings.Builder{}); !errors.Is(err, context.Canceled) {
					t.Fatal("Dump cancellation", err)
				}
				unchanged()
				if err := stuntgres.Restore(canceled, p.native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
					t.Fatal("Restore cancellation", err)
				}
				unchanged()
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				checkShared()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT count(*) FROM pg_catalog.pg_class WHERE relname='sentinel'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				pgDescriptionRows(t, p, backend, referenceSelect("SELECT oid,relnatts FROM pg_catalog.pg_class WHERE oid=2396", []string{"oid", "relnatts"}, []string{"OID", "INT2"}, [][]any{{int64(2396), int64(3)}}))
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				unchanged()
				nativeDump = dump
			}
			pgDescriptionRetainedText(t, old, "shared named control")
			if metadata[0].Name() != "description" || metadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal("retained descriptor changed", metadata)
			}
			checkShared()
			checkLocal()
			run("DROP ROLE pgsd_control_role")
		})
	}
	// Replay actual native-emitted local SQL only after both backend fixtures
	// have completed and only when the PostgreSQL backend is enabled.
	if nativeDump != "" && p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA pgsd_controls CASCADE"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, nativeDump); err != nil {
			t.Fatal("PG18 rejected actual native Dump", err)
		}
		pg := p.backends()[1]
		referenceRunStep(t, p.ctx, pg.db, pg.recorder, referenceSelect("SELECT a FROM pgsd_controls.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}
