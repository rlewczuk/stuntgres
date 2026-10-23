package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspTimeFeatureSmoke(t *testing.T) {
	uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
		run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='uuid_ossp_part' AND p.proname IN ('uuid_generate_v1','uuid_generate_v1mc','uuid_generate_v4','uuid_nil','uuid_ns_dns','uuid_ns_url','uuid_ns_oid','uuid_ns_x500')", []string{"count"}, []string{"INT8"}, [][]any{{int64(8)}}))
		run(fixture.Exec("CREATE TABLE uuid_time_smoke_sentinel(id integer)", 0))
		run(fixture.Exec("INSERT INTO uuid_time_smoke_sentinel VALUES(7)", 1))
		seen := map[string]bool{}
		held := map[string]fixture.HeldStatement{}
		saved := map[string]string{}
		for _, name := range uuidOsspTimeNames {
			mc := name == "uuid_generate_v1mc"
			call := "uuid_ossp_part." + name + "()"
			q := "SELECT " + call + " AS u"
			values := uuidOsspTimeRead(t, p, c, r, pg, q, 1, 1, mc)
			uuidOsspTimeUnique(t, seen, values)
			uuidOsspTimeExtract(t, p, c, r, values[0], mc)
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			held[name] = fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			for range 2 {
				uuidOsspTimeUnique(t, seen, uuidOsspTimeRead(t, p, held[name], r, pg, q, 1, 1, mc))
			}
			uuidOsspTimeRead(t, p, c, r, pg, q+" WHERE FALSE", 0, 1, mc)
			uuidOsspFailure(t, p, c, "SELECT uuid_ossp_part."+name+"(NULL)", uuidOsspMissing("uuid_ossp_part."+name+"(unknown)", 8))
			run(fixture.Exec("CREATE TABLE "+name+"_smoke(id integer,u uuid DEFAULT "+call+")", 0))
			run(fixture.Exec("INSERT INTO "+name+"_smoke(id) VALUES(1),(2)", 2))
			rows, err := c.QueryContext(p.Ctx, "SELECT u AS u FROM "+name+"_smoke ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			var valuesSaved []string
			for rows.Next() {
				var v string
				if err := rows.Scan(&v); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				uuidOsspTimeDecode(t, v, mc)
				valuesSaved = append(valuesSaved, v)
			}
			if rows.Err() != nil || len(valuesSaved) != 2 {
				rows.Close()
				t.Fatal("smoke defaults", valuesSaved, rows.Err())
			}
			rows.Close()
			uuidOsspTimeUnique(t, seen, valuesSaved)
			saved[name] = valuesSaved[0]
			run(fixture.Exec("CREATE VIEW "+name+"_smoke_view AS SELECT "+call+" AS u", 0))
			for range 2 {
				uuidOsspTimeUnique(t, seen, uuidOsspTimeRead(t, p, c, r, pg, "SELECT u FROM "+name+"_smoke_view", 1, 1, mc, true))
			}
		}
		run(fixture.Select("SELECT id FROM uuid_time_smoke_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if pg {
			return
		}
		dump := extensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		for _, name := range uuidOsspTimeNames {
			var value string
			if err := c.QueryRowContext(p.Ctx, "SELECT u FROM "+name+"_smoke WHERE id=1").Scan(&value); err != nil || value != saved[name] {
				t.Fatal("saved UUID restore", value, err)
			}
			uuidOsspTimeUnique(t, seen, uuidOsspTimeRead(t, p, held[name], r, false, held[name].SQL, 1, 1, name == "uuid_generate_v1mc"))
			uuidOsspTimeUnique(t, seen, uuidOsspTimeRead(t, p, c, r, false, "SELECT u FROM "+name+"_smoke_view", 1, 1, name == "uuid_generate_v1mc", true))
			run(fixture.Exec("INSERT INTO "+name+"_smoke(id) VALUES(3)", 1))
			if err := c.QueryRowContext(p.Ctx, "SELECT u FROM "+name+"_smoke WHERE id=3").Scan(&value); err != nil {
				t.Fatal(err)
			}
			uuidOsspTimeDecode(t, value, name == "uuid_generate_v1mc")
			uuidOsspTimeUnique(t, seen, []string{value})
		}
	})
}
