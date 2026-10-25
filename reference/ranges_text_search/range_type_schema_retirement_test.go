package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeSchemaRetirement(t *testing.T) {
	for _, target := range []string{"range_schema_source", "range_schema_companion"} {
		t.Run(target, func(t *testing.T) {
			for _, schema := range []string{"range_schema_source", "range_schema_companion", "range_schema_consumer"} {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				for _, q := range []string{
					"CREATE SCHEMA range_schema_source", "CREATE SCHEMA range_schema_companion", "CREATE SCHEMA range_schema_consumer",
					"CREATE TYPE range_schema_source.span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=range_schema_companion.many)",
					"CREATE TABLE range_schema_consumer.saved(k integer,r range_schema_source.span,m range_schema_companion.many)",
					"CREATE TABLE range_schema_consumer.keep(k integer)",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO range_schema_consumer.saved VALUES(1,'[1,3]','{[1,3],[8,9]}')", 1))
				run(fixture.Exec("INSERT INTO range_schema_consumer.keep VALUES(91)", 1))
				if target == "range_schema_source" {
					run(fixture.Exec("CREATE TABLE range_schema_companion.keep(k integer)", 0))
					run(fixture.Exec("INSERT INTO range_schema_companion.keep VALUES(92)", 1))
				}
				saved := fixture.Select("SELECT k,r::text AS r,m::text AS m FROM range_schema_consumer.saved", []string{"k", "r", "m"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "[1,3]", "{[1,3],[8,9]}"}})
				detail := "type range_schema_source.span depends on schema range_schema_source\ncolumn m of table range_schema_consumer.saved depends on type range_schema_companion.many\ncolumn r of table range_schema_consumer.saved depends on type range_schema_source.span"
				if target == "range_schema_companion" {
					detail = "type range_schema_source.span depends on schema range_schema_companion\ncolumn m of table range_schema_consumer.saved depends on type range_schema_companion.many\ncolumn r of table range_schema_consumer.saved depends on type range_schema_source.span"
				}
				denied := fixture.ImplicitError("DROP SCHEMA "+target+" RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop schema " + target + " because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				denied.ErrorStage = "execute"
				rangeTypeRemedyRoutes(t, p, b, c, denied)
				run(saved)
				// Recreate the graph from actual bytes before retiring it on native. The
				// independent PG branch exercises the same ordinary schema operation.
				if !b.PG {
					var script bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
						t.Fatal(err)
					}
					held, err := c.QueryContext(p.Ctx, saved.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
						t.Fatal(err)
					}
					fixture.RangeConsumeHeld(t, held, saved)
					run(saved)
				}
				type address struct {
					class string
					id    int64
				}
				var removed []address
				for _, entry := range []struct {
					class, query string
					count        int
				}{
					{"pg_type", "SELECT oid::bigint FROM pg_type WHERE typnamespace IN ('range_schema_source'::regnamespace,'range_schema_companion'::regnamespace) AND typname IN ('span','_span','many','_many')", 4},
					{"pg_proc", "SELECT oid::bigint FROM pg_proc WHERE pronamespace='range_schema_source'::regnamespace AND proname IN ('span','many')", 5},
					{"pg_cast", "SELECT oid::bigint FROM pg_cast WHERE castsource='range_schema_source.span'::regtype AND casttarget='range_schema_companion.many'::regtype", 1},
				} {
					rows, err := c.QueryContext(p.Ctx, entry.query)
					if err != nil {
						t.Fatal(err)
					}
					count := 0
					for rows.Next() {
						var id int64
						if err := rows.Scan(&id); err != nil {
							t.Fatal(err)
						}
						removed = append(removed, address{entry.class, id})
						count++
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					if count != entry.count {
						t.Fatal("incomplete schema family before drop", entry.class, count)
					}
				}
				run(fixture.Exec("DROP SCHEMA "+target+" CASCADE", 0))
				for _, object := range removed {
					run(fixture.Select("SELECT count(*) AS n FROM "+object.class+" WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, object.id))
					run(fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE (classid='"+object.class+"'::regclass AND objid=$1::oid) OR (refclassid='"+object.class+"'::regclass AND refobjid=$1::oid)", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, object.id))
				}
				run(fixture.Select("SELECT k FROM range_schema_consumer.saved", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='range_schema_consumer.saved'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"k"}}))
				run(fixture.Select("SELECT k FROM range_schema_consumer.keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(91)}}))
				if target == "range_schema_source" {
					run(fixture.Select("SELECT k FROM range_schema_companion.keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(92)}}))
				}
			})
		})
	}
}
