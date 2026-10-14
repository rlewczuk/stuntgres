package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationCompositeNativeCollision(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if !b.PG {
			// Match the target's second real TEMP namespace allocation.
			fixture.CollationExec(t, p, b, `CREATE COLLATION namespace_composite_padding FROM pg_catalog."C"`)
		}
		fixture.CollationExec(t, p, b,
			`CREATE COLLATION coll_composite_anchor FROM pg_catalog."C"`,
			`CREATE COLLATION coll_composite_full FROM pg_catalog.pg_unicode_fast`,
			`CREATE COLLATION coll_composite_simple FROM pg_catalog.pg_c_utf8`,
			`CREATE TYPE coll_composite_row AS (expanded TEXT COLLATE coll_composite_full,compat TEXT COLLATE coll_composite_simple)`,
			`CREATE DOMAIN coll_composite_domain AS coll_composite_row`,
			`CREATE TABLE coll_composite_holder(id INT,v coll_composite_domain DEFAULT '(ß,ß)'::coll_composite_row,a coll_composite_row)`,
			`CREATE VIEW coll_composite_saved AS SELECT id,v::text AS value,a::text AS items FROM coll_composite_holder`,
		)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO coll_composite_holder(id,a) VALUES(1,'(é,é)'::coll_composite_row)`, 1))
		values := fixture.Select(`SELECT id,value,items FROM coll_composite_saved ORDER BY id`, []string{"id", "value", "items"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "(ß,ß)", "(é,é)"}})
		fixture.CollationRoutes(t, p, b, values)
		if b.PG {
			return
		}
		sourceOID := fixture.CollationOID(t, b.DB, "coll_composite_full")
		var stream bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
			t.Fatal("composite registered-provider Dump", err)
		}
		fresh := fixture.UpdateReturningNativeDestination(t)
		fresh.SetMaxOpenConns(2)
		owner := fixture.SequenceCoreConn(t, p.Ctx, fresh)
		if _, err := owner.ExecContext(p.Ctx, `CREATE COLLATION pg_temp.coll_composite_target FROM pg_catalog."C"`); err != nil {
			t.Fatal(err)
		}
		targetOID := fixture.CollationOID(t, fresh, "coll_composite_target")
		if targetOID != sourceOID {
			t.Fatal("missing genuine provider allocator collision", targetOID, sourceOID)
		}
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal("composite capability collision Restore", err)
			}
			imported := fixture.CollationOID(t, fresh, "coll_composite_full")
			if imported == targetOID || fixture.CollationOID(t, fresh, "coll_composite_target") != targetOID {
				t.Fatal("import aliased or replaced live TEMP identity", imported, targetOID)
			}
			var ascii string
			if err := owner.QueryRowContext(p.Ctx, `SELECT upper('é' COLLATE pg_temp.coll_composite_target)`).Scan(&ascii); err != nil || ascii != "é" {
				t.Fatal("target TEMP capability changed", ascii, err)
			}
			rows, err := fresh.QueryContext(p.Ctx, values.Query)
			fixture.DeleteReferenceConsume(t, false, rows, err, values)
			result, err := fresh.ExecContext(p.Ctx, `INSERT INTO coll_composite_holder(id,a) VALUES(2,'(ß,é)'::coll_composite_row)`)
			fixture.DeleteReferenceCount(t, result, err, 1)
			mutation := fixture.Select(`SELECT id,value,items FROM coll_composite_saved WHERE id=2`, values.Columns, values.Types, [][]any{{int64(2), "(ß,ß)", "(ß,é)"}})
			rows, err = fresh.QueryContext(p.Ctx, mutation.Query)
			fixture.DeleteReferenceConsume(t, false, rows, err, mutation)
		}
		if fixture.CollationOID(t, b.DB, "coll_composite_full") != sourceOID {
			t.Fatal("Restore mutated source provider identity")
		}
		fixture.CollationRoutes(t, p, b, values)
		var portable bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, fresh, &portable); err != nil {
			t.Fatal("remapped composite provider redump", err)
		}
		if strings.Contains(portable.String(), "coll_composite_target") || strings.Contains(portable.String(), "pg_temp_") {
			t.Fatal("portable composite Dump leaked TEMP authority", portable.String())
		}
		if p.Postgres != nil {
			conn := fixture.SequenceCoreConn(t, p.Ctx, p.Postgres)
			if _, err := conn.ExecContext(p.Ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(p.Ctx, `ROLLBACK`)
			if _, err := conn.ExecContext(p.Ctx, portable.String()); err != nil {
				t.Fatal("actual PostgreSQL composite-provider replay", err)
			}
			replay := fixture.Select(values.Query, values.Columns, values.Types, [][]any{{int64(1), "(ß,ß)", "(é,é)"}, {int64(2), "(ß,ß)", "(ß,é)"}})
			fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, replay)
		}
	})
}
