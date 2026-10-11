package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDescriptionSequenceTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE SEQUENCE pgd_sequence", 0))
			oid := pgDescriptionRelationID(t, p, backend, "pgd_sequence")
			check := func(executor fixture.Executor, text string) {
				t.Helper()
				want := [][]any{}
				if text != "" {
					want = append(want, []any{oid, int64(1259), int64(0), text})
				}
				s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", want, oid)
				fixture.PgDescriptionExecutorRows(t, p, backend, executor, s)
				fixture.PgDescriptionExecutorRows(t, p, backend, executor, fixture.Prepared(s))
			}
			check(backend.DB, "")
			for _, commit := range []bool{false, true} {
				tx, err := backend.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Prepared(fixture.Exec("COMMENT ON SEQUENCE pgd_sequence IS 'committed'", 0)))
				check(tx, "committed")
				check(backend.DB, "")
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					check(backend.DB, "committed")
				} else {
					check(backend.DB, "")
				}
			}
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("COMMENT ON SEQUENCE pgd_sequence IS NULL", 0))
			check(tx, "")
			check(backend.DB, "committed")
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			check(backend.DB, "committed")
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP SEQUENCE pgd_sequence", 0))
			check(backend.DB, "")
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, q := range []string{"CREATE TEMP SEQUENCE pgd_temp_sequence", "COMMENT ON SEQUENCE pgd_temp_sequence IS 'temporary'"} {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(q, 0))
			}
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgd_temp_sequence'").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			check(conn, "temporary")
			// Temporary object name/access authority remains session-private;
			// descriptions at already known numeric catalog keys are metadata,
			// visible to peers without opening the described sequence.
			check(backend.DB, "temporary")
			temp := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{oid, int64(1259), int64(0), "temporary"}}, oid)
			temp.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.PG) }
			fixture.PgDescriptionExecutorRows(t, p, backend, conn, temp)
			if !backend.PG {
				text := commentInquiryDump(t, p)
				if strings.Contains(text, "pgd_temp_sequence") {
					t.Fatal("TEMP comment leaked into Dump", text)
				}
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("DROP SEQUENCE pgd_temp_sequence", 0))
			check(conn, "")
		})
	}
}
