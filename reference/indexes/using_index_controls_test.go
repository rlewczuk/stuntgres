package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceUsingIndexLocks(t *testing.T) {
	for _, holderSQL := range []string{"SELECT a FROM ux_t", "INSERT INTO ux_t VALUES(3,'z',NULL)"} {
		t.Run(holderSQL, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
				old := c.oid("ux_idx")
				holder, waiter := fixture.LockConnections(c.t, c.p.Ctx, c.b.DB)
				fixture.LockExec(c.t, c.p.Ctx, holder, "BEGIN", 0)
				if strings.HasPrefix(holderSQL, "SELECT") {
					r, err := holder.QueryContext(c.p.Ctx, holderSQL)
					if err != nil {
						c.t.Fatal(err)
					}
					for r.Next() {
					}
					err = r.Err()
					r.Close()
					if err != nil {
						c.t.Fatal(err)
					}
				} else {
					fixture.LockExec(c.t, c.p.Ctx, holder, holderSQL, 1)
				}
				fixture.LockExec(c.t, c.p.Ctx, waiter, "SET lock_timeout='10ms'", 0)
				s := fixture.ErrorDiagnostic("ALTER TABLE ux_t ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx", usingIndexDiagnostic("55P03", "canceling statement due to lock timeout"))
				s.ErrorStage = "execute"
				if c.prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(c.t, c.p.Ctx, waiter, c.b.Recorder, s)
				fixture.LockExec(c.t, c.p.Ctx, waiter, "RESET lock_timeout", 0)
				if c.oid("ux_idx") != old {
					c.t.Fatal("timeout leaked rename")
				}
				q := "ALTER TABLE ux_t ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx"
				if c.b.PG {
					hp, wp := fixture.LockPID(c.t, c.p.Ctx, holder), fixture.LockPID(c.t, c.p.Ctx, waiter)
					done := fixture.RowAsync(c.t, c.p.Ctx, waiter, q, c.prepared, false)
					fixture.LockWaitBehind(c.t, c.p.Ctx, wp, hp, c.oid("ux_t"), "AccessExclusiveLock")
					var canceled bool
					if err := fixture.Admin.QueryRowContext(c.p.Ctx, "SELECT pg_cancel_backend($1)", wp).Scan(&canceled); err != nil || !canceled {
						c.t.Fatal(canceled, err)
					}
					outcome := fixture.LockResult(c.t, done)
					want := usingIndexDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(c.t, outcome.Err, &want)
				} else {
					// This portable route proves request-admission cancellation.
					// TestUsingIndexBlockedCancellation separately observes a real
					// native AX waiter before canceling its live context.
					ctx, cancel := context.WithCancel(c.p.Ctx)
					cancel()
					outcome := fixture.RowExecute(ctx, waiter, q, c.prepared)
					if !errors.Is(outcome.Err, context.Canceled) {
						c.t.Fatal("native cancellation boundary", outcome.Err)
					}
				}
				if c.oid("ux_idx") != old {
					c.t.Fatal("cancel leaked rename")
				}
				fixture.LockExec(c.t, c.p.Ctx, holder, "ROLLBACK", 0)
				c.rows()
				// A successful attachment retains AX to transaction end, blocking a reader.
				c.exec("BEGIN", 0)
				defer c.conn.ExecContext(c.p.Ctx, "ROLLBACK")
				c.exec(q, 0)
				if c.ids("ux_owned")[2] != old {
					c.t.Fatal("retry replaced index")
				}
				fixture.LockExec(c.t, c.p.Ctx, waiter, "SET lock_timeout='10ms'", 0)
				readerTimeout := usingIndexDiagnostic("55P03", "canceling statement due to lock timeout")
				readerTimeout.Position = "15"
				fixture.RunStep(c.t, c.p.Ctx, waiter, c.b.Recorder, fixture.ErrorDiagnostic("SELECT a FROM ux_t", readerTimeout))
				c.exec("ROLLBACK", 0)
				fixture.LockExec(c.t, c.p.Ctx, waiter, "RESET lock_timeout", 0)
				c.rows()
				if c.oid("ux_idx") != old {
					c.t.Fatal("rollback retained AX candidate")
				}
				c.exec(q, 0)
				c.rows()
			})
		})
	}
}

func TestReferenceUsingIndexNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var actualDump string
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			c := &usingIndexCase{t: t, p: p, b: b, conn: conn, recorder: recorder, namespace: fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)}
			if b.PG && actualDump != "" {
				if _, err := conn.ExecContext(p.Ctx, actualDump); err != nil {
					t.Fatal("actual native dump PostgreSQL replay", err)
				}
			} else {
				c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
				c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_owned PRIMARY KEY USING INDEX ux_idx", 0)
				c.exec("CREATE TABLE ux_nnd(k INT,p TEXT)", 0)
				c.exec("CREATE UNIQUE INDEX ux_nnd_idx ON ux_nnd(k) INCLUDE(p) NULLS NOT DISTINCT", 0)
				c.exec("INSERT INTO ux_nnd VALUES(NULL,'kept')", 1)
				c.exec("ALTER TABLE ux_nnd ADD UNIQUE USING INDEX ux_nnd_idx", 0)
				c.exec("ALTER TABLE ux_t ALTER payload TYPE VARCHAR(40)", 0)
			}
			c.ids("ux_owned")
			q := "INSERT INTO ux_t VALUES($1,$2,$3)"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				actualDump = dump.String()
				if !strings.Contains(actualDump, "INCLUDE") || !strings.Contains(actualDump, "USING INDEX") || !strings.Contains(actualDump, "NULLS NOT DISTINCT") {
					t.Fatal("lossy actual dump", actualDump)
				}
				// Same-name replay reconstructs the group in a fresh generation;
				// numeric OIDs can coincide across Restore's isolated allocators.
				beforeSameName := c.ids("ux_owned")
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, recorder, fixture.Exec(q, 1, int64(99), "discard", "temporary"))
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(actualDump)); err != nil {
					t.Fatal("same-name attached Restore", err)
				}
				afterSameName := c.ids("ux_owned")
				if afterSameName[1] != beforeSameName[1] {
					t.Fatal("same-name Restore identities", beforeSameName, afterSameName)
				}
				c.run(fixture.Select("SELECT a,b,payload FROM ux_t ORDER BY a", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}}))
				c.attributes([][]any{{"a", int64(23), true}, {"b", int64(25), true}, {"payload", int64(1043), false}})
				c.indexAttributes("ux_owned", [][]any{{"a", int64(23)}, {"b", int64(25)}, {"payload", int64(1043)}})
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, recorder, fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("ux_t", "ux_owned", "Key (a, b)=(1, x) already exists."), int64(1), "x", "different"))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, recorder, fixture.ErrorDiagnostic(q, fixture.PrimaryKeyReferenceNull("ux_t", "a", "null, z, null"), nil, "z", nil))
				c.run(fixture.ErrorDiagnostic("INSERT INTO ux_nnd VALUES(NULL,'changed')", fixture.UniqueReferenceDuplicate("ux_nnd", "ux_nnd_idx", "Key (k)=(null) already exists.")))
				var sameNameDump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &sameNameDump); err != nil || sameNameDump.String() != actualDump {
					t.Fatal("same-name Restore noncanonical redump", err, sameNameDump.String())
				}
				// Native eager rows have no PG eager-cursor equivalent: explicit control phase.
				eager, err := conn.QueryContext(p.Ctx, "SELECT a,b,payload FROM ux_t ORDER BY a")
				if err != nil {
					t.Fatal(err)
				}
				defer eager.Close()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(actualDump)); err != nil {
					t.Fatal(err)
				}
				current := c.ids("ux_owned")
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != actualDump {
					t.Fatal("noncanonical actual roundtrip", err, again.String())
				}
				for _, want := range [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}} {
					if !eager.Next() {
						t.Fatal("held eager row lost", eager.Err())
					}
					var a int64
					var b string
					var payload any
					if err := eager.Scan(&a, &b, &payload); err != nil || a != want[0] || b != want[1] || payload != want[2] {
						t.Fatal(a, b, payload, err)
					}
				}
				if eager.Next() || eager.Err() != nil {
					t.Fatal(eager.Err())
				}
				eager.Close()
				c.exec("CREATE TABLE ux_sentinel(k INT)", 0)
				c.exec("INSERT INTO ux_sentinel VALUES(7)", 1)
				sentinel := c.oid("ux_sentinel")
				failedRows, err := b.DB.QueryContext(p.Ctx, "SELECT a,b,payload FROM ux_t ORDER BY a")
				if err != nil {
					t.Fatal(err)
				}
				defer failedRows.Close()
				for _, bad := range []string{
					"INSERT INTO public.ux_t VALUES(1,'x','bad');",
					"INSERT INTO public.ux_t VALUES(NULL,'z',NULL);",
					"ALTER TABLE public.ux_t ADD UNIQUE USING INDEX absent;",
					"ALTER TABLE public.ux_t ADD UNIQUE USING INDEX ux_owned nonsense;",
				} {
					err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(actualDump+bad))
					if err == nil {
						t.Fatal("bad replay accepted", bad)
					}
					switch bad {
					case "INSERT INTO public.ux_t VALUES(1,'x','bad');":
						want := fixture.UniqueReferenceDuplicate("ux_t", "ux_owned", "Key (a, b)=(1, x) already exists.")
						fixture.AssertDiagnostic(t, err, &want)
					case "INSERT INTO public.ux_t VALUES(NULL,'z',NULL);":
						want := fixture.PrimaryKeyReferenceNull("ux_t", "a", "null, z, null")
						fixture.AssertDiagnostic(t, err, &want)
					case "ALTER TABLE public.ux_t ADD UNIQUE USING INDEX absent;":
						want := usingIndexDiagnostic("42704", `index "absent" does not exist`)
						want.Position = fixture.Position(utf8.RuneCountInString(actualDump) + strings.Index(bad, "UNIQUE") + 1)
						fixture.AssertDiagnostic(t, err, &want)
					case "ALTER TABLE public.ux_t ADD UNIQUE USING INDEX ux_owned nonsense;":
						want := usingIndexDiagnostic("42601", `syntax error at or near "nonsense"`)
						want.Position = fixture.Position(utf8.RuneCountInString(actualDump) + strings.Index(bad, "nonsense") + 1)
						fixture.AssertDiagnostic(t, err, &want)
					}
					if c.oid("ux_sentinel") != sentinel || c.ids("ux_owned") != current {
						t.Fatal("failed replay changed destination")
					}
					c.run(fixture.Select("SELECT k FROM ux_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
				for _, want := range [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}} {
					if !failedRows.Next() {
						t.Fatal("failed replay lost held row", failedRows.Err())
					}
					var a int64
					var b string
					var payload any
					if err := failedRows.Scan(&a, &b, &payload); err != nil || a != want[0] || b != want[1] || payload != want[2] {
						t.Fatal(a, b, payload, err)
					}
				}
				if failedRows.Next() || failedRows.Err() != nil {
					t.Fatal(failedRows.Err())
				}
				failedRows.Close()
			}
			c.run(fixture.Select("SELECT a,b,payload FROM ux_t ORDER BY a", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}}))
			c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_owned'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (a, b) INCLUDE (payload)"}}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, recorder, fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("ux_t", "ux_owned", "Key (a, b)=(1, x) already exists."), int64(1), "x", nil))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, recorder, fixture.Exec(q, 1, int64(3), "z", nil))
			c.run(fixture.ErrorDiagnostic("INSERT INTO ux_nnd VALUES(NULL,'changed')", fixture.UniqueReferenceDuplicate("ux_nnd", "ux_nnd_idx", "Key (k)=(null) already exists.")))
		})
	}
	t.Run("provider_graph", usingIndexProviderGraph)
}

