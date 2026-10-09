package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferencePGDescriptionControls(t *testing.T)     { pgDescriptionControlsScenario(t) }
func TestReferencePGDescriptionFeatureSmoke(t *testing.T) { pgDescriptionControlsScenario(t) }

func pgDescriptionControlsScenario(t *testing.T) {
	schemaReferenceCleanup(t, "pgd_controls")
	p := newReferenceScenario(t)
	if p.postgres != nil {
		t.Cleanup(func() {
			if _, err := p.postgres.ExecContext(context.Background(), "COMMENT ON SCHEMA pg_catalog IS 'system catalog schema'"); err != nil {
				t.Error(err)
			}
		})
	}
	retained := map[string]*sql.Rows{}
	for _, backend := range p.backends() {
		func() {
			pgDescriptionPreserveSharedRole(t, p, backend)
			run := func(q string) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			empty := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE FALSE", [][]any{})
			empty.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.pg) }
			pgDescriptionRows(t, p, backend, empty)
			pgDescriptionRows(t, p, backend, referencePrepared(empty))
			identity := referenceSelect("SELECT oid,relnatts FROM pg_catalog.pg_class WHERE oid=2609", []string{"oid", "relnatts"}, []string{"OID", "INT2"}, [][]any{{int64(2609), int64(4)}})
			pgDescriptionRows(t, p, backend, identity)
			for _, seed := range []struct {
				oid, class int64
				text       string
			}{{114, 1247, "JSON stored as text"}, {4518, 2607, "conversion for LATIN1 to UTF8"}} {
				s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=$2", [][]any{{seed.oid, seed.class, int64(0), seed.text}}, seed.oid, seed.class)
				pgDescriptionRows(t, p, backend, s)
				pgDescriptionRows(t, p, backend, referencePrepared(s))
			}
			for _, q := range []string{"COMMENT ON ROLE postgres IS 'control shared role'", "CREATE SCHEMA pgd_controls", "COMMENT ON SCHEMA pgd_controls IS 'control schema'", "CREATE TABLE pgd_controls.pgd_control_table(a INT,b INT,c TEXT)", "CREATE SEQUENCE pgd_controls.pgd_control_sequence", "COMMENT ON SEQUENCE pgd_controls.pgd_control_sequence IS 'control sequence'", "COMMENT ON TABLE pgd_controls.pgd_control_table IS 'control whole'", "COMMENT ON COLUMN pgd_controls.pgd_control_table.c IS 'control third'", "ALTER TABLE pgd_controls.pgd_control_table DROP b"} {
				run(q)
			}
			oid := pgDescriptionRelationID(t, p, backend, "pgd_control_table")
			pgDescriptionRelationBag(t, p, backend, oid, [][]any{{oid, int64(1259), int64(0), "control whole"}, {oid, int64(1259), int64(3), "control third"}})
			query := "SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259"
			stmt, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: query}
			run("COMMENT ON TABLE pgd_controls.pgd_control_table IS 'replacement'")
			pgDescriptionExecutorRows(t, p, backend, held, pgDescriptionSelect(query, [][]any{{oid, int64(1259), int64(0), "replacement"}, {oid, int64(1259), int64(3), "control third"}}, oid))
			run("COMMENT ON TABLE pgd_controls.pgd_control_table IS NULL")
			pgDescriptionExecutorRows(t, p, backend, held, pgDescriptionSelect(query, [][]any{{oid, int64(1259), int64(3), "control third"}}, oid))
			run("COMMENT ON TABLE pgd_controls.pgd_control_table IS 'control whole'")
			rows, err := backend.db.QueryContext(p.ctx, "SELECT description FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259 AND objsubid=0", oid)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { rows.Close() })
			retained[backend.name] = rows
			roleOID := pgDescriptionSharedRoleID(t, p, backend)
			pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, roleOID))
			commentInquiryStep(t, p, backend, backend.db, referenceSelect("SELECT shobj_description($1,'pg_authid')", []string{"shobj_description"}, []string{"TEXT"}, [][]any{{"control shared role"}}, roleOID))
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
	native := p.backends()[0]
	checkNative := func() {
		t.Helper()
		oid := pgDescriptionRelationID(t, p, native, "pgd_control_table")
		pgDescriptionRelationBag(t, p, native, oid, [][]any{{oid, int64(1259), int64(0), "control whole"}, {oid, int64(1259), int64(3), "control third"}})
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump+"COMMENT ON COLUMN pgd_controls.pgd_control_table.missing IS 'bad';")); err == nil {
		t.Fatal("late Restore unexpectedly succeeded")
	} else {
		referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" of relation \"pgd_controls.pgd_control_table\" does not exist"})
	}
	checkNative()
	if commentInquiryDump(t, p) != dump {
		t.Fatal("late failing Restore changed complete native catalog")
	}
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
		t.Fatal("Restore cancellation", err)
	}
	checkNative()
	if commentInquiryDump(t, p) != dump {
		t.Fatal("canceled Restore changed complete native catalog")
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, native.db, native.recorder, referenceSelect("SELECT count(*) FROM pg_catalog.pg_description", []string{"count"}, []string{"INT8"}, [][]any{{int64(163)}}))
	pgDescriptionRows(t, p, native, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE classoid=2615", [][]any{{int64(11), int64(2615), int64(0), "system catalog schema"}, {int64(2200), int64(2615), int64(0), "standard public schema"}}))
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	if commentInquiryDump(t, p) != dump {
		t.Fatal("noncanonical logical roundtrip", commentInquiryDump(t, p), dump)
	}
	if p.postgres != nil {
		conn, err := p.postgres.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if _, err := conn.ExecContext(p.ctx, "DROP SCHEMA pgd_controls CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(p.ctx, dump); err != nil {
				t.Fatal("PG18 rejected actual native Dump", err)
			}
		}()
	}
	for _, backend := range p.backends() {
		oid := pgDescriptionRelationID(t, p, backend, "pgd_control_table")
		pgDescriptionRelationBag(t, p, backend, oid, [][]any{{oid, int64(1259), int64(0), "control whole"}, {oid, int64(1259), int64(2), "control third"}})
		sequence := pgDescriptionRelationID(t, p, backend, "pgd_control_sequence")
		pgDescriptionRelationBag(t, p, backend, sequence, [][]any{{sequence, int64(1259), int64(0), "control sequence"}})
		var namespace int64
		if err := backend.db.QueryRowContext(p.ctx, "SELECT objoid FROM pg_catalog.pg_description WHERE classoid=2615 AND description='control schema'").Scan(&namespace); err != nil {
			t.Fatal(err)
		}
		pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE classoid=2615 AND objoid=$1", [][]any{{namespace, int64(2615), int64(0), "control schema"}}, namespace))
		pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE classoid=2615 AND objoid=11", [][]any{{int64(11), int64(2615), int64(0), "control catalog override"}}))
		pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE classoid=2615 AND description='standard public schema'", [][]any{}))
		roleOID := pgDescriptionSharedRoleID(t, p, backend)
		commentInquiryStep(t, p, backend, backend.db, referenceSelect("SELECT shobj_description($1,'pg_authid')", []string{"shobj_description"}, []string{"TEXT"}, [][]any{{"control shared role"}}, roleOID))
		pgDescriptionRows(t, p, backend, pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, roleOID))
		pgDescriptionRetainedText(t, retained[backend.name], "control whole")
	}
}
