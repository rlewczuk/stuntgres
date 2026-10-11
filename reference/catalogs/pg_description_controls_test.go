package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDescriptionControls(t *testing.T)     { pgDescriptionControlsScenario(t) }
func TestReferencePGDescriptionFeatureSmoke(t *testing.T) { pgDescriptionControlsScenario(t) }

func pgDescriptionControlsScenario(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgd_controls")
	p := fixture.NewScenario(t)
	if p.Postgres != nil {
		t.Cleanup(func() {
			if _, err := p.Postgres.ExecContext(context.Background(), "COMMENT ON SCHEMA pg_catalog IS 'system catalog schema'"); err != nil {
				t.Error(err)
			}
		})
	}
	retained := map[string]*sql.Rows{}
	for _, backend := range p.Backends() {
		func() {
			pgDescriptionPreserveSharedRole(t, p, backend)
			run := func(q string) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
			}
			empty := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE FALSE", [][]any{})
			empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.PG) }
			fixture.PgDescriptionRows(t, p, backend, empty)
			fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(empty))
			identity := fixture.Select("SELECT oid,relnatts FROM pg_catalog.pg_class WHERE oid=2609", []string{"oid", "relnatts"}, []string{"OID", "INT2"}, [][]any{{int64(2609), int64(4)}})
			fixture.PgDescriptionRows(t, p, backend, identity)
			for _, seed := range []struct {
				oid, class int64
				text       string
			}{{114, 1247, "JSON stored as text"}, {4518, 2607, "conversion for LATIN1 to UTF8"}} {
				s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=$2", [][]any{{seed.oid, seed.class, int64(0), seed.text}}, seed.oid, seed.class)
				fixture.PgDescriptionRows(t, p, backend, s)
				fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(s))
			}
			for _, q := range []string{"COMMENT ON ROLE postgres IS 'control shared role'", "CREATE SCHEMA pgd_controls", "COMMENT ON SCHEMA pgd_controls IS 'control schema'", "CREATE TABLE pgd_controls.pgd_control_table(a INT,b INT,c TEXT)", "CREATE SEQUENCE pgd_controls.pgd_control_sequence", "COMMENT ON SEQUENCE pgd_controls.pgd_control_sequence IS 'control sequence'", "COMMENT ON TABLE pgd_controls.pgd_control_table IS 'control whole'", "COMMENT ON COLUMN pgd_controls.pgd_control_table.c IS 'control third'", "ALTER TABLE pgd_controls.pgd_control_table DROP b"} {
				run(q)
			}
			oid := pgDescriptionRelationID(t, p, backend, "pgd_control_table")
			pgDescriptionRelationBag(t, p, backend, oid, [][]any{{oid, int64(1259), int64(0), "control whole"}, {oid, int64(1259), int64(3), "control third"}})
			query := "SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259"
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: query}
			run("COMMENT ON TABLE pgd_controls.pgd_control_table IS 'replacement'")
			fixture.PgDescriptionExecutorRows(t, p, backend, held, pgDescriptionSelect(query, [][]any{{oid, int64(1259), int64(0), "replacement"}, {oid, int64(1259), int64(3), "control third"}}, oid))
			run("COMMENT ON TABLE pgd_controls.pgd_control_table IS NULL")
			fixture.PgDescriptionExecutorRows(t, p, backend, held, pgDescriptionSelect(query, [][]any{{oid, int64(1259), int64(3), "control third"}}, oid))
			run("COMMENT ON TABLE pgd_controls.pgd_control_table IS 'control whole'")
			rows, err := backend.DB.QueryContext(p.Ctx, "SELECT description FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259 AND objsubid=0", oid)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { rows.Close() })
			retained[backend.Name] = rows
			roleOID := pgDescriptionSharedRoleID(t, p, backend)
			fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, roleOID))
			commentInquiryStep(t, p, backend, backend.DB, fixture.Select("SELECT shobj_description($1,'pg_authid')", []string{"shobj_description"}, []string{"TEXT"}, [][]any{{"control shared role"}}, roleOID))
			run("COMMENT ON SCHEMA pg_catalog IS 'control catalog override'")
			run("COMMENT ON SCHEMA public IS NULL")
		}()
	}
	dump := commentInquiryDump(t, p)
	for _, fragment := range []string{"COMMENT ON TABLE", "COMMENT ON COLUMN", "COMMENT ON SCHEMA", "COMMENT ON SEQUENCE", "control third", "control catalog override"} {
		if !strings.Contains(dump, fragment) {
			t.Fatal("missing owning dump statement", fragment, dump)
		}
	}
	if strings.Contains(dump, "INSERT INTO \"pg_catalog\".\"pg_description\"") || strings.Contains(dump, "conversion for LATIN1") || strings.Contains(dump, "JSON stored as text") || strings.Contains(dump, "control shared role") {
		t.Fatal("projected/shared seeds leaked into Dump", dump)
	}
	native := p.Backends()[0]
	checkNative := func() {
		t.Helper()
		oid := pgDescriptionRelationID(t, p, native, "pgd_control_table")
		pgDescriptionRelationBag(t, p, native, oid, [][]any{{oid, int64(1259), int64(0), "control whole"}, {oid, int64(1259), int64(3), "control third"}})
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"COMMENT ON COLUMN pgd_controls.pgd_control_table.missing IS 'bad';")); err == nil {
		t.Fatal("late Restore unexpectedly succeeded")
	} else {
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" of relation \"pgd_controls.pgd_control_table\" does not exist"})
	}
	checkNative()
	if commentInquiryDump(t, p) != dump {
		t.Fatal("late failing Restore changed complete native catalog")
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
		t.Fatal("Restore cancellation", err)
	}
	checkNative()
	if commentInquiryDump(t, p) != dump {
		t.Fatal("canceled Restore changed complete native catalog")
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_description", []string{"count"}, []string{"INT8"}, [][]any{{int64(165)}}))
	fixture.PgDescriptionRows(t, p, native, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE classoid=2615", [][]any{{int64(11), int64(2615), int64(0), "system catalog schema"}, {int64(2200), int64(2615), int64(0), "standard public schema"}}))
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	if commentInquiryDump(t, p) != dump {
		t.Fatal("noncanonical logical roundtrip", commentInquiryDump(t, p), dump)
	}
	if p.Postgres != nil {
		conn, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, err := conn.ExecContext(p.Ctx, "DROP SCHEMA pgd_controls CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(p.Ctx, dump); err != nil {
				t.Fatal("PG18 rejected actual native Dump", err)
			}
		}()
	}
	for _, backend := range p.Backends() {
		oid := pgDescriptionRelationID(t, p, backend, "pgd_control_table")
		pgDescriptionRelationBag(t, p, backend, oid, [][]any{{oid, int64(1259), int64(0), "control whole"}, {oid, int64(1259), int64(2), "control third"}})
		sequence := pgDescriptionRelationID(t, p, backend, "pgd_control_sequence")
		pgDescriptionRelationBag(t, p, backend, sequence, [][]any{{sequence, int64(1259), int64(0), "control sequence"}})
		var namespace int64
		if err := backend.DB.QueryRowContext(p.Ctx, "SELECT objoid FROM pg_catalog.pg_description WHERE classoid=2615 AND description='control schema'").Scan(&namespace); err != nil {
			t.Fatal(err)
		}
		fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE classoid=2615 AND objoid=$1", [][]any{{namespace, int64(2615), int64(0), "control schema"}}, namespace))
		fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE classoid=2615 AND objoid=11", [][]any{{int64(11), int64(2615), int64(0), "control catalog override"}}))
		fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE classoid=2615 AND description='standard public schema'", [][]any{}))
		roleOID := pgDescriptionSharedRoleID(t, p, backend)
		commentInquiryStep(t, p, backend, backend.DB, fixture.Select("SELECT shobj_description($1,'pg_authid')", []string{"shobj_description"}, []string{"TEXT"}, [][]any{{"control shared role"}}, roleOID))
		fixture.PgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, roleOID))
		pgDescriptionRetainedText(t, retained[backend.Name], "control whole")
	}
}
