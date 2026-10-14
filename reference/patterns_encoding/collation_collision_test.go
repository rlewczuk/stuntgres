package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationNativeCollision(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if !b.PG {
			// Match the target's second real TEMP namespace allocation.
			fixture.CollationExec(t, p, b, `CREATE COLLATION namespace_collision_padding FROM pg_catalog."C"`)
		}
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_collision_anchor FROM pg_catalog."C"`, `CREATE COLLATION coll_collision_full FROM pg_catalog.pg_unicode_fast`, `CREATE COLLATION coll_collision_simple (PROVIDER=builtin,LOCALE='C.UTF-8')`, `CREATE DOMAIN coll_collision_domain AS TEXT COLLATE coll_collision_full`, `CREATE TABLE coll_collision_t(id INT,v coll_collision_domain DEFAULT ('ß' COLLATE coll_collision_full),s TEXT COLLATE coll_collision_simple DEFAULT 'ß',a coll_collision_domain[],g TEXT GENERATED ALWAYS AS (upper(s COLLATE coll_collision_full)) STORED,CHECK(v COLLATE coll_collision_full > ''))`, `CREATE INDEX coll_collision_ix ON coll_collision_t(v COLLATE coll_collision_full)`, `CREATE VIEW coll_collision_v AS SELECT id,upper(v COLLATE coll_collision_full) AS full,upper(s COLLATE coll_collision_simple) AS simple,g,a::TEXT AS item FROM coll_collision_t`)
		fixture.CollationExec(t, p, b, `CREATE VIEW coll_collision_array_v AS SELECT array_agg(s COLLATE coll_collision_full ORDER BY id) AS items FROM coll_collision_t`)
		fixture.CollationExec(t, p, b,
			`CREATE VIEW coll_collision_compare AS SELECT id,v COLLATE coll_collision_full='ß' AS same,v COLLATE coll_collision_full<'é' AS before FROM coll_collision_t`,
			`CREATE VIEW coll_collision_conflict AS SELECT id,v=s AS same,v COLLATE coll_collision_full=s AS resolved FROM coll_collision_t`)
		fixture.CollationExec(t, p, b,
			`CREATE TABLE coll_collision_range(id INT,k TEXT) PARTITION BY RANGE(k COLLATE coll_collision_full)`,
			`CREATE TABLE coll_collision_range_low PARTITION OF coll_collision_range FOR VALUES FROM('a') TO('m')`,
			`CREATE TABLE coll_collision_range_high PARTITION OF coll_collision_range FOR VALUES FROM('m') TO('z')`,
			`CREATE TABLE coll_collision_range_default PARTITION OF coll_collision_range DEFAULT`)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO coll_collision_range VALUES(1,'a'),(2,'m')`, 2))
		rangeInsert := fixture.Exec(`INSERT INTO coll_collision_range VALUES($1,$2)`, 1, int64(3), "z")
		rangeInsert.Prepared = true
		fixture.CollationStep(t, p, b, rangeInsert)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO coll_collision_range VALUES(4,'ß')`, 1))
		rangeValues := fixture.Select(`SELECT id,k FROM coll_collision_range ORDER BY id`, []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "m"}, {int64(3), "z"}, {int64(4), "ß"}})
		rangeLow := fixture.Select(`SELECT id,k FROM coll_collision_range_low ORDER BY id`, rangeValues.Columns, rangeValues.Types, [][]any{{int64(1), "a"}})
		rangeHigh := fixture.Select(`SELECT id,k FROM coll_collision_range_high ORDER BY id`, rangeValues.Columns, rangeValues.Types, [][]any{{int64(2), "m"}})
		rangeDefault := fixture.Select(`SELECT id,k FROM coll_collision_range_default ORDER BY id`, rangeValues.Columns, rangeValues.Types, [][]any{{int64(3), "z"}, {int64(4), "ß"}})
		for _, s := range []fixture.Step{rangeValues, rangeLow, rangeHigh, rangeDefault} {
			fixture.CollationRoutes(t, p, b, s)
		}
		rangeDependencySQL := `SELECT COUNT(*) FROM pg_catalog.pg_depend WHERE classid=1259 AND objid='coll_collision_range'::REGCLASS::OID AND refclassid=3456 AND refobjid=$1`
		fixture.CollationRoutes(t, p, b, fixture.Select(rangeDependencySQL, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, fixture.CollationOID(t, b.DB, "coll_collision_full")))
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO coll_collision_t(id,v,s,a) VALUES(1,'ß','ß','{ß}'::coll_collision_domain[])`, 1))
		values := fixture.Select(`SELECT id,"full",simple,g,item FROM coll_collision_v ORDER BY id`, []string{"id", "full", "simple", "g", "item"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "SS", "ß", "SS", "{ß}"}})
		properties := fixture.Select(`SELECT collname,collprovider,collencoding,collversion FROM pg_catalog.pg_collation WHERE collname LIKE 'coll_collision_%' ORDER BY collname`, []string{"collname", "collprovider", "collencoding", "collversion"}, []string{"NAME", "CHAR", "INT4", "TEXT"}, [][]any{{"coll_collision_anchor", "c", int64(-1), nil}, {"coll_collision_full", "b", int64(6), "1"}, {"coll_collision_simple", "b", int64(6), "1"}})
		aggregates := fixture.ArrayReferenceSelect(`SELECT items,upper(items[1]) AS folded FROM coll_collision_array_v`, []string{"items", "folded"}, []string{"_TEXT", "TEXT"}, []string{"_TEXT", "TEXT"}, [][]any{{"{ß}", "SS"}}, b.PG)
		comparisons := fixture.Select(`SELECT id,same,before FROM coll_collision_compare ORDER BY id`, []string{"id", "same", "before"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}})
		resolved := fixture.Select(`SELECT id,resolved FROM coll_collision_conflict ORDER BY id`, []string{"id", "resolved"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
		conflicting := exprCollationDemand(`SELECT same FROM coll_collision_conflict`)
		fixture.CollationRoutes(t, p, b, comparisons)
		fixture.CollationRoutes(t, p, b, resolved)
		fixture.CollationRoutes(t, p, b, conflicting)
		fixture.CollationRoutes(t, p, b, values)
		fixture.CollationRoutes(t, p, b, properties)
		fixture.CollationRoutes(t, p, b, aggregates)
		if b.PG {
			return
		}
		sourceOID := fixture.CollationOID(t, b.DB, "coll_collision_full")
		dependencySQL := `SELECT COUNT(*) FROM pg_catalog.pg_depend WHERE refclassid=3456 AND refobjid=$1`
		var sourceEdges int64
		if err := b.DB.QueryRowContext(p.Ctx, dependencySQL, sourceOID).Scan(&sourceEdges); err != nil || sourceEdges == 0 {
			t.Fatal("source lacks actual provider dependency edges", sourceEdges, err)
		}
		var stream bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
			t.Fatal(err)
		}
		fresh := fixture.UpdateReturningNativeDestination(t)
		fresh.SetMaxOpenConns(2)
		owner := fixture.SequenceCoreConn(t, p.Ctx, fresh)
		if _, err := owner.ExecContext(p.Ctx, `CREATE COLLATION pg_temp.collision_target_temp FROM pg_catalog."C"`); err != nil {
			t.Fatal(err)
		}
		targetOID := fixture.CollationOID(t, fresh, "collision_target_temp")
		if sourceOID != targetOID {
			t.Fatal("fixture did not produce an actual independent allocator collision", sourceOID, targetOID)
		}
		assertTarget := func() {
			t.Helper()
			if actual := fixture.CollationOID(t, fresh, "collision_target_temp"); actual != targetOID {
				t.Fatal("target TEMP identity changed", actual, targetOID)
			}
			var value string
			if err := owner.QueryRowContext(p.Ctx, `SELECT upper('é' COLLATE pg_temp.collision_target_temp)`).Scan(&value); err != nil || value != "é" {
				t.Fatal("target TEMP ASCII capability changed", value, err)
			}
		}
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal(err)
			}
			assertTarget()
			imported := fixture.CollationOID(t, fresh, "coll_collision_full")
			if imported == sourceOID || imported == targetOID {
				t.Fatal("import aliased live TEMP provider", imported, sourceOID, targetOID)
			}
			var targetEdges, importedEdges int64
			if err := fresh.QueryRowContext(p.Ctx, dependencySQL, targetOID).Scan(&targetEdges); err != nil || targetEdges != 0 {
				t.Fatal("import retargeted durable edges to TEMP capability", targetEdges, err)
			}
			if err := fresh.QueryRowContext(p.Ctx, dependencySQL, imported).Scan(&importedEdges); err != nil || importedEdges != sourceEdges {
				t.Fatal("import lost exact provider dependency edges", importedEdges, sourceEdges, err)
			}
			for _, provider := range []struct {
				oid  int64
				want int64
			}{{targetOID, 0}, {imported, 1}} {
				var edges int64
				if err := fresh.QueryRowContext(p.Ctx, rangeDependencySQL, provider.oid).Scan(&edges); err != nil || edges != provider.want {
					t.Fatal("partition key provider dependency", provider, edges, err)
				}
			}
			for _, s := range []fixture.Step{rangeValues, rangeLow, rangeHigh, rangeDefault} {
				rows, err := fresh.QueryContext(p.Ctx, s.Query)
				fixture.DeleteReferenceConsume(t, false, rows, err, s)
			}
			result, err := fresh.ExecContext(p.Ctx, `INSERT INTO coll_collision_range VALUES(5,'b')`)
			fixture.DeleteReferenceCount(t, result, err, 1)
			rangeStatement, err := fresh.PrepareContext(p.Ctx, rangeInsert.Query)
			if err != nil {
				t.Fatal(err)
			}
			result, err = rangeStatement.ExecContext(p.Ctx, int64(6), "n")
			fixture.DeleteReferenceCount(t, result, err, 1)
			if err := rangeStatement.Close(); err != nil {
				t.Fatal(err)
			}
			for _, s := range []fixture.Step{
				fixture.Select(rangeLow.Query, rangeLow.Columns, rangeLow.Types, [][]any{{int64(1), "a"}, {int64(5), "b"}}),
				fixture.Select(rangeHigh.Query, rangeHigh.Columns, rangeHigh.Types, [][]any{{int64(2), "m"}, {int64(6), "n"}}),
				rangeDefault,
			} {
				rows, err := fresh.QueryContext(p.Ctx, s.Query)
				fixture.DeleteReferenceConsume(t, false, rows, err, s)
			}
			for _, s := range []fixture.Step{values, properties, aggregates} {
				rows, err := fresh.QueryContext(p.Ctx, s.Query)
				fixture.DeleteReferenceConsume(t, false, rows, err, s)
			}
			statement, err := fresh.PrepareContext(p.Ctx, values.Query)
			if err != nil {
				t.Fatal(err)
			}
			rows, queryErr := statement.QueryContext(p.Ctx)
			fixture.DeleteReferenceConsume(t, false, rows, queryErr, values)
			if err = statement.Close(); err != nil {
				t.Fatal(err)
			}
			aggregateStatement, err := fresh.PrepareContext(p.Ctx, aggregates.Query)
			if err != nil {
				t.Fatal(err)
			}
			rows, queryErr = aggregateStatement.QueryContext(p.Ctx)
			fixture.DeleteReferenceConsume(t, false, rows, queryErr, aggregates)
			if err = aggregateStatement.Close(); err != nil {
				t.Fatal(err)
			}
			for _, s := range []fixture.Step{comparisons, resolved} {
				rows, err := fresh.QueryContext(p.Ctx, s.Query)
				fixture.DeleteReferenceConsume(t, false, rows, err, s)
				stmt, err := fresh.PrepareContext(p.Ctx, s.Query)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = stmt.QueryContext(p.Ctx)
				fixture.DeleteReferenceConsume(t, false, rows, err, s)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
			{
				rows, err := fresh.QueryContext(p.Ctx, conflicting.Query)
				if rows != nil {
					rows.Close()
				}
				fixture.AssertControlDiagnostic(t, err, *conflicting.Diagnostic)
				stmt, err := fresh.PrepareContext(p.Ctx, conflicting.Query)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = stmt.QueryContext(p.Ctx)
				if rows != nil {
					rows.Close()
				}
				fixture.AssertControlDiagnostic(t, err, *conflicting.Diagnostic)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
			result, err = fresh.ExecContext(p.Ctx, `INSERT INTO coll_collision_t(id,a) VALUES(2,'{é}'::coll_collision_domain[])`)
			fixture.DeleteReferenceCount(t, result, err, 1)
			mutation := fixture.Select(`SELECT id,"full",simple,g,item FROM coll_collision_v WHERE id=2`, values.Columns, values.Types, [][]any{{int64(2), "SS", "ß", "SS", "{é}"}})
			rows, queryErr = fresh.QueryContext(p.Ctx, mutation.Query)
			fixture.DeleteReferenceConsume(t, false, rows, queryErr, mutation)
			mutatedAggregate := aggregates
			mutatedAggregate.Rows = [][]any{{"{ß,ß}", "SS"}}
			rows, queryErr = fresh.QueryContext(p.Ctx, mutatedAggregate.Query)
			fixture.DeleteReferenceConsume(t, false, rows, queryErr, mutatedAggregate)
		}
		if actual := fixture.CollationOID(t, b.DB, "coll_collision_full"); actual != sourceOID {
			t.Fatal("import mutated source catalog", actual, sourceOID)
		}
		fixture.CollationRoutes(t, p, b, aggregates)
		fixture.CollationRoutes(t, p, b, comparisons)
		fixture.CollationRoutes(t, p, b, resolved)
		for _, s := range []fixture.Step{rangeValues, rangeLow, rangeHigh, rangeDefault} {
			fixture.CollationRoutes(t, p, b, s)
		}
		var sourceAgain bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &sourceAgain); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stream.Bytes(), sourceAgain.Bytes()) {
			t.Fatal("collision replacement mutated source persistent descriptors/data")
		}
		var portable bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, fresh, &portable); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(portable.String(), "collision_target_temp") || strings.Contains(portable.String(), "pg_temp_") {
			t.Fatal("portable import dump leaked target TEMP", portable.String())
		}
		if p.Postgres != nil {
			conn := fixture.SequenceCoreConn(t, p.Ctx, p.Postgres)
			if _, err := conn.ExecContext(p.Ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(p.Ctx, `ROLLBACK`)
			if _, err := conn.ExecContext(p.Ctx, portable.String()); err != nil {
				t.Fatal("real PostgreSQL remapped-provider replay", err)
			}
			replay := fixture.Select(values.Query, values.Columns, values.Types, [][]any{{int64(1), "SS", "ß", "SS", "{ß}"}, {int64(2), "SS", "ß", "SS", "{é}"}})
			fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, replay)
			fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, properties)
			replayedAggregate := fixture.ArrayReferenceSelect(aggregates.Query, aggregates.Columns, aggregates.Types, aggregates.RawTypes, [][]any{{"{ß,ß}", "SS"}}, true)
			fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, replayedAggregate)
			for _, s := range []fixture.Step{
				fixture.Select(comparisons.Query, comparisons.Columns, comparisons.Types, [][]any{{int64(1), true, true}, {int64(2), true, true}}),
				fixture.Select(resolved.Query, resolved.Columns, resolved.Types, [][]any{{int64(1), true}, {int64(2), true}}),
			} {
				fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, s)
				s.Prepared = true
				fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, s)
			}
			for _, s := range []fixture.Step{
				fixture.Select(rangeValues.Query, rangeValues.Columns, rangeValues.Types, [][]any{{int64(1), "a"}, {int64(2), "m"}, {int64(3), "z"}, {int64(4), "ß"}, {int64(5), "b"}, {int64(6), "n"}}),
				fixture.Select(rangeLow.Query, rangeLow.Columns, rangeLow.Types, [][]any{{int64(1), "a"}, {int64(5), "b"}}),
				fixture.Select(rangeHigh.Query, rangeHigh.Columns, rangeHigh.Types, [][]any{{int64(2), "m"}, {int64(6), "n"}}),
				rangeDefault,
			} {
				fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, s)
				s.Prepared = true
				fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, s)
			}
			replayInsert := fixture.Exec(rangeInsert.Query, 1, int64(7), "c")
			fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, replayInsert)
			replayInsert.Prepared, replayInsert.Args = true, []any{int64(8), "o"}
			fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, replayInsert)
			for _, s := range []fixture.Step{
				fixture.Select(rangeLow.Query, rangeLow.Columns, rangeLow.Types, [][]any{{int64(1), "a"}, {int64(5), "b"}, {int64(7), "c"}}),
				fixture.Select(rangeHigh.Query, rangeHigh.Columns, rangeHigh.Types, [][]any{{int64(2), "m"}, {int64(6), "n"}, {int64(8), "o"}}),
				rangeDefault,
			} {
				fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, s)
			}
			// Each demanded PostgreSQL error aborts its subtransaction. Recover
			// before preparing/executing another route, retaining the replay
			// transaction's rollback-only lifecycle and every existing witness.
			for _, prepared := range []bool{false, true} {
				if _, err := conn.ExecContext(p.Ctx, `SAVEPOINT coll_collision_demand`); err != nil {
					t.Fatal(err)
				}
				demand := conflicting
				demand.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, demand)
				if _, err := conn.ExecContext(p.Ctx, `ROLLBACK TO SAVEPOINT coll_collision_demand`); err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(p.Ctx, `RELEASE SAVEPOINT coll_collision_demand`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := conn.ExecContext(p.Ctx, `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
		}
	})
}
