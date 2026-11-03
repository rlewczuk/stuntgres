package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatDatabaseTransactions(t *testing.T) {
	statDatabaseScenario(t, "transactions", func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			target := create(statDatabasePreparedLabel(prepared))
			admissionBase := statDatabaseSetup(t, p, target, observer)
			writer, retire := statDatabaseConn(t, p, target)
			base := statDatabaseWriterBaseline(t, p, target, observer, writer, admissionBase, 0)
			run := func(q string, n int64) { statDatabaseExec(t, p, target, writer, prepared, q, n) }
			// Controls are direct: Parse/Describe/Sync outside an explicit block can
			// itself complete a transaction on PostgreSQL. Prepared DML stays inside.
			statsExec(t, p, target, writer, "BEGIN", 0)
			run("INSERT INTO stat_db_rows VALUES(11)", 1)
			if v := statDatabaseGet(t, p, observer, target.Name); v.Insert != base.Insert {
				t.Fatal("unresolved insert published", v, base)
			}
			statsExec(t, p, target, writer, "COMMIT", 0)
			statsExec(t, p, target, writer, "BEGIN", 0)
			run("INSERT INTO stat_db_rows VALUES(22)", 1)
			statsExec(t, p, target, writer, "ROLLBACK", 0)
			retire()
			got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool {
				return v.Backends == 0 && v.Insert == base.Insert+2 && v.Rollback == base.Rollback+1
			})
			statDatabaseWantRetiredDelta(t, base, got, 1, 1, 2, 0, 0, 0, 0, 0, 0)
			verify, endVerify := statDatabaseConn(t, p, target)
			fixture.RunStep(t, p.Ctx, verify, target.Recorder, fixture.Select("SELECT id FROM stat_db_rows ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
			endVerify()
		}
		// A read-only explicit top transaction is a commit, not a write count.
		target := create("readonly")
		admissionBase := statDatabaseSetup(t, p, target, observer)
		writer, retire := statDatabaseConn(t, p, target)
		base := statDatabaseWriterBaseline(t, p, target, observer, writer, admissionBase, 0)
		statsExec(t, p, target, writer, "BEGIN READ ONLY", 0)
		fixture.RunStep(t, p.Ctx, writer, target.Recorder, fixture.Select("SELECT 1 AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(1)}}))
		statsExec(t, p, target, writer, "COMMIT", 0)
		retire()
		got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Commit == base.Commit+1 })
		statDatabaseWantRetiredDelta(t, base, got, 1, 0, 0, 0, 0, 0, 0, 0, 0)
	})
}
func TestReferenceStatDatabaseTuples(t *testing.T) {
	for _, commit := range []bool{false, true} {
		label := "abort"
		if commit {
			label = "commit"
		}
		t.Run(label, func(t *testing.T) {
			statDatabaseScenario(t, "tuples_"+label, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
				target := create("target")
				admissionBase := statDatabaseSetup(t, p, target, observer)
				writer, retire := statDatabaseConn(t, p, target)
				base := statDatabaseWriterBaseline(t, p, target, observer, writer, admissionBase, 0)
				for _, step := range []fixture.Step{fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO stat_db_rows VALUES(1),(2),(3)", 3), fixture.Exec("UPDATE stat_db_rows SET id=11 WHERE id=1", 1), fixture.Exec("DELETE FROM stat_db_rows WHERE id=2", 1), fixture.Exec("SAVEPOINT child", 0), fixture.Exec("INSERT INTO stat_db_rows VALUES(44)", 1), fixture.Exec("ROLLBACK TO child", 0), fixture.Exec("RELEASE child", 0)} {
					fixture.RunStep(t, p.Ctx, writer, target.Recorder, step)
				}
				end := "ROLLBACK"
				c, r := int64(0), int64(1)
				if commit {
					end = "COMMIT"
					c, r = 1, 0
				}
				statsExec(t, p, target, writer, end, 0)
				retire()
				got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool {
					return v.Backends == 0 && v.Insert == base.Insert+4 && v.Update == base.Update+1 && v.Delete == base.Delete+1
				})
				statDatabaseWantRetiredDelta(t, base, got, c, r, 4, 1, 1, 0, 0, 0, 0)
				// Retiring the relation never retires database history.
				writer, retire = statDatabaseConn(t, p, target)
				statsExec(t, p, target, writer, "DROP TABLE stat_db_rows", 0)
				retire()
				after := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == got.Sessions+1 })
				if after.Insert < got.Insert || after.Update < got.Update || after.Delete < got.Delete {
					t.Fatal("drop erased historical attempts", got, after)
				}
			})
		})
	}
	for _, commit := range []bool{false, true} {
		label := "truncate_abort"
		if commit {
			label = "truncate_commit"
		}
		t.Run(label, func(t *testing.T) {
			statDatabaseScenario(t, "tuples_"+label, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
				target := create("target")
				admissionBase := statDatabaseSetup(t, p, target, observer)
				writer, retire := statDatabaseConn(t, p, target)
				base := statDatabaseWriterBaseline(t, p, target, observer, writer, admissionBase, 0)
				for _, step := range []fixture.Step{fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO stat_db_rows VALUES(1),(2)", 2), fixture.Exec("TRUNCATE stat_db_rows", 0), fixture.Exec("INSERT INTO stat_db_rows VALUES(3)", 1)} {
					fixture.RunStep(t, p.Ctx, writer, target.Recorder, step)
				}
				end := "ROLLBACK"
				c, r, i := int64(0), int64(1), int64(2)
				if commit {
					end = "COMMIT"
					c, r, i = 1, 0, 1
				}
				statsExec(t, p, target, writer, end, 0)
				retire()
				got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Insert == base.Insert+i })
				// TRUNCATE also updates real PostgreSQL catalog tuples. The user insert
				// first-pre component is exact; catalog update/delete totals are not zero.
				if got.Commit-base.Commit != c || got.Rollback-base.Rollback != r || got.Sessions != base.Sessions || got.Abandoned != base.Abandoned || got.Fatal != base.Fatal || got.Killed != base.Killed || got.Update < base.Update || got.Delete < base.Delete {
					t.Fatal(base, got)
				}
			})
		})
	}
}