// The default's REGCLASS leaf points to the attached index itself. Together
// with the enum key/provider and view this exercises the actual predecessor
// graph, deferred defaults and final data, rather than just a plain table dump.
func usingIndexProviderGraph(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			var actualDump string
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					c := &usingIndexCase{t: t, p: p, b: b, conn: conn, recorder: recorder, prepared: prepared, namespace: fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)}
					if b.PG && actualDump != "" {
						if _, err := conn.ExecContext(p.Ctx, actualDump); err != nil {
							t.Fatal("provider graph actual PostgreSQL replay", err)
						}
					} else {
						c.exec("CREATE TYPE ux_mood AS ENUM ('cold','warm')", 0)
						c.exec("CREATE SEQUENCE ux_seq START WITH 7", 0)
						c.exec("CREATE TABLE ux_t(a INT DEFAULT nextval('ux_seq'::regclass),b ux_mood DEFAULT enum_last(NULL::ux_mood),payload TEXT DEFAULT 'kept',ref REGCLASS)", 0)
						c.exec("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload,ref)", 0)
						c.exec("ALTER TABLE ux_t ALTER ref SET DEFAULT 'ux_idx'::regclass", 0)
						c.exec("INSERT INTO ux_t DEFAULT VALUES", 1)
						c.exec("INSERT INTO ux_t DEFAULT VALUES", 1)
						before := c.oid("ux_idx")
						c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_owned PRIMARY KEY USING INDEX ux_idx", 0)
						if c.ids("ux_owned")[2] != before {
							t.Fatal("provider attachment rebuilt populated index")
						}
						c.exec("CREATE VIEW ux_graph_view AS SELECT a,b,payload,ref FROM ux_t", 0)
					}
					selectQ := "SELECT a,b::text AS mood,payload,ref::text AS backing FROM ux_graph_view ORDER BY a"
					selectStmt, err := conn.PrepareContext(p.Ctx, selectQ)
					if err != nil {
						t.Fatal(err)
					}
					defer selectStmt.Close()
					insertQ := "INSERT INTO ux_t(payload) VALUES($1)"
					insertStmt, err := conn.PrepareContext(p.Ctx, insertQ)
					if err != nil {
						t.Fatal(err)
					}
					defer insertStmt.Close()
					heldSelect := fixture.HeldStatement{Executor: conn, Statement: selectStmt, SQL: selectQ}
					check := func() {
						ids := c.ids("ux_owned")
						provider := fixture.Select("SELECT enum_last(NULL::ux_mood) AS v", []string{"v"}, []string{""}, [][]any{{"warm"}})
						provider.Prepared = prepared
						provider.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, false)
						fixture.EnumReferenceRunStep(t, p.Ctx, conn, recorder, provider)
						fixture.TableDDLMetadata(t, p.Ctx, conn, b.PG, prepared, "SELECT a,payload FROM ux_t WHERE FALSE", []string{"a", "payload"}, []string{"INT4", "TEXT"}, []bool{false, true}, 0)
						c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_owned'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (a, b) INCLUDE (payload, ref)"}}))
						fixture.RunStep(t, p.Ctx, heldSelect, recorder, fixture.Select(selectQ, []string{"a", "mood", "payload", "backing"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(7), "warm", "kept", "ux_owned"}, {int64(8), "warm", "kept", "ux_owned"}}))
						// Replay the actual public deparse; generated source aliases and
						// cosmetic whitespace are not attachment/provider semantics.
						viewDefinition := fixture.DeparseRelationsRead(t, p, b, conn, "SELECT pg_get_viewdef('ux_graph_view'::text) AS definition")
						c.run(fixture.Exec("CREATE VIEW ux_graph_copy AS "+viewDefinition, 0))
						c.run(fixture.Select("SELECT a,b::text AS mood,payload,ref::text AS backing FROM ux_graph_copy ORDER BY a", []string{"a", "mood", "payload", "backing"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(7), "warm", "kept", "ux_owned"}, {int64(8), "warm", "kept", "ux_owned"}}))
						c.run(fixture.Exec("DROP VIEW ux_graph_copy", 0))
						c.run(fixture.Select("SELECT adnum,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid=$1 ORDER BY adnum", []string{"adnum", "pg_get_expr"}, []string{"INT2", "TEXT"}, [][]any{{int64(1), "nextval('ux_seq'::regclass)"}, {int64(2), "enum_last(NULL::ux_mood)"}, {int64(3), "'kept'::text"}, {int64(4), "'ux_owned'::regclass"}}, ids[1]))
						typ := fixture.PgDependID(t, b, "SELECT 'ux_mood'::regtype::oid")
						seq := c.oid("ux_seq")
						refDefault := fixture.PgDependDefinition(t, b, ids[1], 4)
						seqDefault := fixture.PgDependDefinition(t, b, ids[1], 1)
						enumDefault := fixture.PgDependDefinition(t, b, ids[1], 2)
						view := c.oid("ux_graph_view")
						rule := fixture.PgDependRule(t, b, view)
						c.run(fixture.Select("SELECT refobjsubid FROM pg_depend WHERE classid=2606 AND objid=$1 AND refclassid=1259 AND refobjid=$2 AND deptype='a' ORDER BY refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}, ids[0], ids[1]))
						for _, edge := range []struct{ class, object, refclass, ref int64 }{{1259, ids[1], 1247, typ}, {2604, refDefault, 1259, ids[2]}, {2604, seqDefault, 1259, seq}, {2604, enumDefault, 1247, typ}} {
							c.run(fixture.Select("SELECT refobjid::bigint FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=$3 AND refobjid=$4 AND deptype='n'", []string{"refobjid"}, []string{"INT8"}, [][]any{{edge.ref}}, edge.class, edge.object, edge.refclass, edge.ref))
						}
						c.run(fixture.Select("SELECT refobjsubid FROM pg_depend WHERE classid=2618 AND objid=$1 AND refclassid=1259 AND refobjid=$2 AND refobjsubid>0 AND deptype='n' ORDER BY refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}, rule, ids[1]))
					}
					check()
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						actualDump = dump.String()
						if !strings.Contains(actualDump, "USING INDEX") || !strings.Contains(actualDump, "enum_last") || !strings.Contains(actualDump, "ux_graph_view") || !strings.Contains(actualDump, "ux_seq") {
							t.Fatal("provider graph missing from actual dump", actualDump)
						}
						before := c.ids("ux_owned")
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: insertStmt, SQL: insertQ}, recorder, fixture.Exec(insertQ, 1, "discard"))
						c.run(fixture.Select("SELECT a,payload FROM ux_t WHERE a=9", []string{"a", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "discard"}}))
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(actualDump)); err != nil {
							t.Fatal("provider graph same-name Restore", err)
						}
						after := c.ids("ux_owned")
						if after[1] != before[1] {
							t.Fatal("provider graph same-name identities", before, after)
						}
						check()
						var redump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != actualDump {
							t.Fatal("provider graph noncanonical redump", err, redump.String())
						}
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: insertStmt, SQL: insertQ}, recorder, fixture.Exec(insertQ, 1, "next"))
					c.run(fixture.Select("SELECT a,b::text AS mood,payload,ref::text AS backing FROM ux_t WHERE a=9", []string{"a", "mood", "payload", "backing"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(9), "warm", "next", "ux_owned"}}))
					c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t(a,b,payload) VALUES(7,'warm','different')", fixture.UniqueReferenceDuplicate("ux_t", "ux_owned", "Key (a, b)=(7, warm) already exists.")))
				})
			}
		})
	}
}

