package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndexClausesMaintenance(t *testing.T) {
	indexClausesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		indexClausesSeed(run)
		run(fixture.Exec("CREATE INDEX cover ON t(a,b) INCLUDE(p)", 0))
		run(fixture.Prepared(fixture.Exec("UPDATE t SET p='xx' WHERE p='x'", 1)))
		run(fixture.Exec("INSERT INTO t VALUES(1,2,'xx'),(1,2,NULL)", 2))
		run(fixture.Select("SELECT p FROM t WHERE a=1 AND b=2 ORDER BY p NULLS LAST", []string{"p"}, []string{"TEXT"}, [][]any{{"xx"}, {"xx"}, {nil}}))
		run(fixture.Exec("DELETE FROM t WHERE p='xx'", 2))
		run(fixture.Select("SELECT p FROM t WHERE a=1 AND b=2", []string{"p"}, []string{"TEXT"}, [][]any{{nil}}))
		bad := "INSERT INTO t VALUES(9,9,'ok'),('bad',9,'bad')"
		run(fixture.ImplicitError(bad, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: fixture.Position(strings.Index(bad, "'bad'") + 1)}))
		run(fixture.Select("SELECT p FROM t WHERE a=9", []string{"p"}, []string{"TEXT"}, nil))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		tx, err := conn.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		trun(fixture.Exec("UPDATE t SET p='private' WHERE a=2", 1))
		trun(fixture.Exec("SAVEPOINT s", 0))
		trun(fixture.Exec("DELETE FROM t WHERE a=1", 3))
		trun(fixture.Exec("ROLLBACK TO s", 0))
		trun(fixture.Select("SELECT p FROM t WHERE a=2", []string{"p"}, []string{"TEXT"}, [][]any{{"private"}}))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT p FROM t WHERE a=2", []string{"p"}, []string{"TEXT"}, [][]any{{"w"}}))
	})
}

