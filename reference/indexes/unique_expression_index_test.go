package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// One scenario body per prepared route: DDL/DML are prepared when the matrix
// asks for it, while catalog probes stay direct. Every expectation is the
// pinned PostgreSQL 18.6 observation, never learned from native.
type uniqueExpressionCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	prepared bool
}

func (c uniqueExpressionCase) run(s fixture.Step) {
	fixture.RunStep(c.t, c.p.Ctx, c.b.DB, c.b.Recorder, s)
}

func (c uniqueExpressionCase) exec(query string, count int64) {
	s := fixture.Exec(query, count)
	if c.prepared {
		s = fixture.Prepared(s)
	}
	c.run(s)
}

// A unique-expression utility is admitted by Prepare and rejected at Execute.
func (c uniqueExpressionCase) fail(query string, want fixture.Diagnostic) {
	s := fixture.ErrorDiagnostic(query, want)
	s.ErrorStage = "execute"
	if c.prepared {
		s = fixture.Prepared(s)
	}
	c.run(s)
}

func (c uniqueExpressionCase) probe(query string, columns, types []string, rows [][]any) {
	c.run(fixture.Select(query, columns, types, rows))
}

func TestReferenceUniqueExpressionIndex(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					t.Run("lower-expression-index", func(t *testing.T) {
						c := uniqueExpressionCase{t, p, b, prepared}
						c.exec("CREATE TABLE ux(id INT, username TEXT)", 0)
						c.exec("CREATE UNIQUE INDEX ux_lower_idx ON ux(lower(username))", 0)
						c.exec("INSERT INTO ux VALUES(1,'bob')", 1)
						c.fail("INSERT INTO ux VALUES(2,'BOB')", fixture.UniqueReferenceDuplicate("ux", "ux_lower_idx", "Key (lower(username))=(bob) already exists."))
						// Default NULLS DISTINCT never conflicts NULL expression results.
						c.exec("INSERT INTO ux VALUES(3,NULL),(4,NULL)", 2)
						c.probe("SELECT pg_get_indexdef('ux_lower_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX ux_lower_idx ON public.ux USING btree (lower(username))"}})
						c.probe("SELECT indexdef FROM pg_indexes WHERE schemaname='public' AND indexname='ux_lower_idx'", []string{"indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX ux_lower_idx ON public.ux USING btree (lower(username))"}})
						c.probe("SELECT pg_get_indexdef('ux_lower_idx'::regclass,1,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"lower(username)"}})
						c.probe("SELECT attname FROM pg_attribute WHERE attrelid='ux_lower_idx'::regclass AND attnum=1", []string{"attname"}, []string{"NAME"}, [][]any{{"lower"}})
						// A standalone unique expression index is not a constraint.
						c.probe("SELECT count(*) AS n FROM pg_constraint WHERE conname='ux_lower_idx'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
						c.probe("SELECT indnatts,indnkeyatts,indisunique,indnullsnotdistinct,indimmediate,indkey::text FROM pg_index WHERE indexrelid='ux_lower_idx'::regclass", []string{"indnatts", "indnkeyatts", "indisunique", "indnullsnotdistinct", "indimmediate", "indkey"}, []string{"INT2", "INT2", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{int64(1), int64(1), true, false, true, "0"}})
					})
					t.Run("migration-statement", func(t *testing.T) {
						c := uniqueExpressionCase{t, p, b, prepared}
						// coder migration 000001 line 24 verbatim.
						c.exec("CREATE TABLE users(id INT, username TEXT)", 0)
						c.exec("CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_idx ON users USING btree (lower(username))", 0)
						c.exec("INSERT INTO users VALUES(1,'bob')", 1)
						c.fail("INSERT INTO users VALUES(2,'BOB')", fixture.UniqueReferenceDuplicate("users", "users_username_lower_idx", "Key (lower(username))=(bob) already exists."))
					})
					t.Run("composite-partial", func(t *testing.T) {
						c := uniqueExpressionCase{t, p, b, prepared}
						c.exec("CREATE TABLE t2(owner_id INT, name TEXT, tag TEXT)", 0)
						c.exec("INSERT INTO t2 VALUES(1,'ann','n')", 1)
						c.exec("CREATE UNIQUE INDEX t2_lower ON t2(owner_id, lower(name)) WHERE tag <> 'skip'", 0)
						c.fail("INSERT INTO t2 VALUES(1,'aNn','n')", fixture.UniqueReferenceDuplicate("t2", "t2_lower", "Key (owner_id, lower(name))=(1, ann) already exists."))
						c.exec("INSERT INTO t2 VALUES(1,'ann','skip')", 1)
						c.exec("INSERT INTO t2 VALUES(1,'ann','skip')", 1)
						c.exec("INSERT INTO t2 VALUES(3,'ann','q')", 1)
						c.probe("SELECT owner_id,name,tag FROM t2 ORDER BY owner_id,name,tag", []string{"owner_id", "name", "tag"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "ann", "n"}, {int64(1), "ann", "skip"}, {int64(1), "ann", "skip"}, {int64(3), "ann", "q"}})
						c.probe("SELECT pg_get_indexdef('t2_lower'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX t2_lower ON public.t2 USING btree (owner_id, lower(name)) WHERE (tag <> 'skip'::text)"}})
					})
					t.Run("nulls-not-distinct", func(t *testing.T) {
						c := uniqueExpressionCase{t, p, b, prepared}
						c.exec("CREATE TABLE t4(a TEXT)", 0)
						c.exec("INSERT INTO t4 VALUES('a')", 1)
						c.exec("CREATE UNIQUE INDEX t4n ON t4(lower(a)) NULLS NOT DISTINCT", 0)
						c.fail("INSERT INTO t4 VALUES('A')", fixture.UniqueReferenceDuplicate("t4", "t4n", "Key (lower(a))=(a) already exists."))
						c.exec("INSERT INTO t4 VALUES(NULL)", 1)
						c.fail("INSERT INTO t4 VALUES(NULL)", fixture.UniqueReferenceDuplicate("t4", "t4n", "Key (lower(a))=(null) already exists."))
						c.probe("SELECT pg_get_indexdef('t4n'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX t4n ON public.t4 USING btree (lower(a)) NULLS NOT DISTINCT"}})
					})
					t.Run("existing-data", func(t *testing.T) {
						c := uniqueExpressionCase{t, p, b, prepared}
						c.exec("CREATE TABLE u(a TEXT)", 0)
						c.exec("INSERT INTO u VALUES('bob'),('BOB')", 2)
						c.fail("CREATE UNIQUE INDEX u_lower ON u(lower(a))", uniqueIndexReferenceBuildDiagnostic("u", "u_lower", "Key (lower(a))=(bob) is duplicated."))
						// The failed non-concurrent build leaves no descriptor.
						c.probe("SELECT relname FROM pg_class WHERE relname='u_lower'", []string{"relname"}, []string{"NAME"}, nil)
						c.exec("DELETE FROM u WHERE a='BOB'", 1)
						c.exec("CREATE UNIQUE INDEX u_lower ON u(lower(a))", 0)
						c.fail("INSERT INTO u VALUES('Bob')", fixture.UniqueReferenceDuplicate("u", "u_lower", "Key (lower(a))=(bob) already exists."))
						c.exec("CREATE TABLE un(a TEXT)", 0)
						c.exec("INSERT INTO un VALUES(NULL),(NULL)", 2)
						c.fail("CREATE UNIQUE INDEX un_nnd ON un(lower(a)) NULLS NOT DISTINCT", uniqueIndexReferenceBuildDiagnostic("un", "un_nnd", "Key (lower(a))=(null) is duplicated."))
					})
					t.Run("immutable-expression", func(t *testing.T) {
						c := uniqueExpressionCase{t, p, b, prepared}
						c.exec("CREATE TABLE rx(a INT)", 0)
						c.fail("CREATE UNIQUE INDEX rx_rand ON rx ((random()::text))", fixture.ControlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE"))
						c.probe("SELECT indexname FROM pg_indexes WHERE indexname='rx_rand'", []string{"indexname"}, []string{"NAME"}, nil)
						// The rejected name is reusable by an immutable expression.
						c.exec("CREATE UNIQUE INDEX rx_rand ON rx (lower(a::text))", 0)
					})
					t.Run("forbidden-expression", func(t *testing.T) {
						c := uniqueExpressionCase{t, p, b, prepared}
						c.exec("CREATE TABLE fx(a INT)", 0)
						forbidden := func(query, code, message, token string) {
							d := fixture.ControlsReferenceDiagnostic(code, message)
							d.Position = fixture.Position(strings.Index(query, token) + 1)
							c.fail(query, d)
						}
						forbidden("CREATE UNIQUE INDEX fx_agg ON fx((count(*)))", "42803", "aggregate functions are not allowed in index expressions", "count")
						forbidden("CREATE UNIQUE INDEX fx_win ON fx((row_number() OVER ()))", "42P20", "window functions are not allowed in index expressions", "row_number")
						forbidden("CREATE UNIQUE INDEX fx_sub ON fx(((SELECT 1)))", "0A000", "cannot use subquery in index expression", "(SELECT")
						c.probe("SELECT indexname FROM pg_indexes WHERE indexname IN ('fx_agg','fx_win','fx_sub') ORDER BY indexname", []string{"indexname"}, []string{"NAME"}, nil)
						c.exec("CREATE UNIQUE INDEX fx_ok ON fx(lower(a::text))", 0)
					})
				})
			}
		})
	}
}

