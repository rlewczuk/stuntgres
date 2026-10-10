package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCollationCompositeNativeCollision(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		collationExec(t, p, b,
			`CREATE COLLATION coll_composite_anchor FROM pg_catalog."C"`,
			`CREATE COLLATION coll_composite_full FROM pg_catalog.pg_unicode_fast`,
			`CREATE COLLATION coll_composite_simple FROM pg_catalog.pg_c_utf8`,
			`CREATE TYPE coll_composite_row AS (expanded TEXT COLLATE coll_composite_full,compat TEXT COLLATE coll_composite_simple)`,
			`CREATE DOMAIN coll_composite_domain AS coll_composite_row`,
			`CREATE TABLE coll_composite_holder(id INT,v coll_composite_domain DEFAULT '(ß,ß)'::coll_composite_row,a coll_composite_row)`,
			`CREATE VIEW coll_composite_saved AS SELECT id,v::text AS value,a::text AS items FROM coll_composite_holder`,
		)
		collationStep(t, p, b, referenceExec(`INSERT INTO coll_composite_holder(id,a) VALUES(1,'(é,é)'::coll_composite_row)`, 1))
		values := referenceSelect(`SELECT id,value,items FROM coll_composite_saved ORDER BY id`, []string{"id", "value", "items"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "(ß,ß)", "(é,é)"}})
		collationRoutes(t, p, b, values)
		if b.pg {
			return
		}
		sourceOID := collationOID(t, b.db, "coll_composite_full")
		var stream bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &stream); err != nil {
			t.Fatal("composite registered-provider Dump", err)
		}
		fresh := updateReturningNativeDestination(t)
		fresh.SetMaxOpenConns(2)
		owner := sequenceCoreConn(t, p.ctx, fresh)
		if _, err := owner.ExecContext(p.ctx, `CREATE COLLATION pg_temp.coll_composite_target FROM pg_catalog."C"`); err != nil {
			t.Fatal(err)
		}
		targetOID := collationOID(t, fresh, "coll_composite_target")
		if targetOID != sourceOID {
			t.Fatal("missing genuine provider allocator collision", targetOID, sourceOID)
		}
		for range 2 {
			if err := stuntgres.Restore(p.ctx, fresh, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal("composite capability collision Restore", err)
			}
			imported := collationOID(t, fresh, "coll_composite_full")
			if imported == targetOID || collationOID(t, fresh, "coll_composite_target") != targetOID {
				t.Fatal("import aliased or replaced live TEMP identity", imported, targetOID)
			}
			var ascii string
			if err := owner.QueryRowContext(p.ctx, `SELECT upper('é' COLLATE pg_temp.coll_composite_target)`).Scan(&ascii); err != nil || ascii != "é" {
				t.Fatal("target TEMP capability changed", ascii, err)
			}
			rows, err := fresh.QueryContext(p.ctx, values.query)
			deleteReferenceConsume(t, false, rows, err, values)
			result, err := fresh.ExecContext(p.ctx, `INSERT INTO coll_composite_holder(id,a) VALUES(2,'(ß,é)'::coll_composite_row)`)
			deleteReferenceCount(t, result, err, 1)
			mutation := referenceSelect(`SELECT id,value,items FROM coll_composite_saved WHERE id=2`, values.columns, values.types, [][]any{{int64(2), "(ß,ß)", "(ß,é)"}})
			rows, err = fresh.QueryContext(p.ctx, mutation.query)
			deleteReferenceConsume(t, false, rows, err, mutation)
		}
		if collationOID(t, b.db, "coll_composite_full") != sourceOID {
			t.Fatal("Restore mutated source provider identity")
		}
		collationRoutes(t, p, b, values)
		var portable bytes.Buffer
		if err := stuntgres.Dump(p.ctx, fresh, &portable); err != nil {
			t.Fatal("remapped composite provider redump", err)
		}
		if strings.Contains(portable.String(), "coll_composite_target") || strings.Contains(portable.String(), "pg_temp_") {
			t.Fatal("portable composite Dump leaked TEMP authority", portable.String())
		}
		if p.postgres != nil {
			conn := sequenceCoreConn(t, p.ctx, p.postgres)
			if _, err := conn.ExecContext(p.ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(p.ctx, `ROLLBACK`)
			if _, err := conn.ExecContext(p.ctx, portable.String()); err != nil {
				t.Fatal("actual PostgreSQL composite-provider replay", err)
			}
			replay := referenceSelect(values.query, values.columns, values.types, [][]any{{int64(1), "(ß,ß)", "(é,é)"}, {int64(2), "(ß,ß)", "(ß,é)"}})
			referenceRunStep(t, p.ctx, conn, p.postgresRecord, replay)
		}
	})
}
