package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregateDomainCollation(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec(`CREATE DOMAIN ca_state_c AS text COLLATE "C"`, 0),
			fixture.Exec("CREATE FUNCTION ca_domain_keep_step(ca_state_c,text) RETURNS ca_state_c LANGUAGE SQL AS $$SELECT $1$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_domain_keep(text) (SFUNC=ca_domain_keep_step,STYPE=ca_state_c,INITCOND='seed')", 0),
			fixture.Exec("CREATE FUNCTION ca_domain_zero_step(ca_state_c) RETURNS ca_state_c LANGUAGE SQL AS $$SELECT $1$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_domain_zero(*) (SFUNC=ca_domain_zero_step,STYPE=ca_state_c,INITCOND='seed')", 0),
			fixture.Exec("CREATE FUNCTION ca_domain_integer_step(ca_state_c,integer) RETURNS ca_state_c LANGUAGE SQL AS $$SELECT $1$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_domain_integer(integer) (SFUNC=ca_domain_integer_step,STYPE=ca_state_c,INITCOND='seed')", 0),
			fixture.Exec(`CREATE TABLE ca_domain_input(id integer,p text COLLATE "POSIX",d text)`, 0),
			fixture.Exec("INSERT INTO ca_domain_input VALUES(1,'a','b')", 1))
		views := []struct{ name, expression, collation string }{
			{"ca_domain_posix", "ca_domain_keep(p)", "POSIX"},
			{"ca_domain_default", "ca_domain_keep(d)", "default"},
			{"ca_domain_modifiers", `ca_domain_keep(p ORDER BY d COLLATE "C") FILTER(WHERE (d COLLATE "C")<>'skip')`, "POSIX"},
			{"ca_domain_zero_result", "ca_domain_zero(*)", "C"},
			{"ca_domain_integer_result", "ca_domain_integer(id)", "C"},
		}
		for _, view := range views {
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT "+view.expression+"::text AS v FROM ca_domain_input", []string{"v"}, []string{"TEXT"}, [][]any{{"seed"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT "+view.expression+"::text AS v FROM ca_domain_input WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{"seed"}}))
			createAggregateRun(t, p, b, c, fixture.Exec("CREATE VIEW "+view.name+" AS SELECT "+view.expression+" AS v FROM ca_domain_input", 0))
		}
		held := make([]fixture.HeldStatement, 0, 2)
		for _, input := range []string{"p", "d"} {
			query := "SELECT ca_domain_keep(" + input + ")::text AS v FROM ca_domain_input WHERE id<=$1"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query})
		}
		check := func() {
			for _, view := range views {
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT v::text AS v FROM "+view.name, []string{"v"}, []string{"TEXT"}, [][]any{{"seed"}}))
				attribute := "SELECT attcollation,atttypid FROM pg_attribute WHERE attrelid='" + view.name + "'::regclass AND attname='v'"
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT c.collname,a.atttypid='ca_state_c'::regtype AS state_type FROM ("+attribute+") a JOIN pg_collation c ON c.oid=a.attcollation", []string{"collname", "state_type"}, []string{"NAME", "BOOL"}, [][]any{{view.collation, true}}))
			}
			for _, statement := range held {
				for _, limit := range []int64{1, 0} {
					fixture.RunStep(t, p.Ctx, statement, b.Recorder, fixture.Select(statement.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{"seed"}}, limit))
				}
			}
		}
		check()
		if b.PG {
			return
		}
		var emitted bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &emitted); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted.String())); err != nil {
				t.Fatal(err)
			}
			check()
		}
	})
}
