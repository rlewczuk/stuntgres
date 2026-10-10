package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCollationNativeCollision(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		collationExec(t, p, b, `CREATE COLLATION coll_collision_anchor FROM pg_catalog."C"`, `CREATE COLLATION coll_collision_full FROM pg_catalog.pg_unicode_fast`, `CREATE COLLATION coll_collision_simple (PROVIDER=builtin,LOCALE='C.UTF-8')`, `CREATE DOMAIN coll_collision_domain AS TEXT COLLATE coll_collision_full`, `CREATE TABLE coll_collision_t(id INT,v coll_collision_domain DEFAULT ('ß' COLLATE coll_collision_full),s TEXT COLLATE coll_collision_simple DEFAULT 'ß',a coll_collision_domain[],g TEXT GENERATED ALWAYS AS (upper(s COLLATE coll_collision_full)) STORED,CHECK(v COLLATE coll_collision_full > ''))`, `CREATE INDEX coll_collision_ix ON coll_collision_t(v COLLATE coll_collision_full)`, `CREATE VIEW coll_collision_v AS SELECT id,upper(v COLLATE coll_collision_full) AS full,upper(s COLLATE coll_collision_simple) AS simple,g,a::TEXT AS item FROM coll_collision_t`)
		collationExec(t, p, b, `CREATE VIEW coll_collision_array_v AS SELECT array_agg(s COLLATE coll_collision_full ORDER BY id) AS items FROM coll_collision_t`)
		collationStep(t, p, b, referenceExec(`INSERT INTO coll_collision_t(id,v,s,a) VALUES(1,'ß','ß','{ß}'::coll_collision_domain[])`, 1))
		values := referenceSelect(`SELECT id,"full",simple,g,item FROM coll_collision_v ORDER BY id`, []string{"id", "full", "simple", "g", "item"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "SS", "ß", "SS", "{ß}"}})
		properties := referenceSelect(`SELECT collname,collprovider,collencoding,collversion FROM pg_catalog.pg_collation WHERE collname LIKE 'coll_collision_%' ORDER BY collname`, []string{"collname", "collprovider", "collencoding", "collversion"}, []string{"NAME", "CHAR", "INT4", "TEXT"}, [][]any{{"coll_collision_anchor", "c", int64(-1), nil}, {"coll_collision_full", "b", int64(6), "1"}, {"coll_collision_simple", "b", int64(6), "1"}})
		aggregates := arrayReferenceSelect(`SELECT items,upper(items[1]) AS folded FROM coll_collision_array_v`, []string{"items", "folded"}, []string{"_TEXT", "TEXT"}, []string{"_TEXT", "TEXT"}, [][]any{{"{ß}", "SS"}}, b.pg)
		collationRoutes(t, p, b, values)
		collationRoutes(t, p, b, properties)
		collationRoutes(t, p, b, aggregates)
		if b.pg {
			return
		}
		sourceOID := collationOID(t, b.db, "coll_collision_full")
		dependencySQL := `SELECT COUNT(*) FROM pg_catalog.pg_depend WHERE refclassid=3456 AND refobjid=$1`
		var sourceEdges int64
		if err := b.db.QueryRowContext(p.ctx, dependencySQL, sourceOID).Scan(&sourceEdges); err != nil || sourceEdges == 0 {
			t.Fatal("source lacks actual provider dependency edges", sourceEdges, err)
		}
		var stream bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &stream); err != nil {
			t.Fatal(err)
		}
		fresh := updateReturningNativeDestination(t)
		fresh.SetMaxOpenConns(2)
		owner := sequenceCoreConn(t, p.ctx, fresh)
		if _, err := owner.ExecContext(p.ctx, `CREATE COLLATION pg_temp.collision_target_temp FROM pg_catalog."C"`); err != nil {
			t.Fatal(err)
		}
		targetOID := collationOID(t, fresh, "collision_target_temp")
		if sourceOID != targetOID {
			t.Fatal("fixture did not produce an actual independent allocator collision", sourceOID, targetOID)
		}
		assertTarget := func() {
			t.Helper()
			if actual := collationOID(t, fresh, "collision_target_temp"); actual != targetOID {
				t.Fatal("target TEMP identity changed", actual, targetOID)
			}
			var value string
			if err := owner.QueryRowContext(p.ctx, `SELECT upper('é' COLLATE pg_temp.collision_target_temp)`).Scan(&value); err != nil || value != "é" {
				t.Fatal("target TEMP ASCII capability changed", value, err)
			}
		}
		for range 2 {
			if err := stuntgres.Restore(p.ctx, fresh, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal(err)
			}
			assertTarget()
			imported := collationOID(t, fresh, "coll_collision_full")
			if imported == sourceOID || imported == targetOID {
				t.Fatal("import aliased live TEMP provider", imported, sourceOID, targetOID)
			}
			var targetEdges, importedEdges int64
			if err := fresh.QueryRowContext(p.ctx, dependencySQL, targetOID).Scan(&targetEdges); err != nil || targetEdges != 0 {
				t.Fatal("import retargeted durable edges to TEMP capability", targetEdges, err)
			}
			if err := fresh.QueryRowContext(p.ctx, dependencySQL, imported).Scan(&importedEdges); err != nil || importedEdges != sourceEdges {
				t.Fatal("import lost exact provider dependency edges", importedEdges, sourceEdges, err)
			}
			for _, s := range []referenceStep{values, properties, aggregates} {
				rows, err := fresh.QueryContext(p.ctx, s.query)
				deleteReferenceConsume(t, false, rows, err, s)
			}
			statement, err := fresh.PrepareContext(p.ctx, values.query)
			if err != nil {
				t.Fatal(err)
			}
			rows, queryErr := statement.QueryContext(p.ctx)
			deleteReferenceConsume(t, false, rows, queryErr, values)
			if err = statement.Close(); err != nil {
				t.Fatal(err)
			}
			aggregateStatement, err := fresh.PrepareContext(p.ctx, aggregates.query)
			if err != nil {
				t.Fatal(err)
			}
			rows, queryErr = aggregateStatement.QueryContext(p.ctx)
			deleteReferenceConsume(t, false, rows, queryErr, aggregates)
			if err = aggregateStatement.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := fresh.ExecContext(p.ctx, `INSERT INTO coll_collision_t(id,a) VALUES(2,'{é}'::coll_collision_domain[])`)
			deleteReferenceCount(t, result, err, 1)
			mutation := referenceSelect(`SELECT id,"full",simple,g,item FROM coll_collision_v WHERE id=2`, values.columns, values.types, [][]any{{int64(2), "SS", "ß", "SS", "{é}"}})
			rows, queryErr = fresh.QueryContext(p.ctx, mutation.query)
			deleteReferenceConsume(t, false, rows, queryErr, mutation)
			mutatedAggregate := aggregates
			mutatedAggregate.rows = [][]any{{"{ß,ß}", "SS"}}
			rows, queryErr = fresh.QueryContext(p.ctx, mutatedAggregate.query)
			deleteReferenceConsume(t, false, rows, queryErr, mutatedAggregate)
		}
		if actual := collationOID(t, b.db, "coll_collision_full"); actual != sourceOID {
			t.Fatal("import mutated source catalog", actual, sourceOID)
		}
		collationRoutes(t, p, b, aggregates)
		var portable bytes.Buffer
		if err := stuntgres.Dump(p.ctx, fresh, &portable); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(portable.String(), "collision_target_temp") || strings.Contains(portable.String(), "pg_temp_") {
			t.Fatal("portable import dump leaked target TEMP", portable.String())
		}
		if p.postgres != nil {
			conn := sequenceCoreConn(t, p.ctx, p.postgres)
			if _, err := conn.ExecContext(p.ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(p.ctx, `ROLLBACK`)
			if _, err := conn.ExecContext(p.ctx, portable.String()); err != nil {
				t.Fatal("real PostgreSQL remapped-provider replay", err)
			}
			replay := referenceSelect(values.query, values.columns, values.types, [][]any{{int64(1), "SS", "ß", "SS", "{ß}"}, {int64(2), "SS", "ß", "SS", "{é}"}})
			referenceRunStep(t, p.ctx, conn, p.postgresRecord, replay)
			referenceRunStep(t, p.ctx, conn, p.postgresRecord, properties)
			replayedAggregate := arrayReferenceSelect(aggregates.query, aggregates.columns, aggregates.types, aggregates.rawTypes, [][]any{{"{ß,ß}", "SS"}}, true)
			referenceRunStep(t, p.ctx, conn, p.postgresRecord, replayedAggregate)
			if _, err := conn.ExecContext(p.ctx, `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
		}
	})
}
