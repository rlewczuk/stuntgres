package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablespaceLifecycleOwner(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		for _, prepared := range []bool{false, true} {
			t.Run(lifecycleRoute(prepared), func(t *testing.T) {
				s.begin(t)
				defer s.rollback(t)
				for _, role := range []string{"ts_lifecycle_o", "ts_lifecycle_to", "ts_lifecycle_outsider", "ts_lifecycle_inheritor"} {
					s.exec(t, "CREATE ROLE "+role)
				}
				s.exec(t, "GRANT ts_lifecycle_o TO ts_lifecycle_inheritor WITH INHERIT TRUE, SET FALSE")
				for _, recipient := range []string{"ts_lifecycle_missing_role", "public", "\"current_user\"", "\"current_role\"", "\"session_user\""} {
					name := recipient
					if name[0] == '"' {
						name = name[1 : len(name)-1]
					}
					for _, object := range []string{"pg_default", "ts_lifecycle_missing"} {
						s.error(t, "ALTER TABLESPACE "+object+" OWNER TO "+recipient, tablespaceDiagnostic("42704", "role \""+name+"\" does not exist"), false)
					}
				}
				s.error(t, "ALTER TABLESPACE ts_lifecycle_missing OWNER TO ts_lifecycle_o", lifecycleMissing("ts_lifecycle_missing"), false)
				s.exec(t, "ALTER TABLESPACE pg_default OWNER TO ts_lifecycle_o")
				s.exec(t, "SET ROLE ts_lifecycle_o")
				s.exec(t, "GRANT CREATE ON TABLESPACE pg_default TO ts_lifecycle_outsider")
				s.exec(t, "RESET ROLE")
				beforeACL := "{ts_lifecycle_o=C/ts_lifecycle_o,ts_lifecycle_outsider=C/ts_lifecycle_o}"
				s.owner(t, "pg_default", "ts_lifecycle_o", beforeACL)
				// A nonowner may request the existing owner: no ownership/SET
				// check and no ACL rewrite, but readonly admission is separate.
				s.exec(t, "SET ROLE ts_lifecycle_outsider")
				step := fixture.Exec("ALTER TABLESPACE pg_default OWNER TO ts_lifecycle_o", 0)
				step.Prepared = prepared
				s.step(t, step)
				s.owner(t, "pg_default", "ts_lifecycle_o", beforeACL)
				s.error(t, "ALTER TABLESPACE pg_default OWNER TO ts_lifecycle_to", tablespaceDiagnostic("42501", "must be owner of tablespace pg_default"), false)
				s.exec(t, "RESET ROLE")
				s.exec(t, "SET ROLE ts_lifecycle_inheritor")
				s.error(t, "ALTER TABLESPACE pg_default OWNER TO ts_lifecycle_to", tablespaceDiagnostic("42501", "must be able to SET ROLE \"ts_lifecycle_to\""), false)
				s.exec(t, "RESET ROLE")
				s.exec(t, "GRANT ts_lifecycle_to TO ts_lifecycle_o WITH INHERIT FALSE, SET TRUE")
				s.exec(t, "SET ROLE ts_lifecycle_o")
				step = fixture.Exec("ALTER TABLESPACE pg_default OWNER TO ts_lifecycle_to", 0)
				step.Prepared = prepared
				s.step(t, step)
				s.owner(t, "pg_default", "ts_lifecycle_to", "{ts_lifecycle_to=C/ts_lifecycle_to,ts_lifecycle_outsider=C/ts_lifecycle_to}")
				s.exec(t, "RESET ROLE")
				// Outgoing role edges follow real owner/ACL mutation, not a
				// fabricated incoming placement dependency on the tablespace.
				s.count(t, "SELECT count(*) AS n FROM pg_shdepend WHERE classid=1213 AND objid=1663 AND refobjid=(SELECT oid FROM pg_roles WHERE rolname='ts_lifecycle_to') AND deptype='o'", 1)
				s.count(t, "SELECT count(*) AS n FROM pg_shdepend WHERE classid=1213 AND objid=1663 AND refobjid=(SELECT oid FROM pg_roles WHERE rolname='ts_lifecycle_o')", 0)
				d := tablespaceDiagnostic("2BP01", "role \"ts_lifecycle_to\" cannot be dropped because some objects depend on it")
				d.Detail = "owner of tablespace pg_default"
				s.error(t, "DROP ROLE ts_lifecycle_to", d, false)
				// Typed identities are not quoted literal identifiers. Prepare
				// before SET ROLE and check execution's CURRENT_USER identity.
				q := "ALTER TABLESPACE pg_global OWNER TO CURRENT_USER"
				held, err := s.c.PrepareContext(s.p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				s.exec(t, "ALTER TABLESPACE pg_global OWNER TO ts_lifecycle_o")
				s.exec(t, "SET ROLE ts_lifecycle_o")
				s.step(t, fixture.Exec("ALTER TABLESPACE pg_global OWNER TO CURRENT_ROLE", 0))
				fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: held, SQL: q}, s.b.Recorder, fixture.Exec(q, 0))
				s.owner(t, "pg_global", "ts_lifecycle_o", "")
				s.exec(t, "RESET ROLE")
				// Superuser can transfer to an ordinary role without CREATE,
				// CREATEDB or membership; SESSION_USER is resolved at Exec.
				s.utility(t, "ALTER TABLESPACE pg_global OWNER TO SESSION_USER", prepared)
				bootstrap := "postgres"
				if s.b.PG {
					bootstrap = "stuntgres"
				}
				s.owner(t, "pg_global", bootstrap, "")
				s.exec(t, "CREATE ROLE ts_lifecycle_session")
				s.exec(t, "CREATE ROLE ts_lifecycle_effective SUPERUSER")
				s.exec(t, "GRANT ts_lifecycle_effective TO ts_lifecycle_session WITH INHERIT FALSE, SET TRUE")
				s.exec(t, "SET SESSION AUTHORIZATION ts_lifecycle_session")
				s.exec(t, "SET ROLE ts_lifecycle_effective")
				step = fixture.Exec("ALTER TABLESPACE pg_global OWNER TO CURRENT_USER", 0)
				step.Prepared = prepared
				s.step(t, step)
				s.owner(t, "pg_global", "ts_lifecycle_effective", "")
				step = fixture.Exec("ALTER TABLESPACE pg_global OWNER TO SESSION_USER", 0)
				step.Prepared = prepared
				s.step(t, step)
				s.owner(t, "pg_global", "ts_lifecycle_session", "")
				s.exec(t, "RESET SESSION AUTHORIZATION")
				transferSQL := "ALTER TABLESPACE pg_default OWNER TO ts_lifecycle_outsider"
				transfer, err := s.c.PrepareContext(s.p.Ctx, transferSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer transfer.Close()
				for _, actor := range []string{"ts_lifecycle_o", "ts_lifecycle_to"} {
					s.exec(t, "SET ROLE "+actor)
					s.exec(t, "SAVEPOINT ts_lifecycle_transfer")
					_, err = transfer.ExecContext(s.p.Ctx)
					want := tablespaceDiagnostic("42501", "must be owner of tablespace pg_default")
					if actor == "ts_lifecycle_to" {
						want = tablespaceDiagnostic("42501", "must be able to SET ROLE \"ts_lifecycle_outsider\"")
					}
					fixture.AssertDiagnostic(t, err, &want)
					s.exec(t, "ROLLBACK TO ts_lifecycle_transfer")
					s.exec(t, "RELEASE ts_lifecycle_transfer")
					s.exec(t, "RESET ROLE")
				}
				s.exec(t, "GRANT ts_lifecycle_outsider TO ts_lifecycle_to WITH INHERIT FALSE, SET TRUE")
				s.exec(t, "SET ROLE ts_lifecycle_to")
				fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: transfer, SQL: transferSQL}, s.b.Recorder, fixture.Exec(transferSQL, 0))
				// aclnewowner merges the recipient's existing grant with its
				// owner entry, and rebases both grantor and grantee.
				s.owner(t, "pg_default", "ts_lifecycle_outsider", "{ts_lifecycle_outsider=C/ts_lifecycle_outsider}")
				s.exec(t, "RESET ROLE")
			})
			s.names(t, "pg_default", "pg_global")
		}
	})
}

