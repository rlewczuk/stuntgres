package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferencePGDescriptionSequenceTransactions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE SEQUENCE pgd_sequence", 0))
			oid := pgDescriptionRelationID(t, p, backend, "pgd_sequence")
			check := func(executor referenceExecutor, text string) {
				t.Helper()
				want := [][]any{}
				if text != "" {
					want = append(want, []any{oid, int64(1259), int64(0), text})
				}
				s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", want, oid)
				pgDescriptionExecutorRows(t, p, backend, executor, s)
				pgDescriptionExecutorRows(t, p, backend, executor, referencePrepared(s))
			}
			check(backend.db, "")
			for _, commit := range []bool{false, true} {
				tx, err := backend.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, backend.recorder, referencePrepared(referenceExec("COMMENT ON SEQUENCE pgd_sequence IS 'committed'", 0)))
				check(tx, "committed")
				check(backend.db, "")
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					check(backend.db, "committed")
				} else {
					check(backend.db, "")
				}
			}
			tx, err := backend.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("COMMENT ON SEQUENCE pgd_sequence IS NULL", 0))
			check(tx, "")
			check(backend.db, "committed")
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			check(backend.db, "committed")
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("DROP SEQUENCE pgd_sequence", 0))
			check(backend.db, "")
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, q := range []string{"CREATE TEMP SEQUENCE pgd_temp_sequence", "COMMENT ON SEQUENCE pgd_temp_sequence IS 'temporary'"} {
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec(q, 0))
			}
			if err := conn.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgd_temp_sequence'").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			check(conn, "temporary")
			// Temporary object name/access authority remains session-private;
			// descriptions at already known numeric catalog keys are metadata,
			// visible to peers without opening the described sequence.
			check(backend.db, "temporary")
			temp := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{oid, int64(1259), int64(0), "temporary"}}, oid)
			temp.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgDescriptionMetadata(t, rows, backend.pg) }
			pgDescriptionExecutorRows(t, p, backend, conn, temp)
			if !backend.pg {
				text := commentInquiryDump(t, p)
				if strings.Contains(text, "pgd_temp_sequence") {
					t.Fatal("TEMP comment leaked into Dump", text)
				}
			}
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("DROP SEQUENCE pgd_temp_sequence", 0))
			check(conn, "")
		})
	}
}
