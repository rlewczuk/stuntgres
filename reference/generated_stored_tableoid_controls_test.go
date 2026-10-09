package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceGeneratedStoredTableOIDControls(t *testing.T) {
	var nativeDump string
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		// The live TEMP sequence reserves the first user relation identity. Restore's
		// private first persistent relation therefore collides on native replacement.
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.ctx, `CREATE TEMP SEQUENCE retained`); err != nil {
			t.Fatal(err)
		}
		// The private replay allocates this schema before oidg, aligning oidg's
		// relation identity with the retained TEMP sequence, not its namespace.
		run(referenceExec(`CREATE SCHEMA oid_marker`, 0))
		run(referenceExec(`CREATE TABLE oidg(a INT,b OID GENERATED ALWAYS AS(tableoid) STORED,c BIGINT GENERATED ALWAYS AS(CASE WHEN a IS NULL THEN tableoid::bigint ELSE tableoid::bigint+a END) STORED,CHECK(current_user=current_user),CHECK(random()>=0 AND random()<1),CHECK(obj_description(b,'pg_class') IS NULL),CHECK(pg_table_is_visible(b)))`, 0))
		run(referenceExec(`INSERT INTO oidg(a) VALUES(2)`, 1))
		heldInsert, err := backend.db.PrepareContext(p.ctx, `INSERT INTO oidg(a) VALUES($1)`)
		if err != nil {
			t.Fatal(err)
		}
		defer heldInsert.Close()
		var before int64
		if err := backend.db.QueryRowContext(p.ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		var b, c int64
		if err := backend.db.QueryRowContext(p.ctx, `SELECT b,c FROM oidg`).Scan(&b, &c); err != nil || b != before || c != before+2 {
			t.Fatal(b, c, before, err)
		}
		if !backend.pg {
			held, err := p.native.QueryContext(p.ctx, `SELECT b,c FROM oidg`)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			nativeDump = dump.String()
			if strings.Contains(nativeDump, "retained") {
				t.Fatal("TEMP sequence serialized", nativeDump)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(nativeDump)); err != nil {
				t.Fatal(err)
			}
			if !held.Next() {
				t.Fatal(held.Err())
			}
			var oldB, oldC int64
			if err := held.Scan(&oldB, &oldC); err != nil || oldB != before || oldC != before+2 {
				t.Fatal(oldB, oldC, before, err)
			}
			if held.Next() || held.Err() != nil {
				t.Fatal("held rows changed", held.Err())
			}
		} else if nativeDump != "" {
			run(referenceExec(`DROP TABLE oidg`, 0))
			run(referenceExec(`DROP SCHEMA oid_marker`, 0))
			if _, err := backend.db.ExecContext(p.ctx, nativeDump); err != nil {
				t.Fatal(err)
			}
		}
		var owner int64
		if err := backend.db.QueryRowContext(p.ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&owner); err != nil || owner <= 0 {
			t.Fatal(owner, err)
		}
		if !backend.pg && owner == before {
			t.Fatal("collision did not remap relation", owner)
		}
		if err := backend.db.QueryRowContext(p.ctx, `SELECT b,c FROM oidg`).Scan(&b, &c); err != nil || b != owner || c != owner+2 {
			t.Fatal("remapped stored cells", b, c, owner, err)
		}
		result, err := heldInsert.ExecContext(p.ctx, 4)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		run(referenceExec(`UPDATE oidg SET a=8 WHERE a=2`, 1))
		if err := backend.db.QueryRowContext(p.ctx, `SELECT b,c FROM oidg WHERE a=8`).Scan(&b, &c); err != nil || b != owner || c != owner+8 {
			t.Fatal(b, c, owner, err)
		}
		if err := backend.db.QueryRowContext(p.ctx, `SELECT b,c FROM oidg WHERE a=4`).Scan(&b, &c); err != nil || b != owner || c != owner+4 {
			t.Fatal(b, c, owner, err)
		}
	})
}