func TestReferenceIndexClausesLifecycle(t *testing.T) {
	indexClausesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE t(extra INT,n INT,p TEXT,s TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(0,1,'alpha','a')", 1))
		run(fixture.Exec(`CREATE INDEX cover ON t(n int4_ops,s COLLATE "POSIX" text_pattern_ops) INCLUDE(p)`, 0))
		run(fixture.Exec("ALTER TABLE t RENAME p TO payload", 0))
		run(fixture.Exec("ALTER TABLE t DROP extra", 0))
		run(fixture.Exec("ALTER TABLE t ALTER payload TYPE VARCHAR USING payload::varchar", 0))
		run(fixture.Select("SELECT payload FROM t", []string{"payload"}, []string{"VARCHAR"}, [][]any{{"alpha"}}))
		run(fixture.Exec("ALTER TABLE t ALTER s TYPE VARCHAR USING s::varchar", 0))
		run(fixture.Exec("CREATE DOMAIN payload_array AS name[]", 0))
		run(fixture.Exec("ALTER TABLE t ALTER payload TYPE payload_array USING '{alpha,NULL}'::payload_array", 0))
		run(fixture.Select("SELECT payload FROM t", []string{"payload"}, []string{"_NAME"}, [][]any{{"{alpha,NULL}"}}))
		run(fixture.Exec("ALTER TABLE t ALTER n TYPE TEXT USING n::text", 0))
		run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"TEXT"}, [][]any{{"1"}}))
		if b.PG {
			run(fixture.Select("SELECT o.opcname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_opclass o ON i.indclass[0]=o.oid WHERE c.relname='cover'", []string{"opcname"}, []string{"NAME"}, [][]any{{"text_ops"}}))
		}
		run(fixture.Exec("CREATE TABLE retained(n INT,p TEXT)", 0))
		run(fixture.Exec("INSERT INTO retained VALUES(1,'payload')", 1))
		run(fixture.Exec("CREATE INDEX retained_class ON retained(n oid_ops) INCLUDE(p)", 0))
		var beforeType bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &beforeType); err != nil {
				t.Fatal(err)
			}
		}
		fail := fixture.ImplicitError("ALTER TABLE retained ALTER n TYPE TEXT USING n::text", fixture.Diagnostic{Code: "42804", Message: `operator class "oid_ops" does not accept data type text`})
		run(fail)
		run(fixture.Select("SELECT n,p FROM retained", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "payload"}}))
		run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='retained_class'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(2)}}))
		if !b.PG {
			var afterType bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &afterType); err != nil || beforeType.String() != afterType.String() {
				t.Fatal("incompatible retained class mutated destination", err)
			}
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO retained VALUES(-1,'after')", 1)))
		run(fixture.Select("SELECT n,p FROM retained ORDER BY n", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "after"}, {int64(1), "payload"}}))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, fragment := range []string{`INCLUDE ("payload")`, `"pg_catalog"."POSIX"`, `"pg_catalog"."text_pattern_ops"`} {
				if !strings.Contains(dump.String(), fragment) {
					t.Fatalf("missing retained option %s: %s", fragment, dump.String())
				}
			}
		}
		run(fixture.Exec("ALTER TABLE t DROP payload", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='cover'", []string{"relname"}, []string{"NAME"}, nil))
		q := "CREATE INDEX held ON t(n text_ops) INCLUDE(s)"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("ALTER TABLE t RENAME s TO renamed", 0))
		result, err := stmt.ExecContext(p.Ctx)
		if result != nil {
			t.Fatal("held failure published result")
		}
		want := fixture.ControlsReferenceDiagnostic("42703", `column "s" does not exist`)
		fixture.AssertDiagnostic(t, err, &want)
		run(fixture.Exec("ALTER TABLE t RENAME renamed TO s", 0))
		result, err = stmt.ExecContext(p.Ctx)
		fixture.DeleteReferenceCount(t, result, err, 0)
		run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='held'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(2)}}))
	})
	for _, tc := range []struct{ name, clause string }{
		{"same-type-key", "n TYPE INT"},
		{"same-type-key-using", "n TYPE INT USING n"},
		{"same-type-include", "p TYPE TEXT"},
		{"same-type-include-using", "p TYPE TEXT USING p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			indexClausesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(n INT,s TEXT,r TEXT,p TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(-1,'a ','keep','payload'),(1,'a','real','next')", 2))
				run(fixture.Exec(`CREATE INDEX cover ON t(n int4_ops,s COLLATE "default" text_ops,r COLLATE "POSIX" text_pattern_ops) INCLUDE(p)`, 0))
				inquiry := func() {
					for _, tc := range []struct{ call, want string }{
						{"pg_get_indexdef('cover'::regclass)", `CREATE INDEX cover ON public.t USING btree (n, s, r COLLATE "POSIX" text_pattern_ops) INCLUDE (p)`},
						{"pg_get_indexdef('cover'::regclass,4,false)", "p"},
						{"pg_get_indexdef('cover'::regclass,5,false)", ""},
					} {
						s := fixture.DeparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
						run(s)
						run(fixture.Prepared(s))
					}
				}
				inquiry()
				var oldOID int64
				if b.PG {
					if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='cover'").Scan(&oldOID); err != nil || oldOID <= 0 {
						t.Fatal("capture logical index identity", oldOID, err)
					}
				} else {
					var before bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
						t.Fatal(err)
					}
					want := `CREATE INDEX "cover" ON "public"."t" USING btree ("n" "pg_catalog"."int4_ops" ASC NULLS LAST, "s" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST, "r" COLLATE "pg_catalog"."POSIX" "pg_catalog"."text_pattern_ops" ASC NULLS LAST) INCLUDE ("p");`
					if !strings.Contains(before.String(), want) {
						t.Fatalf("missing original explicit definition %s: %s", want, before.String())
					}
				}
				run(fixture.Exec("ALTER TABLE t ALTER "+tc.clause, 0))
				rows := [][]any{{int64(-1), "a ", "keep", "payload"}, {int64(1), "a", "real", "next"}}
				s := fixture.Select("SELECT n,s,r,p FROM t ORDER BY n", []string{"n", "s", "r", "p"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, rows)
				run(s)
				run(fixture.Prepared(s))
				run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='cover'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(4)}}))
				if b.PG {
					// Logical identity changes even when physical index storage is reused.
					run(fixture.Select("SELECT oid<>$1 AS rebuilt FROM pg_class WHERE relname='cover'", []string{"rebuilt"}, []string{"BOOL"}, [][]any{{true}}, oldOID))
					for i, class := range []string{"int4_ops", "text_ops", "text_pattern_ops"} {
						slot := []string{"0", "1", "2"}[i]
						run(fixture.Select("SELECT o.opcname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_opclass o ON i.indclass["+slot+"]=o.oid WHERE c.relname='cover'", []string{"opcname"}, []string{"NAME"}, [][]any{{class}}))
					}
					for _, collation := range []struct{ slot, name string }{{"1", "default"}, {"2", "POSIX"}} {
						run(fixture.Select("SELECT o.collname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_collation o ON i.indcollation["+collation.slot+"]=o.oid WHERE c.relname='cover'", []string{"collname"}, []string{"NAME"}, [][]any{{collation.name}}))
					}
				} else {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					want := `CREATE INDEX "cover" ON "public"."t" USING btree ("n" ASC NULLS LAST, "s" ASC NULLS LAST, "r" COLLATE "pg_catalog"."POSIX" "pg_catalog"."text_pattern_ops" ASC NULLS LAST) INCLUDE ("p");`
					if !strings.Contains(after.String(), want) {
						t.Fatalf("missing reconstructed definition %s: %s", want, after.String())
					}
				}
				inquiry()
				run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(2,'after','retained','new')", 1)))
				run(fixture.Select("SELECT n,p FROM t ORDER BY n", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "payload"}, {int64(1), "next"}, {int64(2), "new"}}))
			})
		})
	}
	t.Run("derived-composition", func(t *testing.T) {
		for _, tc := range []struct{ name, alter, payload string }{
			{"dependency-type", "ALTER TABLE t ALTER s TYPE VARCHAR USING s::varchar", ""},
			{"include-type", "ALTER TABLE t ALTER payload TYPE TEXT USING payload||'!'", "!"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				indexClausesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE t(extra INT,n INT,s TEXT,p TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(0,-1,'a ','space'),(0,1,'a','plain')", 2))
					run(fixture.Exec("CREATE INDEX unsigned ON t((n+0) oid_ops) INCLUDE(p)", 0))
					run(fixture.Exec(`CREATE INDEX cover ON t(lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST,lower(s) COLLATE "default" text_ops) INCLUDE(p)`, 0))
					check := func() {
						s := fixture.Select("SELECT n+0 AS signed,lower(s) AS lowered,payload FROM t ORDER BY n+0", []string{"signed", "lowered", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(-1), "a ", "space" + tc.payload}, {int64(1), "a", "plain" + tc.payload}})
						run(s)
						run(fixture.Prepared(s))
						run(fixture.Select("SELECT n FROM t WHERE lower(s)='a' ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(fixture.Select("SELECT n FROM t WHERE lower(s)::bpchar='a'::bpchar ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}}))
						run(fixture.Select("SELECT n FROM t ORDER BY lower(s),n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(-1)}}))
					}
					run(fixture.Exec("ALTER TABLE t RENAME p TO payload", 0))
					run(fixture.Exec("ALTER TABLE t DROP extra", 0))
					var before bytes.Buffer
					if !b.PG {
						if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
						want := `CREATE INDEX "cover" ON "public"."t" USING btree ((pg_catalog.lower("s")) COLLATE "pg_catalog"."POSIX" "pg_catalog"."bpchar_ops" DESC NULLS LAST, (pg_catalog.lower("s")) COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST) INCLUDE ("payload");`
						if !strings.Contains(before.String(), want) {
							t.Fatalf("DROP/rename lost derived definition: %s", before.String())
						}
					}
					run(fixture.Exec(tc.alter, 0))
					check()
					run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='cover'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(3)}}))
					if b.PG {
						for _, provider := range []struct{ index, slot, class string }{{"cover", "0", "bpchar_ops"}, {"cover", "1", "text_ops"}, {"unsigned", "0", "oid_ops"}} {
							run(fixture.Select("SELECT o.opcname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_opclass o ON i.indclass["+provider.slot+"]=o.oid WHERE c.relname='"+provider.index+"'", []string{"opcname"}, []string{"NAME"}, [][]any{{provider.class}}))
						}
						run(fixture.Select("SELECT o.collname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_collation o ON i.indcollation[0]=o.oid WHERE c.relname='cover'", []string{"collname"}, []string{"NAME"}, [][]any{{"POSIX"}}))
					} else {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
							t.Fatal(err)
						}
						want := `CREATE INDEX "cover" ON "public"."t" USING btree ((pg_catalog.lower("s")) COLLATE "pg_catalog"."POSIX" "pg_catalog"."bpchar_ops" DESC NULLS LAST, (pg_catalog.lower("s")) ASC NULLS LAST) INCLUDE ("payload");`
						if !strings.Contains(after.String(), want) {
							t.Fatalf("TYPE lost rebound trees/providers or retained old defaults: %s", after.String())
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(after.String())); err != nil {
							t.Fatal(err)
						}
						check()
					}
					run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(2,'A','after')", 1)))
					run(fixture.Select("SELECT n,payload FROM t WHERE lower(s)='a' ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "plain" + tc.payload}, {int64(2), "after"}}))
				})
			})
		}
	})
}

