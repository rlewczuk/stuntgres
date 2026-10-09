package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceIndexClausesMaintenance(t *testing.T) {
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		indexClausesSeed(run)
		run(referenceExec("CREATE INDEX cover ON t(a,b) INCLUDE(p)", 0))
		run(referencePrepared(referenceExec("UPDATE t SET p='xx' WHERE p='x'", 1)))
		run(referenceExec("INSERT INTO t VALUES(1,2,'xx'),(1,2,NULL)", 2))
		run(referenceSelect("SELECT p FROM t WHERE a=1 AND b=2 ORDER BY p NULLS LAST", []string{"p"}, []string{"TEXT"}, [][]any{{"xx"}, {"xx"}, {nil}}))
		run(referenceExec("DELETE FROM t WHERE p='xx'", 2))
		run(referenceSelect("SELECT p FROM t WHERE a=1 AND b=2", []string{"p"}, []string{"TEXT"}, [][]any{{nil}}))
		bad := "INSERT INTO t VALUES(9,9,'ok'),('bad',9,'bad')"
		run(referenceImplicitError(bad, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: referencePosition(strings.Index(bad, "'bad'") + 1)}))
		run(referenceSelect("SELECT p FROM t WHERE a=9", []string{"p"}, []string{"TEXT"}, nil))
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		tx, err := conn.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		trun := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
		trun(referenceExec("UPDATE t SET p='private' WHERE a=2", 1))
		trun(referenceExec("SAVEPOINT s", 0))
		trun(referenceExec("DELETE FROM t WHERE a=1", 3))
		trun(referenceExec("ROLLBACK TO s", 0))
		trun(referenceSelect("SELECT p FROM t WHERE a=2", []string{"p"}, []string{"TEXT"}, [][]any{{"private"}}))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, conn, b.recorder, referenceSelect("SELECT p FROM t WHERE a=2", []string{"p"}, []string{"TEXT"}, [][]any{{"w"}}))
	})
}