// The SQL phase independently exercises the sequence CHECK. The native control
// phase forces an additional CHECK during table-owner repair, after replay has
// succeeded, and must discard that private sequence effect with the candidate.
func TestReferenceGeneratedStoredTableOIDRepairAtomicity(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		const script = `CREATE SCHEMA repair_scope; CREATE TABLE repair_scope.repair(id SERIAL,b OID GENERATED ALWAYS AS(tableoid) STORED,CONSTRAINT repair_check CHECK(nextval('repair_scope.repair_id_seq')<=2)); INSERT INTO repair_scope.repair(id) VALUES(7); SELECT setval('repair_scope.repair_id_seq',2,true);`
		if backend.pg {
			if _, err := backend.db.ExecContext(p.ctx, script); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect(`SELECT last_value FROM repair_scope.repair_id_seq`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(referenceSelect(`SELECT id FROM repair_scope.repair`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			return
		}
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, q := range []string{`CREATE TEMP SEQUENCE retained_one`, `CREATE TEMP SEQUENCE retained_two`} {
			if _, err := conn.ExecContext(p.ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		run(referenceExec(`CREATE TABLE sentinel(a INT)`, 0))
		run(referenceExec(`INSERT INTO sentinel VALUES(99)`, 1))
		held, err := backend.db.QueryContext(p.ctx, `SELECT a FROM sentinel`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		prepared, err := backend.db.PrepareContext(p.ctx, `SELECT a FROM sentinel`)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		var before bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
			t.Fatal(err)
		}
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(script))
		referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "repair" violates check constraint "repair_check"`, Schema: "repair_scope", Table: "repair", Constraint: "repair_check"})
		var after bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
			t.Fatal(err)
		}
		if before.String() != after.String() {
			t.Fatal("failed repair changed destination")
		}
		run(referenceSelect(`SELECT a FROM sentinel`, []string{"a"}, []string{"INT4"}, [][]any{{int64(99)}}))
		var value int64
		if err := prepared.QueryRowContext(p.ctx).Scan(&value); err != nil || value != 99 {
			t.Fatal(value, err)
		}
		if !held.Next() {
			t.Fatal(held.Err())
		}
		if err := held.Scan(&value); err != nil || value != 99 {
			t.Fatal(value, err)
		}
		if held.Next() || held.Err() != nil {
			t.Fatal("held snapshot changed", held.Err())
		}
		for _, q := range []string{`SELECT nextval('retained_one')`, `SELECT nextval('retained_two')`} {
			if err := conn.QueryRowContext(p.ctx, q).Scan(&value); err != nil || value != 1 {
				t.Fatal(value, err)
			}
		}
		// Replay leaves the detached generator at 2; the successful repair must
		// publish its real CHECK effect (3), without touching the TEMP generators.
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(strings.Replace(script, "<=2", "<=3", 1))); err != nil {
			t.Fatal(err)
		}
		run(referenceSelect(`SELECT id FROM repair_scope.repair`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceSelect(`SELECT last_value FROM repair_scope.repair_id_seq`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}

func TestReferenceGeneratedStoredTableOIDOwnerAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"generated_restore_owner"})
	uniqueReferenceSchemaCleanup(t, "generated_owned")
	generatedStoredScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, run func(referenceStep)) {
		run(referenceExec(`CREATE ROLE generated_restore_owner`, 0))
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		const script = `CREATE SCHEMA generated_owned AUTHORIZATION generated_restore_owner; SET SESSION AUTHORIZATION generated_restore_owner; CREATE TABLE generated_owned.owned_generated(a INT,b OID GENERATED ALWAYS AS(tableoid) STORED,CHECK(current_user='generated_restore_owner'),CHECK(session_user='generated_restore_owner')); INSERT INTO generated_owned.owned_generated(a) VALUES(7);`
		if backend.pg {
			defer conn.ExecContext(p.ctx, `RESET SESSION AUTHORIZATION`)
			if _, err := conn.ExecContext(p.ctx, script); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := conn.ExecContext(p.ctx, `CREATE TEMP SEQUENCE owner_collision`); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
		}
		run(referenceSelect(`SELECT a FROM generated_owned.owned_generated`, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		var owner, cell int64
		if err := backend.db.QueryRowContext(p.ctx, `SELECT oid FROM pg_class WHERE relname='owned_generated'`).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		if err := backend.db.QueryRowContext(p.ctx, `SELECT b FROM generated_owned.owned_generated`).Scan(&cell); err != nil || cell != owner {
			t.Fatal(owner, cell, err)
		}
	})
}
