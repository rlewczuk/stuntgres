package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferencePGUserFeatureSmoke(t *testing.T) {
	pgUserScenario(t, []string{"pgu_smoke_target", "pgu_smoke_reader", "pgu_smoke_group"}, func(t *testing.T, p *referencePair, b referenceBackend) {
		id := pgUserCreate(t, p, b, "pgu_smoke_target")
		pgUserCreate(t, p, b, "pgu_smoke_reader")
		group := pgShDescriptionCreateRole(t, p, b, "pgu_smoke_group", "public")
		pgUserExec(t, p, b, "ALTER ROLE pgu_smoke_target VALID UNTIL '2030-01-02 03:04:05+00'")
		if _, err := b.db.ExecContext(p.ctx, "ALTER ROLE pgu_smoke_target PASSWORD 'pgu-smoke-private-password'"); err != nil {
			t.Fatal("password fixture setup failed")
		}
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.ctx, "SET ROLE pgu_smoke_reader"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := conn.ExecContext(p.ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
		}()
		row := pgUserRow("pgu_smoke_target", id, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
		for _, view := range []string{"pg_user", "pg_catalog.pg_user"} {
			for _, prepared := range []bool{false, true} {
				s := pgUserSelect("SELECT * FROM "+view+" WHERE usesysid=$1", [][]any{row}, id)
				s.prepared = prepared
				pgDescriptionExecutorRows(t, p, b, conn, s)
				empty := pgUserSelect("SELECT * FROM "+view+" WHERE usesysid=$1", [][]any{}, group)
				empty.prepared = prepared
				empty.checkMetadata = func(t *testing.T, rows *sql.Rows) { pgUserMetadata(t, rows, b.pg) }
				pgDescriptionExecutorRows(t, p, b, conn, empty)
			}
		}
		query := "SELECT usecreatedb FROM pg_user WHERE usesysid=$1"
		stmt, err := conn.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		old, err := stmt.QueryContext(p.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		pgUserExec(t, p, b, "ALTER ROLE pgu_smoke_target CREATEDB")
		// The mutation used a different pool connection. Consume the retained
		// old snapshot before reusing its connection for the fresh execution.
		pgUserRetainedFlag(t, old, false)
		held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: query}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(query, []string{"usecreatedb"}, []string{"BOOL"}, [][]any{{true}}, id))
		// Restore administrator for rewrite diagnostics rather than privilege errors.
		if _, err := conn.ExecContext(p.ctx, "RESET ROLE"); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("UPDATE pg_catalog.pg_user SET passwd=NULL WHERE FALSE", pgUserWriteDiagnostic("update", true))
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, conn, b.recorder, s)
		}
		row[2] = true
		pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{row}, id))
		for _, name := range []string{"pgu_smoke_target", "pgu_smoke_reader", "pgu_smoke_group"} {
			pgUserExec(t, p, b, "DROP ROLE "+name)
		}
	})
}