func indexClausesUnique(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE u(n INT UNIQUE,p TEXT)", 0))
	run(fixture.Exec("CREATE INDEX u_cover ON u(n) INCLUDE(p)", 0))
	run(fixture.Exec("INSERT INTO u VALUES(1,'first')", 1))
	for _, q := range []string{"INSERT INTO u VALUES(1,'different')", "INSERT INTO u VALUES(1,NULL)"} {
		s := fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("u", "u_n_key", "Key (n)=(1) already exists."))
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.Exec("UPDATE u SET p=NULL WHERE n=1", 1))
	run(fixture.Select("SELECT n,p FROM u", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
	run(fixture.Exec("CREATE TABLE pg_unique(n INT,p TEXT)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pg_cover ON pg_unique(n) INCLUDE(p)", 0))
	run(fixture.Exec("INSERT INTO pg_unique VALUES(1,'first')", 1))
	run(fixture.ErrorDiagnostic("INSERT INTO pg_unique VALUES(1,NULL)", fixture.UniqueReferenceDuplicate("pg_unique", "pg_cover", "Key (n)=(1) already exists.")))
}

func TestReferenceIndexClausesUniqueInvariant(t *testing.T) {
	indexClausesScenario(t, indexClausesUnique)
}

func indexClausesReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step), stream *string) {
	t.Helper()
	if b.PG {
		if *stream == "" {
			t.Fatal("missing actual native Dump stream")
		}
		run(fixture.Exec("DROP SCHEMA public CASCADE", 0))
		run(fixture.Exec("CREATE SCHEMA public", 0))
		if _, err := b.DB.ExecContext(p.Ctx, *stream); err != nil {
			t.Fatal("PostgreSQL replay of actual native Dump", err)
		}
	} else {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{`INCLUDE ("p")`, `"pg_catalog"."C"`, `"pg_catalog"."text_pattern_ops"`, `"pg_catalog"."int4_ops"`} {
			if !strings.Contains(dump.String(), fragment) {
				t.Fatalf("missing %s in dump: %s", fragment, dump.String())
			}
		}
		*stream = dump.String()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='cover'", []string{"relname"}, []string{"NAME"}, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		foreign := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal("foreign Restore", err)
		}
		fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Select("SELECT p FROM t WHERE a=1 AND b=2", []string{"p"}, []string{"TEXT"}, [][]any{{"xx"}}))
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE INDEX broken ON t(a) INCLUDE((missing+1));"))
		want := fixture.ControlsReferenceDiagnostic("0A000", "expressions are not supported in included columns")
		fixture.AssertDiagnostic(t, err, &want)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
			t.Fatal("late invalid restore changed destination", err)
		}
	}
	for _, tc := range []struct{ call, want string }{
		{"pg_get_indexdef('cover'::regclass)", "CREATE INDEX cover ON public.t USING btree (a, b DESC) INCLUDE (p)"},
		{"pg_get_indexdef('cover'::regclass,3,false)", "p"},
		{"pg_get_indexdef('cover'::regclass,4,false)", ""},
	} {
		s := fixture.DeparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='cover'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(3)}}))
	run(fixture.Select("SELECT p FROM t WHERE a=1 AND b=2", []string{"p"}, []string{"TEXT"}, [][]any{{"xx"}}))
	run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(3,4,'after')", 1)))
	run(fixture.Select("SELECT p FROM t WHERE a=3", []string{"p"}, []string{"TEXT"}, [][]any{{"after"}}))
}