func TestReferenceIndexClausesLifecycle(t *testing.T) {
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE t(extra INT,n INT,p TEXT,s TEXT)", 0))
		run(referenceExec("INSERT INTO t VALUES(0,1,'alpha','a')", 1))
		run(referenceExec(`CREATE INDEX cover ON t(n int4_ops,s COLLATE "POSIX" text_pattern_ops) INCLUDE(p)`, 0))
		run(referenceExec("ALTER TABLE t RENAME p TO payload", 0))
		run(referenceExec("ALTER TABLE t DROP extra", 0))
		run(referenceExec("ALTER TABLE t ALTER payload TYPE VARCHAR USING payload::varchar", 0))
		run(referenceSelect("SELECT payload FROM t", []string{"payload"}, []string{"VARCHAR"}, [][]any{{"alpha"}}))
		run(referenceExec("ALTER TABLE t ALTER s TYPE VARCHAR USING s::varchar", 0))
		run(referenceExec("CREATE DOMAIN payload_array AS name[]", 0))
		run(referenceExec("ALTER TABLE t ALTER payload TYPE payload_array USING '{alpha,NULL}'::payload_array", 0))
		run(referenceSelect("SELECT payload FROM t", []string{"payload"}, []string{"_NAME"}, [][]any{{"{alpha,NULL}"}}))
		run(referenceExec("ALTER TABLE t ALTER n TYPE TEXT USING n::text", 0))
		run(referenceSelect("SELECT n FROM t", []string{"n"}, []string{"TEXT"}, [][]any{{"1"}}))
		if b.pg {
			run(referenceSelect("SELECT o.opcname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_opclass o ON i.indclass[0]=o.oid WHERE c.relname='cover'", []string{"opcname"}, []string{"NAME"}, [][]any{{"text_ops"}}))
		}
		run(referenceExec("CREATE TABLE retained(n INT,p TEXT)", 0))
		run(referenceExec("INSERT INTO retained VALUES(1,'payload')", 1))
		run(referenceExec("CREATE INDEX retained_class ON retained(n oid_ops) INCLUDE(p)", 0))
		var beforeType bytes.Buffer
		if !b.pg {
			if err := stuntgres.Dump(p.ctx, p.native, &beforeType); err != nil {
				t.Fatal(err)
			}
		}
		fail := referenceImplicitError("ALTER TABLE retained ALTER n TYPE TEXT USING n::text", referenceDiagnostic{Code: "42804", Message: `operator class "oid_ops" does not accept data type text`})
		run(fail)
		run(referenceSelect("SELECT n,p FROM retained", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "payload"}}))
		run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='retained_class'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(2)}}))
		if !b.pg {
			var afterType bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &afterType); err != nil || beforeType.String() != afterType.String() {
				t.Fatal("incompatible retained class mutated destination", err)
			}
		}
		run(referencePrepared(referenceExec("INSERT INTO retained VALUES(-1,'after')", 1)))
		run(referenceSelect("SELECT n,p FROM retained ORDER BY n", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "after"}, {int64(1), "payload"}}))
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, fragment := range []string{`INCLUDE ("payload")`, `"pg_catalog"."POSIX"`, `"pg_catalog"."text_pattern_ops"`} {
				if !strings.Contains(dump.String(), fragment) {
					t.Fatalf("missing retained option %s: %s", fragment, dump.String())
				}
			}
		}
		run(referenceExec("ALTER TABLE t DROP payload", 0))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='cover'", []string{"relname"}, []string{"NAME"}, nil))
		q := "CREATE INDEX held ON t(n text_ops) INCLUDE(s)"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(referenceExec("ALTER TABLE t RENAME s TO renamed", 0))
		result, err := stmt.ExecContext(p.ctx)
		if result != nil {
			t.Fatal("held failure published result")
		}
		want := controlsReferenceDiagnostic("42703", `column "s" does not exist`)
		referenceAssertDiagnostic(t, err, &want)
		run(referenceExec("ALTER TABLE t RENAME renamed TO s", 0))
		result, err = stmt.ExecContext(p.ctx)
		deleteReferenceCount(t, result, err, 0)
		run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='held'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(2)}}))
	})
	for _, tc := range []struct{ name, clause string }{
		{"same-type-key", "n TYPE INT"},
		{"same-type-key-using", "n TYPE INT USING n"},
		{"same-type-include", "p TYPE TEXT"},
		{"same-type-include-using", "p TYPE TEXT USING p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE t(n INT,s TEXT,r TEXT,p TEXT)", 0))
				run(referenceExec("INSERT INTO t VALUES(-1,'a ','keep','payload'),(1,'a','real','next')", 2))
				run(referenceExec(`CREATE INDEX cover ON t(n int4_ops,s COLLATE "default" text_ops,r COLLATE "POSIX" text_pattern_ops) INCLUDE(p)`, 0))
				inquiry := func() {
					for _, tc := range []struct{ call, want string }{
						{"pg_get_indexdef('cover'::regclass)", `CREATE INDEX cover ON public.t USING btree (n, s, r COLLATE "POSIX" text_pattern_ops) INCLUDE (p)`},
						{"pg_get_indexdef('cover'::regclass,4,false)", "p"},
						{"pg_get_indexdef('cover'::regclass,5,false)", ""},
					} {
						s := deparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
						run(s)
						run(referencePrepared(s))
					}
				}
				inquiry()
				var oldOID int64
				if b.pg {
					if err := b.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='cover'").Scan(&oldOID); err != nil || oldOID <= 0 {
						t.Fatal("capture logical index identity", oldOID, err)
					}
				} else {
					var before bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
						t.Fatal(err)
					}
					want := `CREATE INDEX "cover" ON "public"."t" USING btree ("n" "pg_catalog"."int4_ops" ASC NULLS LAST, "s" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST, "r" COLLATE "pg_catalog"."POSIX" "pg_catalog"."text_pattern_ops" ASC NULLS LAST) INCLUDE ("p");`
					if !strings.Contains(before.String(), want) {
						t.Fatalf("missing original explicit definition %s: %s", want, before.String())
					}
				}
				run(referenceExec("ALTER TABLE t ALTER "+tc.clause, 0))
				rows := [][]any{{int64(-1), "a ", "keep", "payload"}, {int64(1), "a", "real", "next"}}
				s := referenceSelect("SELECT n,s,r,p FROM t ORDER BY n", []string{"n", "s", "r", "p"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, rows)
				run(s)
				run(referencePrepared(s))
				run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='cover'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(4)}}))
				if b.pg {
					// Logical identity changes even when physical index storage is reused.
					run(referenceSelect("SELECT oid<>$1 AS rebuilt FROM pg_class WHERE relname='cover'", []string{"rebuilt"}, []string{"BOOL"}, [][]any{{true}}, oldOID))
					for i, class := range []string{"int4_ops", "text_ops", "text_pattern_ops"} {
						slot := []string{"0", "1", "2"}[i]
						run(referenceSelect("SELECT o.opcname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_opclass o ON i.indclass["+slot+"]=o.oid WHERE c.relname='cover'", []string{"opcname"}, []string{"NAME"}, [][]any{{class}}))
					}
					for _, collation := range []struct{ slot, name string }{{"1", "default"}, {"2", "POSIX"}} {
						run(referenceSelect("SELECT o.collname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_collation o ON i.indcollation["+collation.slot+"]=o.oid WHERE c.relname='cover'", []string{"collname"}, []string{"NAME"}, [][]any{{collation.name}}))
					}
				} else {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
						t.Fatal(err)
					}
					want := `CREATE INDEX "cover" ON "public"."t" USING btree ("n" ASC NULLS LAST, "s" ASC NULLS LAST, "r" COLLATE "pg_catalog"."POSIX" "pg_catalog"."text_pattern_ops" ASC NULLS LAST) INCLUDE ("p");`
					if !strings.Contains(after.String(), want) {
						t.Fatalf("missing reconstructed definition %s: %s", want, after.String())
					}
				}
				inquiry()
				run(referencePrepared(referenceExec("INSERT INTO t VALUES(2,'after','retained','new')", 1)))
				run(referenceSelect("SELECT n,p FROM t ORDER BY n", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "payload"}, {int64(1), "next"}, {int64(2), "new"}}))
			})
		})
	}
	t.Run("derived-composition", func(t *testing.T) {
		for _, tc := range []struct{ name, alter, payload string }{
			{"dependency-type", "ALTER TABLE t ALTER s TYPE VARCHAR USING s::varchar", ""},
			{"include-type", "ALTER TABLE t ALTER payload TYPE TEXT USING payload||'!'", "!"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
					run(referenceExec("CREATE TABLE t(extra INT,n INT,s TEXT,p TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(0,-1,'a ','space'),(0,1,'a','plain')", 2))
					run(referenceExec("CREATE INDEX unsigned ON t((n+0) oid_ops) INCLUDE(p)", 0))
					run(referenceExec(`CREATE INDEX cover ON t(lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST,lower(s) COLLATE "default" text_ops) INCLUDE(p)`, 0))
					check := func() {
						s := referenceSelect("SELECT n+0 AS signed,lower(s) AS lowered,payload FROM t ORDER BY n+0", []string{"signed", "lowered", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(-1), "a ", "space" + tc.payload}, {int64(1), "a", "plain" + tc.payload}})
						run(s)
						run(referencePrepared(s))
						run(referenceSelect("SELECT n FROM t WHERE lower(s)='a' ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(referenceSelect("SELECT n FROM t WHERE lower(s)::bpchar='a'::bpchar ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}}))
						run(referenceSelect("SELECT n FROM t ORDER BY lower(s),n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(-1)}}))
					}
					run(referenceExec("ALTER TABLE t RENAME p TO payload", 0))
					run(referenceExec("ALTER TABLE t DROP extra", 0))
					var before bytes.Buffer
					if !b.pg {
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
						want := `CREATE INDEX "cover" ON "public"."t" USING btree ((pg_catalog.lower("s")) COLLATE "pg_catalog"."POSIX" "pg_catalog"."bpchar_ops" DESC NULLS LAST, (pg_catalog.lower("s")) COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST) INCLUDE ("payload");`
						if !strings.Contains(before.String(), want) {
							t.Fatalf("DROP/rename lost derived definition: %s", before.String())
						}
					}
					run(referenceExec(tc.alter, 0))
					check()
					run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='cover'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(3)}}))
					if b.pg {
						for _, provider := range []struct{ index, slot, class string }{{"cover", "0", "bpchar_ops"}, {"cover", "1", "text_ops"}, {"unsigned", "0", "oid_ops"}} {
							run(referenceSelect("SELECT o.opcname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_opclass o ON i.indclass["+provider.slot+"]=o.oid WHERE c.relname='"+provider.index+"'", []string{"opcname"}, []string{"NAME"}, [][]any{{provider.class}}))
						}
						run(referenceSelect("SELECT o.collname FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid JOIN pg_collation o ON i.indcollation[0]=o.oid WHERE c.relname='cover'", []string{"collname"}, []string{"NAME"}, [][]any{{"POSIX"}}))
					} else {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
							t.Fatal(err)
						}
						want := `CREATE INDEX "cover" ON "public"."t" USING btree ((pg_catalog.lower("s")) COLLATE "pg_catalog"."POSIX" "pg_catalog"."bpchar_ops" DESC NULLS LAST, (pg_catalog.lower("s")) ASC NULLS LAST) INCLUDE ("payload");`
						if !strings.Contains(after.String(), want) {
							t.Fatalf("TYPE lost rebound trees/providers or retained old defaults: %s", after.String())
						}
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(after.String())); err != nil {
							t.Fatal(err)
						}
						check()
					}
					run(referencePrepared(referenceExec("INSERT INTO t VALUES(2,'A','after')", 1)))
					run(referenceSelect("SELECT n,payload FROM t WHERE lower(s)='a' ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "plain" + tc.payload}, {int64(2), "after"}}))
				})
			})
		}
	})
}

