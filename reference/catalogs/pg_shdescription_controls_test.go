package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGShDescriptionControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgsd_control_role"})
	fixture.SchemaReferenceCleanup(t, "pgsd_controls")
	p := fixture.NewScenario(t)
	var nativeDump string
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			pgDescriptionPreserveSharedRole(t, p, backend)
			run := func(q string) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
			}
			run("CREATE SCHEMA pgsd_controls")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_control_role", "pgsd_controls")
			bootstrap := pgDescriptionSharedRoleID(t, p, backend)
			for _, q := range []string{"COMMENT ON ROLE postgres IS 'shared bootstrap control'", "COMMENT ON ROLE pgsd_control_role IS 'shared named control'", "CREATE TABLE pgsd_controls.sentinel(a INT)", "COMMENT ON TABLE pgsd_controls.sentinel IS 'local control'", "CREATE VIEW pgsd_controls.shared_view AS SELECT objoid,classoid,description,shobj_description(objoid,'pg_authid') AS inquiry FROM pg_catalog.pg_shdescription WHERE objoid='" + pgDescriptionOIDText(id) + "'::oid AND classoid=1260"} {
				run(q)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO pgsd_controls.sentinel VALUES(7)", 1))
			checkShared := func() {
				t.Helper()
				pgShDescriptionCheck(t, p, backend, backend.DB, bootstrap, new("shared bootstrap control"))
				pgShDescriptionCheck(t, p, backend, backend.DB, id, new("shared named control"))
			}
			checkLocal := func() {
				t.Helper()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT a FROM pgsd_controls.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				view := fixture.Select("SELECT * FROM pgsd_controls.shared_view", []string{"objoid", "classoid", "description", "inquiry"}, []string{"OID", "OID", "TEXT", "TEXT"}, [][]any{{id, int64(1260), "shared named control", "shared named control"}})
				fixture.PgDescriptionRows(t, p, backend, view)
				fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(view))
				localID := pgDescriptionRelationID(t, p, backend, "sentinel")
				fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{localID, int64(1259), int64(0), "local control"}}, localID))
			}
			checkShared()
			checkLocal()
			old, err := backend.DB.QueryContext(p.Ctx, "SELECT description FROM pg_catalog.pg_shdescription WHERE objoid=$1 AND classoid=1260", id)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "TEXT" {
				t.Fatal(metadata, err)
			}
			run("COMMENT ON ROLE pgsd_control_role IS 'replacement'")
			pgShDescriptionCheck(t, p, backend, backend.DB, id, new("replacement"))
			run("COMMENT ON ROLE pgsd_control_role IS 'shared named control'")
			// No native control call is outside this guarded phase. PostgreSQL
			// independently checks the same SQL state, not an invented control API.
			if !backend.PG {
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
					err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+tc.suffix))
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					fixture.AssertDiagnostic(t, err, &want)
					unchanged()
				}
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
					t.Fatal("Reset cancellation", err)
				}
				unchanged()
				if err := stuntgres.Dump(canceled, p.Native, &strings.Builder{}); !errors.Is(err, context.Canceled) {
					t.Fatal("Dump cancellation", err)
				}
				unchanged()
				if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
					t.Fatal("Restore cancellation", err)
				}
				unchanged()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				checkShared()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_class WHERE relname='sentinel'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				fixture.PgDescriptionRows(t, p, backend, fixture.Select("SELECT oid,relnatts FROM pg_catalog.pg_class WHERE oid=2396", []string{"oid", "relnatts"}, []string{"OID", "INT2"}, [][]any{{int64(2396), int64(3)}}))
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
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
	if nativeDump != "" && p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA pgsd_controls CASCADE"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, nativeDump); err != nil {
			t.Fatal("PG18 rejected actual native Dump", err)
		}
		pg := p.Backends()[1]
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Select("SELECT a FROM pgsd_controls.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}
