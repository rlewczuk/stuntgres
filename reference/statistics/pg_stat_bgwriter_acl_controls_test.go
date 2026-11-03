package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatBGWriterACLControls(t *testing.T) {
	for i, name := range []string{"pg_stat_get_bgwriter_buf_written_clean", "pg_stat_get_bgwriter_maxwritten_clean", "pg_stat_get_buf_alloc", "pg_stat_get_bgwriter_stat_reset_time"} {
		t.Run(name, func(t *testing.T) {
			bgwriterACLControls(t, name, ([]string{"2772", "2773", "2859", "3075"})[i], bgwriterNames[i])
		})
	}
}

func bgwriterACLControls(t *testing.T, name, oid, column string) {
	bgwriterACLQuery := "SELECT proacl::text AS acl,has_function_privilege('pg_signal_backend',oid,'EXECUTE') AS allowed FROM pg_catalog.pg_proc WHERE oid="+oid
	bgwriterACLLocator := ` WHERE oid=E'"pg_catalog"."`+name+`"()'::pg_catalog.regprocedure;`
	selectedSQL := "SELECT "+column+" IS NOT NULL AS present FROM pg_catalog.pg_stat_bgwriter"
	// Raw order, duplicates, grant options and grantor identities are state,
	// independently of whether their effective EXECUTE rights happen to agree.
	cases := []struct {
		name    string
		raw     any
		literal string
		allowed bool
	}{
		{"implicit", nil, "NULL", true},
		{"empty", "{}", `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)`, false},
		{"explicit_default", "{=X/postgres,postgres=X/postgres}", "'{=X/postgres,postgres=X/postgres}'", true},
		{"reordered_default", "{postgres=X/postgres,=X/postgres}", "'{postgres=X/postgres,=X/postgres}'", true},
		{"duplicate_public", "{=X/postgres,postgres=X/postgres,=X/postgres}", "'{=X/postgres,postgres=X/postgres,=X/postgres}'", true},
		{"grant_option", "{postgres=X/postgres,pg_signal_backend=X*/postgres}", "'{postgres=X/postgres,pg_signal_backend=X*/postgres}'", true},
		{"split_grant", "{pg_signal_backend=X/postgres,postgres=X/postgres,pg_signal_backend=X*/postgres}", "'{pg_signal_backend=X/postgres,postgres=X/postgres,pg_signal_backend=X*/postgres}'", true},
		{"alternate_grantor", "{postgres=X/postgres,pg_signal_backend=X/pg_read_all_stats}", "'{postgres=X/postgres,pg_signal_backend=X/pg_read_all_stats}'", true},
	}
	emitted := make(map[string]string)
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE bgwriter_acl_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO bgwriter_acl_sentinel VALUES(37)", 1)
		var original sql.NullString
		if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_catalog.pg_proc WHERE oid="+oid).Scan(&original); err != nil {
			t.Fatal(err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
			if b.PG {
				var raw any
				if original.Valid {
					raw = original.String
				}
				if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid="+oid, raw); err != nil {
					t.Error("restore original oracle ACL", err)
				}
			}
		}()
		// Other scenarios can have restored an explicit effective default on
		// the shared oracle. Normalize only this test's starting state and
		// restore its exact prior raw value in cleanup.
		if b.PG {
			fixture.StatisticsExec(t, p, b, c, "UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid="+oid, 1)
		}
		held := bgwriterHeld(t, p, c, selectedSQL)
		defer held.Statement.Close()
		aclHeld := bgwriterHeld(t, p, c, bgwriterACLQuery)
		defer aclHeld.Statement.Close()
		bgwriterCheckACL(t, bgwriterACLQuery, name, p, b, c, held, aclHeld, nil, true)
		var pristine bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &pristine); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(pristine.String(), name) {
				t.Fatal("implicit PUBLIC backing ACL needs no replay", pristine.String())
			}
		}

		// Ordinary live GRANT/REVOKE must use this descriptor, not a second
		// restricted seed or a lookup-only routine identity.
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog."+name+"() FROM PUBLIC", 0)
		bgwriterCheckACL(t, bgwriterACLQuery, name, p, b, c, held, aclHeld, "{"+owner+"=X/"+owner+"}", false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+name+"() TO pg_signal_backend WITH GRANT OPTION", 0)))
		bgwriterCheckACL(t, bgwriterACLQuery, name, p, b, c, held, aclHeld, "{"+owner+"=X/"+owner+",pg_signal_backend=X*/"+owner+"}", true)

		var destination *sql.DB
		var dest *sql.Conn
		var destBackend fixture.Backend
		if !b.PG {
			connector, err := stuntgres.NewDriver().OpenConnector("bgwriter-acl-destination")
			if err != nil {
				t.Fatal(err)
			}
			destination = sql.OpenDB(connector)
			defer destination.Close()
			pool, recorder := fixture.RecordedNativePool(t, connector)
			destBackend = fixture.Backend{Name: "bgwriter-acl-destination", DB: pool, Recorder: recorder}
			dest, err = pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer dest.Close()
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				repair := "UPDATE pg_catalog.pg_proc SET proacl=" + tc.literal + "::pg_catalog.aclitem[]" + bgwriterACLLocator + "\n"
				if b.PG {
					if tc.raw != nil {
						repair = emitted[tc.name]
						if repair == "" {
							t.Fatal("missing actual emitted ACL SQL", tc.name)
						}
					}
					fixture.StatisticsExec(t, p, b, c, repair, 1)
					bgwriterCheckACL(t, bgwriterACLQuery, name, p, b, c, held, aclHeld, tc.raw, tc.allowed)
					return
				}
				// Raw repair is an isolated Restore capability, never ordinary
				// live catalog DML. Start from a genuine complete native dump.
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(pristine.String()+repair)); err != nil {
					t.Fatal("install raw ACL", err)
				}
				bgwriterCheckACL(t, bgwriterACLQuery, name, p, b, c, held, aclHeld, tc.raw, tc.allowed)
				var snapshot bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
					t.Fatal(err)
				}
				var repairs []string
				for line := range strings.SplitSeq(snapshot.String(), "\n") {
					if strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") && strings.HasSuffix(line, bgwriterACLLocator) {
						repairs = append(repairs, line)
					}
				}
				if tc.raw == nil {
					if len(repairs) != 0 {
						t.Fatal("raw absence was needlessly serialized", repairs)
					}
				} else {
					if len(repairs) != 1 {
						t.Fatal("explicit ACL requires exactly one raw repair", repairs)
					}
					emitted[tc.name] = repairs[0]
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal("same-target ACL replay", err)
				}
				bgwriterCheckACL(t, bgwriterACLQuery, name, p, b, c, held, aclHeld, tc.raw, tc.allowed)
				if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal("independent destination ACL replay", err)
				}
				destHeld := bgwriterHeld(t, p, dest, selectedSQL)
				defer destHeld.Statement.Close()
				destACLHeld := bgwriterHeld(t, p, dest, bgwriterACLQuery)
				defer destACLHeld.Statement.Close()
				bgwriterCheckACL(t, bgwriterACLQuery, name, p, destBackend, dest, destHeld, destACLHeld, tc.raw, tc.allowed)
				// Replay a fresh dump from that destination as well, so raw
				// distinctions survive capture after principal remapping.
				snapshot.Reset()
				if err := stuntgres.Dump(p.Ctx, destination, &snapshot); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal(err)
				}
				bgwriterCheckACL(t, bgwriterACLQuery, name, p, destBackend, dest, destHeld, destACLHeld, tc.raw, tc.allowed)
			})
		}
		if !b.PG {
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			checkUnchanged := func() {
				t.Helper()
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed ACL repair changed catalog/raw authority", err)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM bgwriter_acl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
			}
			for _, repair := range []string{
				"UPDATE pg_catalog.pg_proc SET proacl='{=X*/postgres}'::pg_catalog.aclitem[]"+bgwriterACLLocator,
				"UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid=2772::pg_catalog.oid;",
				"UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.bgwriter_missing()'::pg_catalog.regprocedure;",
			} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(before.String()+repair)); err == nil {
					t.Fatal("invalid bounded ACL repair accepted", repair)
				}
				checkUnchanged()
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(pristine.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary canceled ACL replacement", err)
			}
			checkUnchanged()
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid="+oid, bgwriterDiagnostic("0A000", "catalog source mutation is unsupported"))
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				checkUnchanged()
			}
		}
	})
}

func bgwriterCheckACL(t *testing.T, bgwriterACLQuery, name string, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held, aclHeld fixture.HeldStatement, raw any, allowed bool) {
	t.Helper()
	step := fixture.Select(bgwriterACLQuery, []string{"acl", "allowed"}, []string{"TEXT", "BOOL"}, [][]any{{raw, allowed}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
	fixture.RunStep(t, p.Ctx, aclHeld, b.Recorder, step)
	fixture.StatisticsExec(t, p, b, c, "SET ROLE pg_signal_backend", 0)
	for _, executor := range []fixture.Executor{c, held} {
		if allowed {
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(held.SQL, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		} else {
			denied := fixture.ErrorDiagnostic(held.SQL, bgwriterDiagnostic("42501", "permission denied for function "+name))
			denied.SelectRows, denied.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, denied)
		}
	}
	fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
}