func TestReferenceIndexClausesControls(t *testing.T) {
	var stream string
	indexClausesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		indexClausesSeed(run)
		run(fixture.Exec("CREATE INDEX cover ON t(a int4_ops ASC NULLS LAST,b DESC NULLS FIRST) INCLUDE(p)", 0))
		run(fixture.Exec(`CREATE INDEX text_cover ON t(p COLLATE pg_catalog."C" pg_catalog.text_pattern_ops) INCLUDE(a)`, 0))
		run(fixture.Exec("UPDATE t SET p='xx' WHERE p='x'", 1))
		indexClausesReplay(t, p, b, run, &stream)
	})
}

func TestReferenceIndexClausesFeatureSmoke(t *testing.T) {
	var stream string
	indexClausesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		indexClausesSeed(run)
		run(fixture.Exec("CREATE INDEX cover ON t(a int4_ops ASC NULLS LAST,b DESC NULLS FIRST) INCLUDE(p)", 0))
		run(fixture.Exec(`CREATE INDEX text_cover ON t(upper(p) COLLATE pg_catalog."C" pg_catalog.text_pattern_ops) INCLUDE(a)`, 0))
		indexClausesOrders(run)
		run(fixture.Prepared(fixture.Select("SELECT upper(p) AS folded FROM t WHERE a=1 ORDER BY upper(p)", []string{"folded"}, []string{"TEXT"}, [][]any{{"X"}, {"Y"}, {"Z"}})))
		run(fixture.Select("SELECT a,b FROM t WHERE p='x'", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		s := fixture.Select("SELECT p FROM t WHERE a=1", []string{"p"}, []string{"TEXT"}, [][]any{{"x"}, {"y"}, {"z"}})
		s.Bag = true
		run(fixture.Prepared(s))
		run(fixture.Exec("UPDATE t SET p='xx' WHERE p='x'", 1))
		list := strings.TrimSuffix(strings.Repeat("p,", 31), ",")
		run(fixture.Exec("CREATE INDEX total32 ON t(a) INCLUDE("+list+")", 0))
		run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='total32'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(32)}}))
		run(fixture.ImplicitError("CREATE INDEX total33 ON t(a) INCLUDE("+list+",p)", fixture.Diagnostic{Code: "54011", Message: "cannot use more than 32 columns in an index"}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='total33'", []string{"relname"}, []string{"NAME"}, nil))
		indexClausesUnique(t, p, b, run)
		indexClausesReplay(t, p, b, run, &stream)
		if !b.PG {
			want := `CREATE INDEX "text_cover" ON "public"."t" USING btree ((pg_catalog.upper("p")) COLLATE "pg_catalog"."C" "pg_catalog"."text_pattern_ops" ASC NULLS LAST) INCLUDE ("a");`
			if !strings.Contains(stream, want) {
				t.Fatalf("missing actual derived-provider replay definition %s: %s", want, stream)
			}
		}
		compact := func() {
			for _, tc := range []struct{ call, want string }{
				{"pg_get_indexdef('cover'::regclass)", "CREATE INDEX cover ON public.t USING btree (a, b DESC) INCLUDE (p)"},
				{"pg_get_indexdef('text_cover'::regclass)", `CREATE INDEX text_cover ON public.t USING btree (upper(p) COLLATE "C" text_pattern_ops) INCLUDE (a)`},
				{"pg_get_indexdef('cover'::regclass,3,false)", "p"},
				{"pg_get_indexdef('cover'::regclass,4,false)", ""},
			} {
				s := fixture.DeparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
				run(s)
				run(fixture.Prepared(s))
			}
		}
		compact()
		run(fixture.Exec("ALTER TABLE t ALTER p TYPE TEXT USING p", 0))
		compact()
		run(fixture.Prepared(fixture.Select("SELECT p FROM t WHERE a=3", []string{"p"}, []string{"TEXT"}, [][]any{{"after"}})))
	})
}
