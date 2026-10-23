package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregateCollation(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec(`CREATE TABLE ca_coll_input(id integer,v text COLLATE "C",w text COLLATE "POSIX")`, 0),
			fixture.Exec("INSERT INTO ca_coll_input VALUES(1,'a','z'),(2,'b','y')", 2),
			fixture.Exec("CREATE AGGREGATE ca_coll_text(text) (SFUNC=pg_catalog.textcat,STYPE=text,INITCOND='')", 0),
			fixture.Exec("CREATE FUNCTION ca_coll_step(text,text,text) RETURNS text LANGUAGE SQL AS $$SELECT $1||$2||$3$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_coll_pair(text,text) (SFUNC=ca_coll_step,STYPE=text,INITCOND='')", 0),
			fixture.Exec("CREATE FUNCTION ca_coll_count_step(integer,text,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1+length($2)+length($3)$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_coll_count(text,text) (SFUNC=ca_coll_count_step,STYPE=integer,INITCOND='0')", 0))

		// Conflicting explicit ordinary inputs are rejected even with an
		// uncollatable result, an empty source, or a suppressed transition.
		for _, name := range []string{"ca_coll_pair", "ca_coll_count"} {
			for _, suffix := range []string{"", " WHERE FALSE"} {
				query := `/* é */ SELECT ` + name + `(v COLLATE "C",w COLLATE "POSIX") AS v FROM ca_coll_input` + suffix
				step := aggregateModifiersError(query, "42P21", `collation mismatch between explicit collations "C" and "POSIX"`, `COLLATE "POSIX"`)
				createAggregateBoth(t, p, b, c, step)
			}
			query := `/* é */ SELECT ` + name + `(v COLLATE "C",w COLLATE "POSIX") FILTER(WHERE FALSE) AS v FROM ca_coll_input`
			step := aggregateModifiersError(query, "42P21", `collation mismatch between explicit collations "C" and "POSIX"`, `COLLATE "POSIX"`)
			createAggregateBoth(t, p, b, c, step)
		}

		// An implicit conflict is indeterminate, not an eager transition error.
		// Concatenation needs no comparison collation; a stored TEXT column does.
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT ca_coll_pair(v,w ORDER BY id) AS v FROM ca_coll_input", []string{"v"}, []string{"TEXT"}, [][]any{{"azby"}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT ca_coll_count(v,w ORDER BY id) AS v FROM ca_coll_input", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}))
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("CREATE VIEW ca_coll_pair_c AS SELECT ca_coll_pair(v,w ORDER BY id) AS v FROM ca_coll_input", fixture.Diagnostic{Code: "42P22", Message: `could not determine which collation to use for view column "v"`, Hint: "Use the COLLATE clause to set the collation explicitly."})
			step.Prepared, step.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}

		views := []struct {
			name, expression, typ, collation string
			value, empty                     any
		}{
			{"ca_coll_c", `ca_coll_text(v COLLATE "C" ORDER BY id)`, "TEXT", "C", "ab", ""},
			{"ca_coll_posix", `ca_coll_text(v COLLATE "POSIX" ORDER BY id)`, "TEXT", "POSIX", "ab", ""},
			{"ca_coll_pair_c", `ca_coll_pair(v COLLATE "C",w COLLATE "C" ORDER BY id)`, "TEXT", "C", "azby", ""},
			{"ca_coll_pair_posix", `ca_coll_pair(v COLLATE "POSIX",w COLLATE "POSIX" ORDER BY id)`, "TEXT", "POSIX", "azby", ""},
			{"ca_coll_order", `ca_coll_text(v COLLATE "C" ORDER BY w COLLATE "POSIX")`, "TEXT", "C", "ba", ""},
			{"ca_coll_filter", `ca_coll_text(v COLLATE "C" ORDER BY id) FILTER(WHERE (w COLLATE "POSIX")<>'skip')`, "TEXT", "C", "ab", ""},
			{"ca_coll_modifiers", `ca_coll_pair(v COLLATE "C",w COLLATE "C" ORDER BY w COLLATE "POSIX") FILTER(WHERE (w COLLATE "POSIX")<>'skip')`, "TEXT", "C", "byaz", ""},
			{"ca_coll_integer", `ca_coll_count(v COLLATE "C",w COLLATE "C" ORDER BY id)`, "INT4", "", int64(4), int64(0)},
		}
		for _, view := range views {
			createAggregateRun(t, p, b, c, fixture.Exec("CREATE VIEW "+view.name+" AS SELECT "+view.expression+" AS v FROM ca_coll_input", 0))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT "+view.expression+" AS v FROM ca_coll_input", []string{"v"}, []string{view.typ}, [][]any{{view.value}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT "+view.expression+" AS v FROM ca_coll_input WHERE FALSE", []string{"v"}, []string{view.typ}, [][]any{{view.empty}}))
		}
		check := func() {
			for _, view := range views {
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT v FROM "+view.name, []string{"v"}, []string{view.typ}, [][]any{{view.value}}))
				// Filter the actual attribute owner before joining catalog rows;
				// preserve every matching row rather than hide ambiguity with LIMIT.
				attribute := "SELECT attcollation FROM pg_attribute WHERE attrelid='" + view.name + "'::regclass AND attname='v'"
				if view.collation == "" {
					createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.attcollation=0::oid AS uncollated FROM ("+attribute+") a", []string{"uncollated"}, []string{"BOOL"}, [][]any{{true}}))
				} else {
					createAggregateBoth(t, p, b, c, fixture.Select("SELECT c.collname FROM ("+attribute+") a JOIN pg_collation c ON c.oid=a.attcollation", []string{"collname"}, []string{"NAME"}, [][]any{{view.collation}}))
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
