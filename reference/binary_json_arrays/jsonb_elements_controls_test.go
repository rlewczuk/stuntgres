package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbElementsAtomic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
	run(fixture.Exec("CREATE TABLE jbe_atomic_source(id int,j jsonb)", 0))
	run(fixture.Exec(`INSERT INTO jbe_atomic_source VALUES(1,'[1,2]'),(2,'{}'),(3,'[]')`, 3))
	for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
		dest := "jbe_atomic_" + f
		run(fixture.Exec("CREATE TABLE "+dest+"(id int,v "+jsonbElementsType(f)+")", 0))
		run(fixture.Exec("INSERT INTO "+dest+" VALUES(0,'99')", 1))
		q := "INSERT INTO " + dest + " SELECT s.id,e.value FROM jbe_atomic_source s," + f + "(s.j) e ORDER BY s.id"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22023", "cannot extract elements from an object"))
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			run(fixture.Select("SELECT id,v FROM "+dest, []string{"id", "v"}, []string{"INT4", jsonbElementsType(f)}, [][]any{{int64(0), "99"}}))
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO "+dest+" SELECT s.id,e.value FROM jbe_atomic_source s,"+f+"(s.j) e WHERE s.id=1", 2)))
		run(fixture.Exec("INSERT INTO "+dest+" SELECT s.id,e.value FROM jbe_atomic_source s,"+f+"(s.j) e WHERE s.id=3", 0))
		run(fixture.Select("SELECT id,v FROM "+dest+" ORDER BY id,v", []string{"id", "v"}, []string{"INT4", jsonbElementsType(f)}, [][]any{{int64(0), "99"}, {int64(1), "1"}, {int64(1), "2"}}))
	}
}

func TestReferenceJSONBElementsConsumersControls(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbElementsAtomic(t, p, b, c)
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE jbe_store(id int,j jsonb)", 0))
		run(fixture.Exec(`INSERT INTO jbe_store VALUES(1,'[1,null,"x"]'),(2,'[]'),(3,NULL)`, 3))
		var checks []fixture.Step
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			run(fixture.Exec("CREATE VIEW jbe_from_"+f+" AS SELECT s.id,e.value,e.n FROM jbe_store s,"+f+"(s.j) WITH ORDINALITY e(value,n)", 0))
			run(fixture.Exec("CREATE VIEW jbe_target_"+f+" AS SELECT id,"+f+"(j) AS v FROM jbe_store", 0))
			n, x := any("null"), any(`"x"`)
			if jsonbElementsType(f) == "TEXT" {
				n, x = nil, "x"
			}
			checks = append(checks, fixture.Select("SELECT id,value,n FROM jbe_from_"+f+" ORDER BY id,n", []string{"id", "value", "n"}, []string{"INT4", jsonbElementsType(f), "INT8"}, [][]any{{int64(1), "1", int64(1)}, {int64(1), n, int64(2)}, {int64(1), x, int64(3)}}))
			s := fixture.Select("SELECT id,v FROM jbe_target_"+f, []string{"id", "v"}, []string{"INT4", jsonbElementsType(f)}, [][]any{{int64(1), "1"}, {int64(1), n}, {int64(1), x}})
			s.Bag = true
			checks = append(checks, s)
			run(fixture.Exec("CREATE TABLE jbe_materialized_"+f+" AS SELECT id,value,n FROM jbe_from_"+f, 3))
			m := checks[len(checks)-2]
			m.Query = "SELECT id,value,n FROM jbe_materialized_" + f + " ORDER BY id,n"
			checks = append(checks, m)
			// Literal-bearing target and FROM views exercise value capture as
			// well as remapping of source-dependent views during restore/replay.
			literal := `'[{"zz":0,"a":1,"a":2},null,"a\nb",1.00,[2,1]]'::jsonb`
			run(fixture.Exec("CREATE VIEW jbe_constant_target_"+f+" AS SELECT "+f+"("+literal+") AS v", 0))
			run(fixture.Exec("CREATE VIEW jbe_constant_from_"+f+" AS SELECT e.v,e.n FROM "+f+"("+literal+") WITH ORDINALITY AS e(v,n)", 0))
			values := []any{`{"a": 2, "zz": 0}`, "null", `"a\nb"`, "1.00", "[2, 1]"}
			if jsonbElementsType(f) == "TEXT" {
				values[1], values[2] = nil, "a\nb"
			}
			var targetRows, fromRows [][]any
			for i, value := range values {
				targetRows = append(targetRows, []any{value})
				fromRows = append(fromRows, []any{value, int64(i + 1)})
			}
			target := fixture.Select("SELECT v FROM jbe_constant_target_"+f, []string{"v"}, []string{jsonbElementsType(f)}, targetRows)
			target.Bag = true
			checks = append(checks,
				jsonbElementsViewMetadata(target, b.PG),
				jsonbElementsViewMetadata(fixture.Select("SELECT v,n FROM jbe_constant_from_"+f+" ORDER BY n", []string{"v", "n"}, []string{jsonbElementsType(f), "INT8"}, fromRows), b.PG),
			)
		}
		run(fixture.Exec("ALTER TABLE jbe_store RENAME COLUMN j TO document", 0))
		run(fixture.Exec("ALTER TABLE jbe_store RENAME TO jbe_documents", 0))
		for _, s := range checks {
			jsonTypeBoth(t, p, b, c, s)
		}
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, checks[0].Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.StringCoreRetainedRows(t, old, nil, checks[0].Columns, checks[0].Types, checks[0].Rows, false)
		for i, ct := range meta {
			if ct.Name() != checks[0].Columns[i] || ct.DatabaseTypeName() != checks[0].Types[i] {
				t.Fatal("retained metadata changed", ct)
			}
		}
		for _, s := range checks {
			jsonTypeBoth(t, p, b, c, s)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if dump.String() != redump.String() {
			t.Fatal("redump changed")
		}
		bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nINSERT INTO jbe_documents VALUES(9,'invalid'::jsonb);\n")...)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
			t.Fatal("invalid late restore succeeded")
		}
		for _, s := range checks {
			run(s)
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual dump replay", err)
			}
			for _, s := range checks {
				if s.CheckMetadata != nil {
					s = jsonbElementsViewMetadata(s, true)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
