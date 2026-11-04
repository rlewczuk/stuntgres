package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const walACLQuery = "SELECT proacl::text AS acl,has_function_privilege('pg_signal_backend',oid,'EXECUTE') AS allowed FROM pg_catalog.pg_proc WHERE oid=1136"

// Dump quotes both identity components inside an escape string literal.
const walACLLocator = ` WHERE oid=E'"pg_catalog"."pg_stat_get_wal"()'::pg_catalog.regprocedure;`

func TestReferencePGStatWALACLControls(t *testing.T) {
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
		initial := walGet(t, p, b, c)
		var original sql.NullString
		if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_catalog.pg_proc WHERE oid=1136").Scan(&original); err != nil {
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
				if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid=1136", raw); err != nil {
					t.Error("restore original oracle ACL", err)
				}
			}
		}()
		// Other scenarios can have restored an explicit effective default on
		// the shared oracle. Normalize only this test's starting state and
		// restore its exact prior raw value in cleanup.
		if b.PG {
			fixture.StatisticsExec(t, p, b, c, "UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid=1136", 1)
		}
		held := archiverHeld(t, p, c, walSQL)
		defer held.Statement.Close()
		aclHeld := archiverHeld(t, p, c, walACLQuery)
		defer aclHeld.Statement.Close()
		walCheckACL(t, p, b, c, held, aclHeld, nil, true)
		var pristine bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &pristine); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(pristine.String(), "pg_stat_get_wal") {
				t.Fatal("implicit PUBLIC backing ACL needs no replay", pristine.String())
			}
		}

		// Ordinary live GRANT/REVOKE must use this descriptor, not a second
		// restricted seed or a lookup-only routine identity.
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() FROM PUBLIC", 0)
		walCheckACL(t, p, b, c, held, aclHeld, "{"+owner+"=X/"+owner+"}", false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_wal() TO pg_signal_backend WITH GRANT OPTION", 0)))
		walCheckACL(t, p, b, c, held, aclHeld, "{"+owner+"=X/"+owner+",pg_signal_backend=X*/"+owner+"}", true)

		var destination *sql.DB
		var dest *sql.Conn
		var destBackend fixture.Backend
		var destInitial walRow
		if !b.PG {
			connector, err := stuntgres.NewDriver().OpenConnector("wal-acl-destination")
			if err != nil {
				t.Fatal(err)
			}
			destination = sql.OpenDB(connector)
			defer destination.Close()
			pool, recorder := fixture.RecordedNativePool(t, connector)
			destBackend = fixture.Backend{Name: "wal-acl-destination", DB: pool, Recorder: recorder}
			dest, err = pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer dest.Close()
			destInitial = walGet(t, p, destBackend, dest)
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				repair := "UPDATE pg_catalog.pg_proc SET proacl=" + tc.literal + "::pg_catalog.aclitem[]" + walACLLocator + "\n"
				if b.PG {
					if tc.raw != nil {
						repair = emitted[tc.name]
						if repair == "" {
							t.Fatal("missing actual emitted ACL SQL", tc.name)
						}
					}
					fixture.StatisticsExec(t, p, b, c, repair, 1)
					walCheckACL(t, p, b, c, held, aclHeld, tc.raw, tc.allowed)
					return
				}
				// Raw repair is an isolated Restore capability, never ordinary
				// live catalog DML. Start from a genuine complete native dump.
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(pristine.String()+repair)); err != nil {
					t.Fatal("install raw ACL", err)
				}
				walCheckACL(t, p, b, c, held, aclHeld, tc.raw, tc.allowed)
				var snapshot bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
					t.Fatal(err)
				}
				var repairs []string
				for line := range strings.SplitSeq(snapshot.String(), "\n") {
					if strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") && strings.HasSuffix(line, walACLLocator) {
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
				walCheckACL(t, p, b, c, held, aclHeld, tc.raw, tc.allowed)
				walSame(t, initial, walGet(t, p, b, c))
				if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal("independent destination ACL replay", err)
				}
				destHeld := archiverHeld(t, p, dest, walSQL)
				defer destHeld.Statement.Close()
				destACLHeld := archiverHeld(t, p, dest, walACLQuery)
				defer destACLHeld.Statement.Close()
				walCheckACL(t, p, destBackend, dest, destHeld, destACLHeld, tc.raw, tc.allowed)
				walSame(t, destInitial, walGet(t, p, destBackend, dest))
				// Replay a fresh dump from that destination as well, so raw
				// distinctions survive capture after principal remapping.
				snapshot.Reset()
				if err := stuntgres.Dump(p.Ctx, destination, &snapshot); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal(err)
				}
				walCheckACL(t, p, destBackend, dest, destHeld, destACLHeld, tc.raw, tc.allowed)
				walSame(t, destInitial, walGet(t, p, destBackend, dest))
			})
		}
		walSame(t, initial, walGet(t, p, b, c))
	})
}

func walCheckACL(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held, aclHeld fixture.HeldStatement, raw any, allowed bool) {
	t.Helper()
	step := fixture.Select(walACLQuery, []string{"acl", "allowed"}, []string{"TEXT", "BOOL"}, [][]any{{raw, allowed}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
	fixture.RunStep(t, p.Ctx, aclHeld, b.Recorder, step)
	fixture.StatisticsExec(t, p, b, c, "SET ROLE pg_signal_backend", 0)
	for _, executor := range []fixture.Executor{c, held} {
		if allowed {
			walGet(t, p, b, executor)
		} else {
			denied := fixture.ErrorDiagnostic(walSQL, archiverDiagnostic("42501", "permission denied for function pg_stat_get_wal"))
			denied.SelectRows, denied.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, denied)
		}
	}
	fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
}
