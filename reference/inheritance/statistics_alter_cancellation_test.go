package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsAlterCancellation(t *testing.T) {
	for _, action := range []string{"RENAME TO sa_after", "SET SCHEMA sa_destination", "OWNER TO sa_recipient", "SET STATISTICS 10001"} {
		t.Run(action, func(t *testing.T) {
			withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
				statisticsAlterSetup(s)
				var owner string
				if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT current_user").Scan(&owner); err != nil {
					t.Fatal(err)
				}
				oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'")
				s.run(fixture.Exec("BEGIN", 0))
				defer s.conn.ExecContext(context.Background(), "ROLLBACK")
				// A real tuple update holds PostgreSQL arbitration and the native original
				// definition lock; rollback leaves the collected source unchanged.
				s.run(fixture.Exec("ALTER STATISTICS sa_before SET STATISTICS 10000", 0))
				b := s.pair.Backends()[0]
				if s.pg {
					b = s.pair.Backends()[1]
				}
				worker, err := b.DB.Conn(s.pair.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				query := "ALTER STATISTICS public.sa_before " + action
				var held *sql.Stmt
				if s.prepared {
					held, err = worker.PrepareContext(s.pair.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				ctx, cancel := context.WithCancel(s.pair.Ctx)
				defer cancel()
				pid := 0
				if s.pg {
					pid = fixture.LockPID(t, ctx, worker)
				}
				join := fixture.DefinitionLockCall(t, ctx, s.pair, b, worker, s.conn, query, "transactionid", "ShareLock", 0, 0, func(ctx context.Context) fixture.RelationOutcome {
					var out fixture.RelationOutcome
					if held != nil {
						out.Result, out.Err = held.ExecContext(ctx)
					} else {
						out.Result, out.Err = worker.ExecContext(ctx, query)
					}
					return out
				})
				if s.pg {
					var signaled bool
					if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
						t.Fatal(signaled, err)
					}
				} else {
					cancel()
				}
				out := join()
				if out.Result != nil || out.Rows != nil {
					t.Fatal("canceled utility returned output")
				}
				if s.pg {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					got := fixture.ExtractDiagnostic(t, out.Err)
					// Each action is deliberately observed at transaction-ID
					// arbitration in CatalogTupleUpdate on pg_statistic_ext.
					// Context is required; only physical heap coordinates vary.
					switch action {
					case "RENAME TO sa_after", "SET SCHEMA sa_destination", "OWNER TO sa_recipient", "SET STATISTICS 10001":
						if !regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "pg_statistic_ext"$`).MatchString(got.Context) {
							t.Fatal("missing or incorrect tuple-update cancellation Context", action, got.Context)
						}
						want.Context = got.Context
					default:
						t.Fatal("missing independent cancellation contract", action)
					}
					fixture.AssertDiagnostic(t, out.Err, &want)
				} else {
					fixture.LexicalOrdinary(t, out.Err, context.Canceled)
				}
				s.run(fixture.Exec("ROLLBACK", 0))
				s.run(statisticsAlterMetadata("sa_before", "public", owner, nil))
				statisticsAlterFacts(t, s, "sa_before")
				if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'"); got != oid {
					t.Fatal("cancellation changed source identity")
				}
				s.run(fixture.Select("SELECT count(*) FROM sa_input", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
			})
		})
	}
	t.Run("current_authority_recapture", func(t *testing.T) {
		withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
			statisticsAlterSetup(s)
			s.run(fixture.Exec("BEGIN", 0))
			defer s.conn.ExecContext(context.Background(), "ROLLBACK")
			s.run(fixture.Exec("ALTER STATISTICS sa_before SET STATISTICS 0", 0))
			b := s.pair.Backends()[0]
			if s.pg {
				b = s.pair.Backends()[1]
			}
			var notices []fixture.Diagnostic
			_, worker, rec := fixture.NoticeReferencePool(t, s.pair, s.pg, &notices)
			query := "ALTER STATISTICS sa_before SET STATISTICS 10001"
			var held *sql.Stmt
			var err error
			if s.prepared {
				held, err = worker.PrepareContext(s.pair.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
			}
			ctx, cancel := context.WithCancel(s.pair.Ctx)
			defer cancel()
			join := fixture.DefinitionLockCall(t, ctx, s.pair, b, worker, s.conn, query, "transactionid", "ShareLock", 0, 0, func(ctx context.Context) fixture.RelationOutcome {
				var out fixture.RelationOutcome
				if held != nil {
					out.Result, out.Err = held.ExecContext(ctx)
				} else {
					out.Result, out.Err = worker.ExecContext(ctx, query)
				}
				return out
			})
			fixture.RunStep(t, s.pair.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE sa_peer(v INT)", 0))
			s.run(fixture.Exec("ROLLBACK", 0))
			out := join()
			if out.Err != nil || out.Result == nil {
				t.Fatal(out.Err)
			}
			n, err := out.Result.RowsAffected()
			if err != nil || n != 0 {
				t.Fatal(n, err)
			}
			want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal("recapture duplicated warning", notices)
			}
			fixture.RunStep(t, s.pair.Ctx, worker, rec, fixture.Select("SELECT stxstattarget FROM pg_statistic_ext WHERE stxname='sa_before'", []string{"stxstattarget"}, []string{"INT2"}, [][]any{{int64(10000)}}))
			statisticsAlterFacts(t, s, "sa_before")
		})
	})
}