func TestReferenceUsingIndexFeatureSmoke(t *testing.T) {
	usingIndexScenario(t, func(c *usingIndexCase) {
		c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
		original := c.oid("ux_idx")
		q := "INSERT INTO ux_t VALUES($1,$2,$3)"
		insert, err := c.conn.PrepareContext(c.p.Ctx, q)
		if err != nil {
			c.t.Fatal(err)
		}
		defer insert.Close()
		held := fixture.HeldStatement{Executor: c.conn, Statement: insert, SQL: q}
		c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_owned PRIMARY KEY USING INDEX ux_idx", 0)
		if c.ids("ux_owned")[2] != original {
			c.t.Fatal("smoke lost index identity")
		}
		c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_owned'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (a, b) INCLUDE (payload)"}}))
		c.rows()
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("ux_t", "ux_owned", "Key (a, b)=(1, x) already exists."), int64(1), "x", "other"))
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.ErrorDiagnostic(q, fixture.PrimaryKeyReferenceNull("ux_t", "a", "null, z, null"), nil, "z", nil))
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.Exec(q, 1, int64(3), "z", nil))
		c.exec("CREATE TABLE ux_other(k INT)", 0)
		c.exec("CREATE UNIQUE INDEX ux_standalone ON ux_other(k)", 0)
		originalStandalone := c.oid("ux_standalone")
		c.failure("ALTER TABLE ux_other ADD CONSTRAINT ux_candidate UNIQUE USING INDEX ux_standalone, ALTER missing SET NOT NULL", usingIndexDiagnostic("42703", `column "missing" of relation "ux_other" does not exist`), false)
		if c.oid("ux_standalone") != originalStandalone {
			c.t.Fatal("failed multi-action changed standalone index")
		}
		c.exec("ALTER TABLE ux_other ADD UNIQUE USING INDEX ux_standalone", 0)
		c.exec("ALTER TABLE ux_other DROP CONSTRAINT ux_standalone", 0)
		c.run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ux_standalone' OR relname='ux_candidate'", []string{"relname"}, []string{"NAME"}, nil))
		if !c.b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
				c.t.Fatal(err)
			}
			if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
				c.t.Fatal(err)
			}
			if err := stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(dump.String())); err != nil {
				c.t.Fatal(err)
			}
			c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_owned'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (a, b) INCLUDE (payload)"}}))
			fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("ux_t", "ux_owned", "Key (a, b)=(1, x) already exists."), int64(1), "x", nil))
		}
	})
}
