//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func referenceSequencePreparedMatrix(t *testing.T) {
	t.Run("NumericIdentityPostgres", referenceSequenceNumericIdentity)
	t.Run("RegclassOutput", referenceSequenceRegclassOutput)
	for _, scenario := range []string{"SchemaMove", "EffectivePath", "GeneratorDoesNotInvalidate", "OptionInvalidation", "DropRecreate", "Transactional", "MultipleLiteralGroup", "FailedGroupPublishesNothing", "TemporalParameterMetadata", "DynamicTemporalBoundary", "ACLInvalidation"} {
		t.Run(scenario, func(t *testing.T) {
			p := newReferencePair(t)
			backends := []struct {
				name string
				db   *sql.DB
			}{{"native", p.memory}, {"postgres", p.postgres}}
			if scenario == "ACLInvalidation" {
				// Native authority is exercised with actual internal SetObjectACL in
				// TestSequencePreparedAuthority; the public adapter has no ACL setter.
				backends = backends[1:]
			}
			for _, backend := range backends {
				t.Run(backend.name, func(t *testing.T) {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					exec := func(q string) { t.Helper(); sequenceProbeExec(t, p.ctx, c, q) }
					hold := func(q string) *sql.Stmt {
						t.Helper()
						s, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(q, err)
						}
						t.Cleanup(func() { s.Close() })
						return s
					}
					value := func(s *sql.Stmt, want int64) {
						t.Helper()
						var got int64
						if err := s.QueryRowContext(p.ctx).Scan(&got); err != nil || got != want {
							t.Fatalf("held got=%d want=%d err=%v", got, want, err)
						}
					}
					failure := func(s *sql.Stmt, code, message, position string) {
						t.Helper()
						var got int64
						err := s.QueryRowContext(p.ctx).Scan(&got)
						want := controlsReferenceDiagnostic(code, message)
						want.Position = position
						referenceAssertDiagnostic(t, err, &want)
					}
					shadow := func() *sql.Stmt {
						for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
							exec(q)
						}
						s := hold("SELECT nextval('s')")
						exec("CREATE SEQUENCE a.s START 100")
						return s
					}
					if backend.name == "postgres" {
						defer func() {
							sequenceProbeExec(t, p.ctx, c, "RESET SESSION AUTHORIZATION")
							sequenceProbeExec(t, p.ctx, c, "SET search_path TO public")
							sequenceProbeExec(t, p.ctx, c, "DROP SCHEMA IF EXISTS a CASCADE")
							sequenceProbeExec(t, p.ctx, c, "DROP SCHEMA IF EXISTS b CASCADE")
							sequenceProbeExec(t, p.ctx, c, "DROP SCHEMA IF EXISTS moved CASCADE")
						}()
					}
					switch scenario {
					case "SchemaMove":
						exec("CREATE SCHEMA moved")
						exec("SET search_path TO public")
						exec("CREATE SEQUENCE s START 20")
						s, qualified := hold("SELECT nextval('s')"), hold("SELECT nextval('public.s')")
						exec("ALTER SEQUENCE s SET SCHEMA moved")
						failure(s, "42P01", `relation "s" does not exist`, "16")
						failure(qualified, "42P01", `relation "public.s" does not exist`, "16")
						exec("SET search_path TO moved,public")
						value(s, 20)
						exec("CREATE SEQUENCE public.s START 70")
						value(qualified, 70)
					case "EffectivePath":
						for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s START 10", "CREATE SEQUENCE b.s START 100", "SET search_path TO a,b"} {
							exec(q)
						}
						s := hold("SELECT nextval('s')")
						value(s, 10)
						exec("SET search_path TO b,a")
						value(s, 100)
						exec("CREATE TEMP SEQUENCE s START 200")
						value(s, 200)
					case "GeneratorDoesNotInvalidate":
						s := shadow()
						value(s, 10)
						sequenceProbeValue(t, p.ctx, c, "SELECT setval('b.s',40,true)", 40)
						sequenceProbeValue(t, p.ctx, c, "SELECT currval('b.s')", 40)
						sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 40)
						exec("SET search_path TO a,a,b")
						value(s, 41)
						sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 100)
					case "OptionInvalidation":
						var actor string
						if err := c.QueryRowContext(p.ctx, "SELECT current_user").Scan(&actor); err != nil {
							t.Fatal(err)
						}
						for i, option := range []string{"AS BIGINT", "INCREMENT 1", "START 10", "RESTART 10", "MINVALUE 1", "NO MINVALUE", "MAXVALUE 9223372036854775807", "NO MAXVALUE", "CACHE 1", "CYCLE", "NO CYCLE", "SET UNLOGGED", "OWNED BY NONE", "OWNED BY b.t.c", "OWNER TO postgres", "SET LOGGED", "SET UNLOGGED"} {
							if i == 0 {
								exec("CREATE SCHEMA a")
								exec("CREATE SCHEMA b")
								exec("CREATE TABLE b.t(c INT)")
								exec("SET search_path TO a,b")
							}
							exec("CREATE SEQUENCE b.s START 10")
							if i == 16 {
								exec("ALTER SEQUENCE b.s SET UNLOGGED")
							}
							if option == "OWNER TO postgres" {
								option = `OWNER TO "` + actor + `"`
							}
							s := hold("SELECT nextval('s')")
							exec("CREATE SEQUENCE a.s START 100")
							// Bare ALTER is a syntax error, not a no-op option. It
							// must leave the held binding and generator untouched.
							_, syntaxErr := c.ExecContext(p.ctx, "ALTER SEQUENCE b.s")
							syntaxWant := controlsReferenceDiagnostic("42601", "syntax error at end of input")
							syntaxWant.Position = "19"
							referenceAssertDiagnostic(t, syntaxErr, &syntaxWant)
							exec("ALTER SEQUENCE b.s " + option)
							want := int64(100)
							if i >= 12 {
								want = 10
							}
							value(s, want)
							exec("DROP SEQUENCE a.s,b.s")
						}
					case "DropRecreate":
						exec("CREATE SEQUENCE s START 10")
						next, curr := hold("SELECT nextval('s')"), hold("SELECT currval('s')")
						exec("DROP SEQUENCE s")
						failure(next, "42P01", `relation "s" does not exist`, "16")
						failure(curr, "42P01", `relation "s" does not exist`, "16")
						exec("CREATE SEQUENCE s START 90")
						failure(curr, "55000", `currval of sequence "s" is not yet defined in this session`, "")
						value(next, 90)
						value(curr, 90)
						sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 90)
					case "Transactional":
						exec("CREATE SEQUENCE s")
						s := hold("SELECT nextval('s')")
						exec("BEGIN")
						exec("ALTER SEQUENCE s RENAME TO identity")
						failure(s, "42P01", `relation "s" does not exist`, "16")
						exec("ROLLBACK")
						value(s, 1)
						exec("BEGIN")
						exec("ALTER SEQUENCE s RESTART 100")
						value(s, 100)
						exec("ROLLBACK")
						value(s, 2)
						exec("BEGIN")
						exec("ALTER SEQUENCE s RENAME TO identity")
						exec("COMMIT")
						failure(s, "42P01", `relation "s" does not exist`, "16")
					case "MultipleLiteralGroup":
						for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
							exec(q)
						}
						s := hold("SELECT nextval('s'),nextval('t')")
						for _, q := range []string{"CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE a.t START 200", "ALTER SEQUENCE b.t CACHE 1"} {
							exec(q)
						}
						var a, b int64
						if err := s.QueryRowContext(p.ctx).Scan(&a, &b); err != nil || a != 100 || b != 200 {
							t.Fatal(a, b, err)
						}
					case "FailedGroupPublishesNothing":
						for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
							exec(q)
						}
						s := hold("SELECT nextval('s'),nextval('t')")
						exec("CREATE SEQUENCE a.s START 100")
						exec("ALTER SEQUENCE b.t RENAME TO identity")
						var a, b int64
						err := s.QueryRowContext(p.ctx).Scan(&a, &b)
						want := controlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
						want.Position = "29"
						referenceAssertDiagnostic(t, err, &want)
						exec("CREATE SEQUENCE a.t START 200")
						if err := s.QueryRowContext(p.ctx).Scan(&a, &b); err != nil || a != 100 || b != 200 {
							t.Fatal("partial group publication/effect", a, b, err)
						}
					case "TemporalParameterMetadata":
						for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
							exec(q)
						}
						s := hold("SELECT nextval('s'),TIMESTAMP '2000-01-01',setval('t',$1,false)")
						check := func(want int64) {
							rows, err := s.QueryContext(p.ctx, int64(70))
							if err != nil {
								t.Fatal(err)
							}
							defer rows.Close()
							types, err := rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							if len(types) != 3 || types[0].DatabaseTypeName() != "INT8" || types[1].DatabaseTypeName() != "TIMESTAMP" || types[2].DatabaseTypeName() != "INT8" {
								t.Fatal(types)
							}
							if !rows.Next() {
								t.Fatal(rows.Err())
							}
							var got, set int64
							var stamp time.Time
							if err := rows.Scan(&got, &stamp, &set); err != nil || got != want || set != 70 || !stamp.Equal(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
								t.Fatal(got, stamp, set, err)
							}
							if rows.Next() || rows.Err() != nil {
								t.Fatal(rows.Err())
							}
						}
						check(10)
						for _, q := range []string{"CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE a.t START 200", "ALTER SEQUENCE b.s CACHE 1"} {
							exec(q)
						}
						check(100)
						sequenceProbeValue(t, p.ctx, c, "SELECT nextval('a.t')", 70)
						sequenceProbeValue(t, p.ctx, c, "SELECT nextval('b.t')", 70)
					case "DynamicTemporalBoundary":
						exec("CREATE SEQUENCE s START 10")
						beforePrepare := time.Now().UTC()
						s := hold("SELECT nextval('s'),TIMESTAMP 'now'")
						afterPrepare := time.Now().UTC()
						var value int64
						var first, second time.Time
						if err := s.QueryRowContext(p.ctx).Scan(&value, &first); err != nil || value != 10 {
							t.Fatal(value, err)
						}
						if first.Before(beforePrepare.Add(-time.Microsecond)) || first.After(afterPrepare.Add(time.Microsecond)) {
							t.Fatal("Prepare temporal outside independently captured bounds", first, beforePrepare, afterPrepare)
						}
						exec("ALTER SEQUENCE s CACHE 1")
						beforeReanalysis := time.Now().UTC()
						err := s.QueryRowContext(p.ctx).Scan(&value, &second)
						afterReanalysis := time.Now().UTC()
						if err != nil || value != 11 {
							t.Fatal(value, err)
						}
						if backend.name == "native" {
							if !second.Equal(first) {
								t.Fatal("native Prepare temporal capture changed", first, second)
							}
						} else if second.Before(beforeReanalysis.Add(-time.Microsecond)) || second.After(afterReanalysis.Add(time.Microsecond)) {
							t.Fatal("PG autocommit reanalysis transaction-start outside independent bounds", second, beforeReanalysis, afterReanalysis)
						}
					case "ACLInvalidation":
						s := shadow()
						exec("GRANT USAGE ON SEQUENCE b.s TO PUBLIC")
						value(s, 100)
					}
				})
			}
		})
	}
}

