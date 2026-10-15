package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceArrayRelationalConsumersReplay(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE ar_consumers(id INT,a INT[],b TEXT[],n INT)",
			"CREATE TABLE ar_inserted(id INT,x INT,y TEXT)",
			"CREATE VIEW ar_view AS SELECT id,x,y,k FROM ar_consumers,unnest(a,b) WITH ORDINALITY AS u(x,y,k)",
			"CREATE VIEW ar_literal_view AS SELECT x,y,k FROM unnest('[0:1]={8,NULL}'::int[],'[5:7]={a,b,c}'::text[]) WITH ORDINALITY AS u(x,y,k)",
			"CREATE VIEW ar_random_view AS SELECT id,cardinality(array_sample(a,n)) AS n,cardinality(array_shuffle(a)) AS m FROM ar_consumers",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ar_consumers VALUES (1,'[-2:-1]={1,NULL}','[5:7]={a,b,c}',1),(2,'{3}','{d}',0)", 2))
		check := fixture.Select("SELECT id,x,y,k FROM ar_view ORDER BY id,k", []string{"id", "x", "y", "k"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(1), int64(1), "a", int64(1)}, {int64(1), nil, "b", int64(2)}, {int64(1), nil, "c", int64(3)}, {int64(2), int64(3), "d", int64(1)}})
		literals := fixture.Select("SELECT x,y,k FROM ar_literal_view ORDER BY k", []string{"x", "y", "k"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(8), "a", int64(1)}, {nil, "b", int64(2)}, {nil, "c", int64(3)}})
		random := fixture.Select("SELECT id,n,m FROM ar_random_view ORDER BY id", []string{"id", "n", "m"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(2)}, {int64(2), int64(0), int64(1)}})
		for _, s := range []fixture.Step{check, literals, random} {
			arrayRelationalBoth(t, p, b, c, s)
		}
		run(fixture.Exec("INSERT INTO ar_inserted SELECT id,x,y FROM ar_view", 4))
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT id,x,y FROM ar_inserted ORDER BY id,y", []string{"id", "x", "y"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "a"}, {int64(1), nil, "b"}, {int64(1), nil, "c"}, {int64(2), int64(3), "d"}}))
		// SELECT INTO/CTAS rows affected counts are independently part of the contract.
		run(fixture.Exec("CREATE TABLE ar_ctas AS SELECT x,y FROM ar_literal_view", 3))
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT x,y FROM ar_ctas ORDER BY y", []string{"x", "y"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "a"}, {nil, "b"}, {nil, "c"}}))
		for _, q := range []string{
			"SELECT id,unnest(a) AS x FROM ar_consumers GROUP BY id,a ORDER BY id,x NULLS LAST",
			"SELECT id,abs(unnest(a)) AS x FROM ar_consumers GROUP BY id,a ORDER BY id,x NULLS LAST",
		} {
			arrayRelationalBoth(t, p, b, c, fixture.Select(q, []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), nil}, {int64(2), int64(3)}}))
		}
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT id,cardinality(CASE WHEN id=1 THEN array_sample(a,n) ELSE array_shuffle(a) END) AS n FROM ar_consumers ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
		for _, q := range []string{"ALTER TABLE ar_consumers RENAME COLUMN a TO choices", "ALTER TABLE ar_consumers RENAME TO ar_renamed"} {
			run(fixture.Exec(q, 0))
		}
		for _, s := range []fixture.Step{check, literals, random} {
			arrayRelationalBoth(t, p, b, c, s)
		}
		if !b.PG {
			arrayRelationalReplay(t, p, b, c, []fixture.Step{check, literals, random}, []string{"ar_renamed", "ar_inserted", "ar_ctas"}, []string{"ar_view", "ar_literal_view", "ar_random_view"})
		}
	})
}

func arrayRelationalReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, checks []fixture.Step, tables, views []string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	retained, err := c.QueryContext(p.Ctx, checks[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.ArrayReferenceConsumeHeld(t, retained, checks[0].Rows, checks[0].Types)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, s := range checks {
		arrayRelationalBoth(t, p, b, c, s)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatalf("symbolic FROM replay changed dump\nfirst=%s\nsecond=%s", dump.String(), redump.String())
	}
	if p.Postgres != nil {
		cleanup := func() {
			for _, name := range views {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW IF EXISTS "+name+" CASCADE"); err != nil {
					t.Error(err)
				}
			}
			for _, name := range tables {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE IF EXISTS "+name+" CASCADE"); err != nil {
					t.Error(err)
				}
			}
		}
		// The native backend runs first; leave the oracle clean for its own scenario.
		defer cleanup()
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual PostgreSQL replay of emitted SQL", err)
		}
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(s))
		}
	}
}