func indexClausesUnique(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
	run(referenceExec("CREATE TABLE u(n INT UNIQUE,p TEXT)", 0))
	run(referenceExec("CREATE INDEX u_cover ON u(n) INCLUDE(p)", 0))
	run(referenceExec("INSERT INTO u VALUES(1,'first')", 1))
	for _, q := range []string{"INSERT INTO u VALUES(1,'different')", "INSERT INTO u VALUES(1,NULL)"} {
		s := referenceErrorDiagnostic(q, uniqueReferenceDuplicate("u", "u_n_key", "Key (n)=(1) already exists."))
		s.errorStage = "execute"
		run(s)
		run(referencePrepared(s))
	}
	run(referenceExec("UPDATE u SET p=NULL WHERE n=1", 1))
	run(referenceSelect("SELECT n,p FROM u", []string{"n", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
	if b.pg {
		run(referenceExec("CREATE TABLE pg_unique(n INT,p TEXT)", 0))
		run(referenceExec("CREATE UNIQUE INDEX pg_cover ON pg_unique(n) INCLUDE(p)", 0))
		run(referenceExec("INSERT INTO pg_unique VALUES(1,'first')", 1))
		run(referenceErrorDiagnostic("INSERT INTO pg_unique VALUES(1,NULL)", uniqueReferenceDuplicate("pg_unique", "pg_cover", "Key (n)=(1) already exists.")))
	}
}

func TestReferenceIndexClausesUniqueInvariant(t *testing.T) {
	indexClausesScenario(t, indexClausesUnique)
}

func indexClausesReplay(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep), stream *string) {
	t.Helper()
	if b.pg {
		if *stream == "" {
			t.Fatal("missing actual native Dump stream")
		}
		run(referenceExec("DROP SCHEMA public CASCADE", 0))
		run(referenceExec("CREATE SCHEMA public", 0))
		if _, err := b.db.ExecContext(p.ctx, *stream); err != nil {
			t.Fatal("PostgreSQL replay of actual native Dump", err)
		}
	} else {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{`INCLUDE ("p")`, `"pg_catalog"."C"`, `"pg_catalog"."text_pattern_ops"`, `"pg_catalog"."int4_ops"`} {
			if !strings.Contains(dump.String(), fragment) {
				t.Fatalf("missing %s in dump: %s", fragment, dump.String())
			}
		}
		*stream = dump.String()
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='cover'", []string{"relname"}, []string{"NAME"}, nil))
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		foreign := newReferenceFixture(t, false, true)
		if err := stuntgres.Restore(foreign.ctx, foreign.native, strings.NewReader(dump.String())); err != nil {
			t.Fatal("foreign Restore", err)
		}
		referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceSelect("SELECT p FROM t WHERE a=1 AND b=2", []string{"p"}, []string{"TEXT"}, [][]any{{"xx"}}))
		var before bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
			t.Fatal(err)
		}
		err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"CREATE INDEX broken ON t(a) INCLUDE((missing+1));"))
		want := controlsReferenceDiagnostic("0A000", "expressions are not supported in included columns")
		referenceAssertDiagnostic(t, err, &want)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
			t.Fatal("late invalid restore changed destination", err)
		}
	}
	for _, tc := range []struct{ call, want string }{
		{"pg_get_indexdef('cover'::regclass)", "CREATE INDEX cover ON public.t USING btree (a, b DESC) INCLUDE (p)"},
		{"pg_get_indexdef('cover'::regclass,3,false)", "p"},
		{"pg_get_indexdef('cover'::regclass,4,false)", ""},
	} {
		s := deparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
		run(s)
		run(referencePrepared(s))
	}
	run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='cover'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(3)}}))
	run(referenceSelect("SELECT p FROM t WHERE a=1 AND b=2", []string{"p"}, []string{"TEXT"}, [][]any{{"xx"}}))
	run(referencePrepared(referenceExec("INSERT INTO t VALUES(3,4,'after')", 1)))
	run(referenceSelect("SELECT p FROM t WHERE a=3", []string{"p"}, []string{"TEXT"}, [][]any{{"after"}}))
}

