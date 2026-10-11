package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func batchDetachedPrivateRole(t *testing.T) {
	t.Run("detached_private_role_prepared", func(t *testing.T) {
		for _, finish := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(finish, func(t *testing.T) {
				role := "batch_detached_" + strings.ToLower(finish)
				fixture.RoleReferenceCleanup(t, []string{role})
				batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, unused, peer *sql.Conn) {
					f := fixture.NewControlsReferenceFixture(t, p, b.PG)
					a := f.A
					batchSeed(t, p, b, a)
					if b.PG {
						batchExec(t, p, b, a, "GRANT USAGE ON SCHEMA public TO PUBLIC", 0)
					}
					f.NoticesWant(t, "", "")
					var session string
					if err := a.QueryRowContext(p.Ctx, "SELECT session_user").Scan(&session); err != nil {
						t.Fatal(err)
					}
					gid := "driver_batch_detached_" + strings.ToLower(finish)
					fixture.PreparedReferenceCleanup(t, b, gid)
					literal := fixture.PreparedReferenceLiteral(gid)
					t.Cleanup(func() { _, _ = a.ExecContext(p.Ctx, "SET ROLE NONE") })
					identitySQL := "SELECT current_user AS cu,session_user AS su"
					reached := batchSet{[]string{"cu", "su"}, []string{"NAME", "NAME"}, [][]any{{role, session}}, false}
					q := "CREATE ROLE " + role + " LOGIN; GRANT SELECT ON batch_t TO " + role + "; SET ROLE " + role + "; " + identitySQL + "; SELECT usesysid AS role_oid FROM pg_user WHERE usename='" + role + "'; SELECT i FROM batch_t; BEGIN; PREPARE TRANSACTION " + literal
					rows := batchQuery(t, p, a, q)
					batchReadSet(t, rows, reached)
					if !rows.NextResultSet() || !rows.Next() {
						t.Fatal("missing independently captured private role OID", rows.Err())
					}
					var id uint64
					if err := rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					if id == 0 || id > uint64(^uint32(0)) {
						t.Fatal("invalid real role OID", id)
					}
					oid := strconv.FormatUint(id, 10)
					if rows.Next() || rows.Err() != nil || !rows.NextResultSet() {
						t.Fatal("private role OID set cardinality/boundary", rows.Err())
					}
					batchReadSet(t, rows, batchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}, false})
					if rows.NextResultSet() || rows.Err() != nil {
						t.Fatal("prepared message completion", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					f.NoticesWant(t, "", "")
					fixture.PreparedReferenceOne(t, p, f.B, gid)
					checkDetached := func() {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT session_user AS su", []string{"su"}, []string{"NAME"}, [][]any{{session}}))
						// PREPARE preserves the effective ID without retaining a private
						// role name. Independently captured pg_user metadata supplies
						// the generated OID, never a fixed diagnostic golden.
						batchError(t, p, a, "SELECT current_user", batchDiag("42704", "invalid role OID: "+oid))
						batchError(t, p, a, "SELECT i FROM batch_t", batchDiag("42501", "permission denied for table batch_t"))
					}
					checkDetached()
					// A distinct bootstrap connection finishes the actual prepared
					// owner while the first physical connection retains that role ID.
					batchExec(t, p, b, f.B, finish+" PREPARED "+literal, 0)
					fixture.PreparedReferenceAbsent(t, p, f.B, gid)
					if finish == "COMMIT" {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(identitySQL, reached.columns, reached.types, reached.rows))
						batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
					} else {
						checkDetached()
					}
					batchExec(t, p, b, a, "SET ROLE NONE", 0)
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(identitySQL, reached.columns, reached.types, [][]any{{session, session}}))
					batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
					if finish == "ROLLBACK" {
						batchError(t, p, a, "SET ROLE "+role, batchDiag("22023", "role \""+role+"\" does not exist"))
					}
				})
			})
		}
	})
}