func TestReferenceUniqueExpressionIndexControls(t *testing.T) {
	t.Run("concurrently", func(t *testing.T) {
		fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE c1(a TEXT)", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO c1 VALUES('A')", 1)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY c1i ON c1(lower(a))", 0)
			fixture.ConcurrentIndexWait(t, p, b, "c1i", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO c1 VALUES('a')", fixture.ConcurrentIndexUnique("c1", "c1i", "Key (lower(a))=(a) already exists.", false))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_get_indexdef('c1i'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX c1i ON public.c1 USING btree (lower(a))"}}))
		})
	})
	t.Run("partitioned", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE p1(id INT,name TEXT) PARTITION BY RANGE(id)", 0))
				run(fixture.Exec("CREATE TABLE p1a PARTITION OF p1 FOR VALUES FROM (0) TO (10)", 0))
				run(fixture.Exec("CREATE TABLE p1b PARTITION OF p1 FOR VALUES FROM (10) TO (20)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX p1i ON p1(id, lower(name))", 0))
				run(fixture.Select("SELECT pg_get_indexdef('p1i'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX p1i ON ONLY public.p1 USING btree (id, lower(name))"}}))
				// Child names derive from the shared algorithm and the expression label.
				run(fixture.Select("SELECT c.relname,i.indisunique,i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_class p ON p.oid=i.indrelid WHERE p.relname IN ('p1a','p1b') ORDER BY c.relname", []string{"relname", "indisunique", "indisvalid"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"p1a_id_lower_idx", true, true}, {"p1b_id_lower_idx", true, true}}))
				run(fixture.Exec("INSERT INTO p1 VALUES(1,'Ann')", 1))
				run(fixture.ErrorDiagnostic("INSERT INTO p1 VALUES(1,'ANN')", fixture.UniqueReferenceDuplicate("p1a", "p1a_id_lower_idx", "Key (id, lower(name))=(1, ann) already exists.")))
				run(fixture.Exec("CREATE TABLE p2(id INT,name TEXT) PARTITION BY RANGE(id)", 0))
				run(fixture.Exec("CREATE TABLE p2a PARTITION OF p2 FOR VALUES FROM (0) TO (10)", 0))
				missing := func(query string) {
					run(fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "unique constraint on partitioned table must include all partitioning columns", Detail: `UNIQUE constraint on table "p2" lacks column "id" which is part of the partition key.`}))
				}
				missing("CREATE UNIQUE INDEX p2i ON p2(lower(name))")
				// An expression is not the partition-key column.
				missing("CREATE UNIQUE INDEX p2k ON p2(lower(id::text), name)")
			})
		}
	})
	t.Run("temporary", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				// TEMP objects are session-scoped, so one connection owns the body.
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK"); _ = conn.Close() })
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				run(fixture.Exec("CREATE TEMP TABLE tmp_u(a TEXT)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX tmp_u_lower ON tmp_u(lower(a))", 0))
				run(fixture.Exec("INSERT INTO tmp_u VALUES('A')", 1))
				_, err = conn.ExecContext(p.Ctx, "INSERT INTO tmp_u VALUES('a')")
				got := fixture.ExtractDiagnostic(t, err)
				if !strings.HasPrefix(got.Schema, "pg_temp_") {
					t.Fatalf("temporary duplicate schema got %q", got.Schema)
				}
				got.Schema = "public"
				want := fixture.UniqueReferenceDuplicate("tmp_u", "tmp_u_lower", "Key (lower(a))=(a) already exists.")
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("temporary duplicate got %#v want %#v", got, want)
				}
				run(fixture.Select("SELECT pg_get_indexdef('tmp_u_lower'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX tmp_u_lower ON pg_temp.tmp_u USING btree (lower(a))"}}))
			})
		}
	})
}