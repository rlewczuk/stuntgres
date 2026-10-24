package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCastImplementedLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{
				"CREATE TYPE cast_life_enum AS ENUM ('red','blue')",
				"CREATE DOMAIN cast_life_positive AS integer CHECK(VALUE>0)",
				"CREATE TABLE cast_life_source(id integer,s text,n integer,j jsonb,e cast_life_enum,d cast_life_positive,c varchar(3) DEFAULT CAST('abcdef'::text AS varchar(3)),b bytea DEFAULT CAST(42 AS bytea))",
				"CREATE VIEW cast_life_view AS SELECT id,CAST(s AS uuid) AS u,CAST(n AS bytea) AS b,CAST(j AS integer) AS r,CAST(ARRAY[e,NULL::cast_life_enum] AS text[]) AS a,CAST(d AS bigint) AS x,c,b AS default_b FROM cast_life_source",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO cast_life_source(id,s,n,j,e,d) VALUES(1,'00000000000000000000000000000001',42,'2.5','red',7),(2,NULL,NULL,NULL,NULL,NULL)", 2))
			q := "SELECT id,u,b,r,a,x,c,default_b FROM cast_life_view ORDER BY id"
			want := fixture.Select(q, []string{"id", "u", "b", "r", "a", "x", "c", "default_b"}, []string{"INT4", "UUID", "BYTEA", "INT4", "_TEXT", "INT8", "VARCHAR", "BYTEA"}, [][]any{
				{int64(1), "00000000-0000-0000-0000-000000000001", "\x00\x00\x00*", int64(3), "{red,NULL}", int64(7), "abc", "\x00\x00\x00*"},
				{int64(2), nil, nil, nil, "{NULL,NULL}", nil, "abc", "\x00\x00\x00*"},
			})
			castWitnessRoutes(t, p, b, want)
			run(fixture.Exec("ALTER TABLE cast_life_source RENAME COLUMN s TO s_new", 0))
			castWitnessRoutes(t, p, b, want)
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				castWitnessRoutes(t, p, b, want)
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh-cast-owner")
				if err != nil {
					t.Fatal(err)
				}
				control := sql.OpenDB(connector)
				defer control.Close()
				if err = stuntgres.Restore(p.Ctx, control, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				fresh, rec := fixture.RecordedNativePool(t, connector)
				fixture.RunStep(t, p.Ctx, fresh, rec, want)
				fixture.RunStep(t, p.Ctx, fresh, rec, fixture.Prepared(want))
				// New inserts must execute restored defaults, not reuse dumped row values.
				insert := "INSERT INTO cast_life_source(id,s_new,n,j,e,d) VALUES(3,'00000000000000000000000000000002',43,'3.5','blue',8)"
				fixture.RunStep(t, p.Ctx, fresh, rec, fixture.Exec(insert, 1))
				third := fixture.Select("SELECT id,u,b,r,a,x,c,default_b FROM cast_life_view WHERE id=3", want.Columns, want.Types, [][]any{{int64(3), "00000000-0000-0000-0000-000000000002", "\x00\x00\x00+", int64(4), "{blue,NULL}", int64(8), "abc", "\x00\x00\x00*"}})
				fixture.RunStep(t, p.Ctx, fresh, rec, third)
				bad := dump.String() + ";CREATE TABLE cast_life_bad(v uuid);INSERT INTO cast_life_bad VALUES('00000000-0000-0000-0000-000000000001'),('bad');"
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
				diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type uuid: "bad"`)
				diagnostic.Position = fixture.Position(strings.LastIndex(bad, "'bad'") + 1)
				fixture.AssertDiagnostic(t, err, &diagnostic)
				castWitnessRoutes(t, p, b, want)
			}
		})
	}
}
