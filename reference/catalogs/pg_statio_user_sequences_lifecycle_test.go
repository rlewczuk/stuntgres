package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserSequencesLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "statio_user_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, owner, "CREATE SCHEMA statio_user_moved")
			// Prepare before the sequence exists; reexecution discovers current
			// descriptors rather than retaining a row captured during admission.
			stmt, err := observer.PrepareContext(p.Ctx, statioUserSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioUserSequenceProbe}
			statioSequenceExec(t, p, b, owner, "CREATE SEQUENCE statio_user_lifecycle CACHE 3")
			oid := statioSequenceOID(t, p, owner, "public.statio_user_lifecycle")
			statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_lifecycle", 0, 0)
			statioSequenceExec(t, p, b, owner, "BEGIN")
			pgSequencesScalar(t, p, b, owner, "SELECT nextval('statio_user_lifecycle')", "nextval", 1)
			statioSequenceExec(t, p, b, owner, "COMMIT")
			statioUserSequenceWitness(t, p, b, observer, held, oid, "public", "statio_user_lifecycle", 1, 0)
			// Native rows are eager and owned; PostgreSQL streams on its borrower.
			var old *sql.Rows
			if !b.PG {
				old, err = observer.QueryContext(p.Ctx, statioUserSequenceProbe, oid)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
			}
			for _, q := range []string{"ALTER SEQUENCE statio_user_lifecycle RENAME TO statio_user_renamed", "ALTER SEQUENCE statio_user_renamed SET SCHEMA statio_user_moved"} {
				statioSequenceExec(t, p, b, owner, q)
			}
			statioUserSequenceWitness(t, p, b, observer, held, oid, "statio_user_moved", "statio_user_renamed", 1, 0)
			for _, q := range []string{"BEGIN", "DROP SEQUENCE statio_user_moved.statio_user_renamed", "ROLLBACK"} {
				statioSequenceExec(t, p, b, owner, q)
			}
			statioUserSequenceWitness(t, p, b, observer, held, oid, "statio_user_moved", "statio_user_renamed", 1, 0)
			statioSequenceExec(t, p, b, owner, "DROP SEQUENCE statio_user_moved.statio_user_renamed")
			statioUserSequenceWitness(t, p, b, observer, held, oid, "", "", 0, 0)
			if old != nil {
				statioSequenceMetadata(t, old, false)
				if !old.Next() {
					t.Fatal("retained user result missing", old.Err())
				}
				var r statioSequenceRow
				if err := old.Scan(&r.oid, &r.schema, &r.name, &r.read, &r.hit); err != nil {
					t.Fatal(err)
				}
				if r != (statioSequenceRow{oid: oid, schema: "public", name: "statio_user_lifecycle", read: 1}) {
					t.Fatal("retained user result mutated by DDL/drop", r)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("retained user cardinality", old.Err())
				}
				old.Close()
			}
			statioSequenceExec(t, p, b, owner, "CREATE SEQUENCE statio_user_moved.statio_user_renamed")
			next := statioSequenceOID(t, p, owner, "statio_user_moved.statio_user_renamed")
			if next == oid {
				t.Fatal("recreated sequence reused retired OID")
			}
			statioUserSequenceWitness(t, p, b, observer, held, next, "statio_user_moved", "statio_user_renamed", 0, 0)
			statioUserSequenceWitness(t, p, b, observer, held, oid, "", "", 0, 0)
			statioSequenceExec(t, p, b, owner, "BEGIN")
			statioSequenceExec(t, p, b, owner, "CREATE SEQUENCE statio_user_aborted")
			aborted := statioSequenceOID(t, p, owner, "public.statio_user_aborted")
			pgSequencesScalar(t, p, b, owner, "SELECT nextval('statio_user_aborted')", "nextval", 1)
			statioSequenceExec(t, p, b, owner, "ROLLBACK")
			statioUserSequenceWitness(t, p, b, observer, held, aborted, "", "", 0, 0)
		})
	}
}