// This scenario is a separate end-to-end feature smoke, not a schema alias.
func TestReferenceStatDatabaseFeatureSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(statDatabasePreparedLabel(prepared), func(t *testing.T) {
			statDatabaseScenario(t, "smoke_"+statDatabasePreparedLabel(prepared), func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
				target := create("target")
				other := create("other")
				admissionBase := statDatabaseSetup(t, p, target, observer)
				otherBefore := statDatabaseGet(t, p, observer, other.Name)
				writer, retire := statDatabaseConn(t, p, target)
				if live := statDatabaseGet(t, p, observer, target.Name); live.Backends != admissionBase.Backends+1 {
					t.Fatal("admission not live", live, admissionBase)
				}
				preparationCommits := int64(0)
				var insert *sql.Stmt
				if prepared {
					var err error
					insert, err = writer.PrepareContext(p.Ctx, "INSERT INTO stat_db_rows VALUES($1)")
					if err != nil {
						t.Fatal(err)
					}
					defer insert.Close()
					preparationCommits = 1
				}
				base := statDatabaseWriterBaseline(t, p, target, observer, writer, admissionBase, preparationCommits)
				executeInsert := func(value int64) {
					if insert == nil {
						statDatabaseExec(t, p, target, writer, false, "INSERT INTO stat_db_rows VALUES("+fmt.Sprint(value)+")", 1)
						return
					}
					result, err := insert.ExecContext(p.Ctx, value)
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
				}
				statsExec(t, p, target, writer, "BEGIN", 0)
				executeInsert(11)
				statsExec(t, p, target, writer, "COMMIT", 0)
				statsExec(t, p, target, writer, "BEGIN", 0)
				executeInsert(22)
				statsExec(t, p, target, writer, "ROLLBACK", 0)
				if insert != nil {
					if err := insert.Close(); err != nil {
						t.Fatal(err)
					}
				}
				retire()
				got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool {
					return v.Backends == admissionBase.Backends && v.Insert == base.Insert+2 && v.Rollback == base.Rollback+1
				})
				want := base
				want.Commit++
				want.Rollback++
				want.Insert += 2
				want.Backends = admissionBase.Backends
				want.Sessions = admissionBase.Sessions + 1
				if got != want {
					t.Fatal("independent post-prepare commit/abort delta and pre-admission lifecycle", got, want)
				}
				if after := statDatabaseGet(t, p, observer, other.Name); after != otherBefore {
					t.Fatal("other database changed", otherBefore, after)
				}
				writer, end := statDatabaseConn(t, p, target)
				step := fixture.Select("SELECT id FROM stat_db_rows ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}})
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, writer, target.Recorder, step)
				end()
				step = fixture.Select("SELECT * FROM pg_catalog.pg_stat_database WHERE false", statDatabaseNames, statDatabaseTypes, nil)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, step)
				// Hold A's full cumulative snapshot across genuine B admission and
				// C publication. A different none-mode reader proves both events
				// without clearing A or flushing any writer.
				baseTarget := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool {
					return v.Sessions == got.Sessions+1 && v.Backends == 0
				})
				baseOther := statDatabaseSetup(t, p, other, observer)
				statsExec(t, p, b, observer, "CREATE TABLE stat_db_smoke_local(id integer)", 0)
				fresh, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer fresh.Close()
				statsExec(t, p, b, fresh, "SET stats_fetch_consistency=none", 0)
				statsExec(t, p, b, observer, "SET stats_fetch_consistency=snapshot", 0)
				statsExec(t, p, b, observer, "BEGIN", 0)
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_stat_user_tables WHERE relname='stat_db_smoke_local'", []string{"relname"}, []string{"NAME"}, [][]any{{"stat_db_smoke_local"}}))
				if left, right := statDatabaseGet(t, p, observer, target.Name), statDatabaseGet(t, p, observer, other.Name); left != baseTarget || right != baseOther {
					t.Fatal("full snapshot did not capture B and C", left, baseTarget, right, baseOther)
				}
				_, retireLive := statDatabaseConn(t, p, target)
				wantHeld := baseTarget
				if !b.PG {
					wantHeld.Backends++
				}
				if held := statDatabaseGet(t, p, observer, target.Name); held != wantHeld {
					t.Fatal("native live versus released cached backend status", held, wantHeld)
				}
				if live := statDatabaseGet(t, p, fresh, target.Name); live.Backends != 1 {
					t.Fatal("independent actual B admission", live)
				}
				otherWriter, retireOther := statDatabaseConn(t, p, other)
				statsExec(t, p, other, otherWriter, "BEGIN", 0)
				statsExec(t, p, other, otherWriter, "INSERT INTO stat_db_rows VALUES(33)", 1)
				statsExec(t, p, other, otherWriter, "COMMIT", 0)
				retireOther()
				publishedOther := statDatabaseAwait(t, p, fresh, other.Name, func(v statDatabaseVector) bool {
					return v.Insert == baseOther.Insert+1 && v.Backends == 0
				})
				statDatabaseWantRetiredDelta(t, baseOther, publishedOther, statDatabaseStartupCommits(other)+1, 0, 1, 0, 0, 1, 0, 0, 0)
				retireLive()
				publishedTarget := statDatabaseAwait(t, p, fresh, target.Name, func(v statDatabaseVector) bool {
					return v.Sessions == baseTarget.Sessions+1 && v.Backends == 0
				})
				statDatabaseWantRetiredDelta(t, baseTarget, publishedTarget, statDatabaseStartupCommits(target), 0, 0, 0, 0, 1, 0, 0, 0)
				if left, right := statDatabaseGet(t, p, observer, target.Name), statDatabaseGet(t, p, observer, other.Name); left != baseTarget || right != baseOther {
					t.Fatal("held cumulative snapshot changed after publication/retirement", left, baseTarget, right, baseOther)
				}
				statsClear(t, p, b, observer)
				if left, right := statDatabaseGet(t, p, observer, target.Name), statDatabaseGet(t, p, observer, other.Name); left != publishedTarget || right != publishedOther {
					t.Fatal("clear omitted published B/C or refreshed backend status", left, publishedTarget, right, publishedOther)
				}
				statsExec(t, p, b, observer, "COMMIT", 0)
				statsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
				shared := fixture.Select("SELECT datid,datname,numbackends FROM pg_catalog.pg_stat_database WHERE datid=0", []string{"datid", "datname", "numbackends"}, []string{"OID", "NAME", "INT4"}, [][]any{{int64(0), nil, int64(0)}})
				shared.Prepared = prepared
				fixture.OidReferenceRows(t, p.Ctx, b, observer, shared)
				var checksums string
				if err := observer.QueryRowContext(p.Ctx, "SHOW data_checksums").Scan(&checksums); err != nil {
					t.Fatal(err)
				}
				off := checksums == "off" || checksums == "false"
				technical := fixture.Select("SELECT checksum_failures IS NULL AS no_checksums,checksum_last_failure IS NULL AS no_failure FROM pg_catalog.pg_stat_database WHERE datname=current_database()", []string{"no_checksums", "no_failure"}, []string{"BOOL", "BOOL"}, [][]any{{off, true}})
				technical.Prepared = prepared
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, technical)
				wantError := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: `cannot update view "pg_stat_database"`, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."}
				const update = "UPDATE pg_catalog.pg_stat_database SET datname='x' WHERE false"
				if prepared {
					statement, failure := observer.PrepareContext(p.Ctx, update)
					err = failure
					if statement != nil {
						_, err = statement.ExecContext(p.Ctx)
						statement.Close()
					}
				} else {
					_, err = observer.ExecContext(p.Ctx, update)
				}
				fixture.AssertDiagnostic(t, err, &wantError)
			})
		})
	}
}