func referenceSequencePreparedQueued(t *testing.T) {
	for _, action := range []string{"rename-commit", "rename-rollback", "drop-commit"} {
		t.Run("Queued/"+action, func(t *testing.T) {
			p := newReferencePair(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s START 10")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 10)
			var oid int64
			if err := a.QueryRowContext(p.ctx, "SELECT 's'::regclass::oid::bigint").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			held, err := b.PrepareContext(p.ctx, "SELECT nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			query := "ALTER SEQUENCE s RENAME TO identity"
			if action == "drop-commit" {
				query = "DROP SEQUENCE s"
			}
			referenceLockExec(t, p.ctx, tx, query, 0)
			await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
				rows, err := held.QueryContext(ctx)
				return relationOutcome{rows: rows, err: err}
			})
			referenceSequenceWaitBehind(t, p.ctx, bp, ap, oid)
			if action == "rename-rollback" {
				err = tx.Rollback()
			} else {
				err = tx.Commit()
			}
			if err != nil {
				t.Fatal(err)
			}
			outcome := await()
			if action == "drop-commit" {
				if outcome.rows != nil {
					outcome.rows.Close()
					t.Fatal("failed original-OID opening returned cursor")
				}
				want := controlsReferenceDiagnostic("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
				referenceAssertDiagnostic(t, outcome.err, &want)
			} else {
				if outcome.err != nil {
					t.Fatal(outcome.err)
				}
				referenceSequenceRows(t, outcome.rows, "nextval", 11)
			}
			if action != "rename-rollback" {
				var v int64
				err := held.QueryRowContext(p.ctx).Scan(&v)
				want := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
				want.Position = "16"
				referenceAssertDiagnostic(t, err, &want)
				if action == "rename-commit" {
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('identity')", 12)
				}
			} else {
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 12)
			}
		})
	}
}
