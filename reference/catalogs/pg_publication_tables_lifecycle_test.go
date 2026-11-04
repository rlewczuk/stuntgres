package reference_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGPublicationTablesMembershipLifecycle(t *testing.T) {
	t.Run("explicit-schema", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := publicationConn(t, p, b)
			peer := publicationConn(t, p, b)
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run := func(steps ...fixture.Step) { publicationRun(t, p, b, c, steps...) }
			query := publicationMembers("publication_rt_p").Query
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			state := func(rows ...[]any) { publicationRun(t, p, b, held, publicationMembers("publication_rt_p", rows...)) }
			a := []any{"publication_rt_s", "a"}
			bb := []any{"publication_rt_s", "b"}
			cc := []any{"publication_rt_s", "c"}
			state()
			run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0), fixture.Exec("CREATE TABLE publication_rt_s.a(id integer)", 0), fixture.Exec("CREATE TABLE publication_rt_s.b(id integer)", 0), fixture.Exec("CREATE PUBLICATION publication_rt_p", 0))
			state()
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_s.a", 0))
			state(a)
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_s.b", 0))
			state(a, bb)
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_s.a", 0))
			state(bb)
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_s.a", 0))
			state(a)
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_s", 0))
			state(a, bb)
			run(fixture.Exec("CREATE TABLE publication_rt_s.c(id integer)", 0))
			state(a, bb, cc)
			run(fixture.Exec("DROP TABLE publication_rt_s.c", 0))
			state(a, bb)
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLES IN SCHEMA publication_rt_s", 0))
			state(a)
			for _, finish := range []string{"ROLLBACK", "COMMIT"} {
				run(fixture.Exec("BEGIN", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_s.b", 0))
				state(a, bb)
				publicationRun(t, p, b, peer, publicationMembers("publication_rt_p", a))
				run(fixture.Exec(finish, 0))
				if finish == "ROLLBACK" {
					state(a)
				} else {
					state(a, bb)
					publicationRun(t, p, b, peer, publicationMembers("publication_rt_p", a, bb))
				}
			}
			run(fixture.Exec("CREATE TABLE publication_rt_s.c(id integer)", 0), publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_s.c,publication_rt_s.a", "42710", `relation "a" is already member of publication "publication_rt_p"`, "", ""))
			state(a, bb)
			run(fixture.Exec("DROP TABLE publication_rt_s.b", 0))
			state(a)
			run(fixture.Exec("DROP PUBLICATION publication_rt_p", 0))
			state()
			run(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_s.c", 0))
			state(cc)
			publicationRun(t, p, b, held, publicationMembers("publication_rt_missing"))
		})
	})
	t.Run("all-future-tables", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := publicationConn(t, p, b)
			run := func(steps ...fixture.Step) { publicationRun(t, p, b, c, steps...) }
			query := "SELECT schemaname,tablename FROM pg_catalog.pg_publication_tables WHERE pubname='publication_rt_p' AND schemaname='publication_rt_s' ORDER BY schemaname,tablename"
			state := func(rows ...[]any) {
				s := fixture.Select(query, []string{"schemaname", "tablename"}, []string{"NAME", "NAME"}, rows)
				run(s, fixture.Prepared(s))
			}
			run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR ALL TABLES", 0))
			state()
			run(fixture.Exec("CREATE TABLE publication_rt_s.a(id integer)", 0))
			state([]any{"publication_rt_s", "a"})
			run(fixture.Exec("CREATE TABLE publication_rt_s.b(id integer)", 0))
			state([]any{"publication_rt_s", "a"}, []any{"publication_rt_s", "b"})
			run(fixture.Exec("DROP TABLE publication_rt_s.a", 0))
			state([]any{"publication_rt_s", "b"})
			run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication_rel r JOIN pg_catalog.pg_publication p ON p.oid=r.prpubid WHERE p.pubname='publication_rt_p'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
}

func publicationTablesLocalRows() fixture.Step {
	return fixture.Select("SELECT id,v FROM publication_rt_s.t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-1)}, {int64(2), int64(2)}})
}

// Only the actual native-control boundary is backend-specific. The same SQL
// expectations run against PostgreSQL before this helper is called.
func publicationTablesRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, want ...[]any) {
	t.Helper()
	if b.PG {
		return
	}
	old, err := p.Native.QueryContext(p.Ctx, publicationTablesSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	publicationTablesMetadata(t, metadata)
	stmt, err := b.DB.PrepareContext(p.Ctx, publicationTablesSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: publicationTablesSQL}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		publicationTablesRead(t, p, b, b.DB, publicationTablesSQL, prepared)
	}
	publicationRun(t, p, b, held, publicationTablesState())
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		publicationTablesRead(t, p, b, b.DB, publicationTablesSQL, prepared, want...)
	}
	publicationRun(t, p, b, held, publicationTablesState(want...), publicationTablesLocalRows())
	publicationTablesConsume(t, b, old, want)
	publicationTablesMetadata(t, metadata)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := p.Native.QueryContext(ctx, publicationTablesSQL)
	if rows != nil {
		rows.Close()
		t.Fatal("pre-canceled native query exposed rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled native read: %v, want context.Canceled", err)
	}
	publicationTablesRead(t, p, b, b.DB, publicationTablesSQL, false, want...)
}

func TestReferencePGPublicationTablesNativeControls(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := publicationConn(t, p, b)
		run := func(steps ...fixture.Step) { publicationRun(t, p, b, c, steps...) }
		run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0), fixture.Exec("CREATE TABLE publication_rt_s.t(id integer,v integer)", 0), fixture.Exec("INSERT INTO publication_rt_s.t VALUES(1,-1),(2,2)", 2), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_s.t(v,id) WHERE(v>0) WITH(publish='insert')", 0), fixture.Exec("CREATE PUBLICATION publication_rt_q FOR TABLES IN SCHEMA publication_rt_s", 0))
		filtered := []any{"publication_rt_p", "publication_rt_s", "t", "{id,v}", "(v > 0)"}
		broad := []any{"publication_rt_q", "publication_rt_s", "t", "{id,v}", nil}
		stmt, err := c.PrepareContext(p.Ctx, publicationTablesSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: publicationTablesSQL}
		for _, prepared := range []bool{false, true} {
			publicationTablesRead(t, p, b, c, publicationTablesSQL, prepared, filtered, broad)
		}
		publicationRun(t, p, b, held, publicationTablesState(filtered, broad), publicationTablesLocalRows())
		publicationTablesRoundtrip(t, p, b, filtered, broad)
		if b.PG {
			return
		}
		run(fixture.Exec("CREATE TABLE publication_rt_s.u(id integer)", 0))
		publicationRun(t, p, b, held, publicationTablesState(filtered, broad, []any{"publication_rt_q", "publication_rt_s", "u", "{id}", nil}))
	})
}