func TestReferenceIndexClausesControls(t *testing.T) {
	var stream string
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		indexClausesSeed(run)
		run(referenceExec("CREATE INDEX cover ON t(a int4_ops ASC NULLS LAST,b DESC NULLS FIRST) INCLUDE(p)", 0))
		run(referenceExec(`CREATE INDEX text_cover ON t(p COLLATE pg_catalog."C" pg_catalog.text_pattern_ops) INCLUDE(a)`, 0))
		run(referenceExec("UPDATE t SET p='xx' WHERE p='x'", 1))
		indexClausesReplay(t, p, b, run, &stream)
	})
}

func TestReferenceIndexClausesFeatureSmoke(t *testing.T) {
	var stream string
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		indexClausesSeed(run)
		run(referenceExec("CREATE INDEX cover ON t(a int4_ops ASC NULLS LAST,b DESC NULLS FIRST) INCLUDE(p)", 0))
		run(referenceExec(`CREATE INDEX text_cover ON t(upper(p) COLLATE pg_catalog."C" pg_catalog.text_pattern_ops) INCLUDE(a)`, 0))
		indexClausesOrders(run)
		run(referencePrepared(referenceSelect("SELECT upper(p) AS folded FROM t WHERE a=1 ORDER BY upper(p)", []string{"folded"}, []string{"TEXT"}, [][]any{{"X"}, {"Y"}, {"Z"}})))
		run(referenceSelect("SELECT a,b FROM t WHERE p='x'", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		s := referenceSelect("SELECT p FROM t WHERE a=1", []string{"p"}, []string{"TEXT"}, [][]any{{"x"}, {"y"}, {"z"}})
		s.bag = true
		run(referencePrepared(s))
		run(referenceExec("UPDATE t SET p='xx' WHERE p='x'", 1))
		list := strings.TrimSuffix(strings.Repeat("p,", 31), ",")
		run(referenceExec("CREATE INDEX total32 ON t(a) INCLUDE("+list+")", 0))
		run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='total32'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(32)}}))
		run(referenceImplicitError("CREATE INDEX total33 ON t(a) INCLUDE("+list+",p)", referenceDiagnostic{Code: "54011", Message: "cannot use more than 32 columns in an index"}))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='total33'", []string{"relname"}, []string{"NAME"}, nil))
		indexClausesUnique(t, p, b, run)
		indexClausesReplay(t, p, b, run, &stream)
		if !b.pg {
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
				s := deparseRelationsTextStep(t, b, "SELECT "+tc.call+" AS definition", tc.want)
				run(s)
				run(referencePrepared(s))
			}
		}
		compact()
		run(referenceExec("ALTER TABLE t ALTER p TYPE TEXT USING p", 0))
		compact()
		run(referencePrepared(referenceSelect("SELECT p FROM t WHERE a=3", []string{"p"}, []string{"TEXT"}, [][]any{{"after"}})))
	})
}