func TestReferenceTablespaceLifecycleTransactions(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		for _, prepared := range []bool{false, true} {
			t.Run(lifecycleRoute(prepared), func(t *testing.T) {
				for _, readonly := range []bool{false, true} {
					for _, q := range []string{"DROP TABLESPACE IF EXISTS ts_lifecycle_missing", "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_ro", "ALTER TABLESPACE pg_default OWNER TO CURRENT_USER"} {
						if !readonly && q[0] != 'D' {
							continue
						}
						s.begin(t)
						if readonly {
							s.exec(t, "SET TRANSACTION READ ONLY")
						}
						*s.notices = nil
						d := tablespaceDiagnostic("25001", "DROP TABLESPACE cannot run inside a transaction block")
						if readonly {
							command := "ALTER TABLESPACE"
							if q[0] == 'D' {
								command = "DROP TABLESPACE"
							}
							d = tablespaceDiagnostic("25006", "cannot execute "+command+" in a read-only transaction")
						}
						step := fixture.ErrorDiagnostic(q, d)
						step.Prepared, step.ErrorStage = prepared, "execute"
						s.step(t, step)
						s.noticesWant(t)
						// Do not Prepare this probe: the assertion is the execution
						// admission phase of the already failed transaction.
						s.step(t, fixture.ImplicitError("SELECT 1", fixture.TransactionAborted()))
						s.rollback(t)
						s.names(t, "pg_default", "pg_global")
					}
				}
				s.begin(t)
				s.exec(t, "SAVEPOINT ts_lifecycle_mutation")
				step := fixture.Exec("ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_tx", 0)
				step.Prepared = prepared
				s.step(t, step)
				s.names(t, "ts_lifecycle_tx", "pg_global")
				s.exec(t, "ROLLBACK TO ts_lifecycle_mutation")
				s.names(t, "pg_default", "pg_global")
				s.exec(t, "RELEASE ts_lifecycle_mutation")
				s.exec(t, "ALTER TABLESPACE pg_global RENAME TO ts_lifecycle_tx_global")
				s.rollback(t)
				s.names(t, "pg_default", "pg_global")
				// Cancellation is a native publication guarantee, not a PG
				// timing contract. A PG autocommit rename could commit before
				// cancellation and cannot be repaired by a reserved-name rename.
				if !s.b.PG {
					t.Run("native-canceled-publication", func(t *testing.T) {
						ctx, cancel := context.WithCancel(s.p.Ctx)
						cancel()
						q := "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_canceled"
						if prepared {
							held, err := s.c.PrepareContext(s.p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							_, err = held.ExecContext(ctx)
							held.Close()
							if !errors.Is(err, context.Canceled) {
								t.Fatal("canceled prepared ALTER", err)
							}
						} else {
							if _, err := s.c.ExecContext(ctx, q); !errors.Is(err, context.Canceled) {
								t.Fatal("canceled direct ALTER", err)
							}
						}
						s.names(t, "pg_default", "pg_global")
					})
				}
			})
		}
		q := "CREATE TABLE ts_lifecycle_chain(a integer); DROP TABLESPACE IF EXISTS ts_lifecycle_missing"
		step := fixture.ErrorDiagnostic(q, tablespaceDiagnostic("25001", "DROP TABLESPACE cannot run inside a transaction block"))
		step.ErrorStage = "execute"
		s.step(t, step)
		s.count(t, "SELECT count(*) AS n FROM pg_class WHERE relname='ts_lifecycle_chain'", 0)
		step = fixture.ErrorDiagnostic(q, tablespaceDiagnostic("42601", "cannot insert multiple commands into a prepared statement"))
		step.Prepared, step.ErrorStage = true, "prepare"
		s.step(t, step)
		s.count(t, "SELECT count(*) AS n FROM pg_class WHERE relname='ts_lifecycle_chain'", 0)
		s.exec(t, "SET default_transaction_read_only=on")
		s.error(t, "DROP TABLESPACE IF EXISTS ts_lifecycle_missing", tablespaceDiagnostic("25006", "cannot execute DROP TABLESPACE in a read-only transaction"), false)
		s.exec(t, "SET default_transaction_read_only=off")
		s.names(t, "pg_default", "pg_global")
	})
}
