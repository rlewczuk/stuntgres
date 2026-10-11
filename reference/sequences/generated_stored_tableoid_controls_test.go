package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedStoredTableOIDControls(t *testing.T) {
	var nativeDump string
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		// The live TEMP sequence reserves the first user relation identity. Restore's
		// private first persistent relation therefore collides on native replacement.
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.Ctx, `CREATE TEMP SEQUENCE retained`); err != nil {
			t.Fatal(err)
		}
		// The private replay allocates this schema before oidg, aligning oidg's
		// relation identity with the retained TEMP sequence, not its namespace.
		run(fixture.Exec(`CREATE SCHEMA oid_marker`, 0))
		run(fixture.Exec(`CREATE TABLE oidg(a INT,b OID GENERATED ALWAYS AS(tableoid) STORED,c BIGINT GENERATED ALWAYS AS(CASE WHEN a IS NULL THEN tableoid::bigint ELSE tableoid::bigint+a END) STORED,CHECK(current_user=current_user),CHECK(random()>=0 AND random()<1),CHECK(obj_description(b,'pg_class') IS NULL),CHECK(pg_table_is_visible(b)))`, 0))
		run(fixture.Exec(`INSERT INTO oidg(a) VALUES(2)`, 1))
		heldInsert, err := backend.DB.PrepareContext(p.Ctx, `INSERT INTO oidg(a) VALUES($1)`)
		if err != nil {
			t.Fatal(err)
		}
		defer heldInsert.Close()
		var before int64
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		var b, c int64
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT b,c FROM oidg`).Scan(&b, &c); err != nil || b != before || c != before+2 {
			t.Fatal(b, c, before, err)
		}
		if !backend.PG {
			held, err := p.Native.QueryContext(p.Ctx, `SELECT b,c FROM oidg`)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			nativeDump = dump.String()
			if strings.Contains(nativeDump, "retained") {
				t.Fatal("TEMP sequence serialized", nativeDump)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(nativeDump)); err != nil {
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
			run(fixture.Exec(`DROP TABLE oidg`, 0))
			run(fixture.Exec(`DROP SCHEMA oid_marker`, 0))
			if _, err := backend.DB.ExecContext(p.Ctx, nativeDump); err != nil {
				t.Fatal(err)
			}
		}
		var owner int64
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='oidg'`).Scan(&owner); err != nil || owner <= 0 {
			t.Fatal(owner, err)
		}
		if !backend.PG && owner == before {
			t.Fatal("collision did not remap relation", owner)
		}
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT b,c FROM oidg`).Scan(&b, &c); err != nil || b != owner || c != owner+2 {
			t.Fatal("remapped stored cells", b, c, owner, err)
		}
		result, err := heldInsert.ExecContext(p.Ctx, 4)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		run(fixture.Exec(`UPDATE oidg SET a=8 WHERE a=2`, 1))
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT b,c FROM oidg WHERE a=8`).Scan(&b, &c); err != nil || b != owner || c != owner+8 {
			t.Fatal(b, c, owner, err)
		}
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT b,c FROM oidg WHERE a=4`).Scan(&b, &c); err != nil || b != owner || c != owner+4 {
			t.Fatal(b, c, owner, err)
		}
	})
}

// The SQL phase independently exercises the sequence CHECK. The native control
// phase forces an additional CHECK during table-owner repair, after replay has
// succeeded, and must discard that private sequence effect with the candidate.
func TestReferenceGeneratedStoredTableOIDRepairAtomicity(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		const script = `CREATE SCHEMA repair_scope; CREATE TABLE repair_scope.repair(id SERIAL,b OID GENERATED ALWAYS AS(tableoid) STORED,CONSTRAINT repair_check CHECK(nextval('repair_scope.repair_id_seq')<=2)); INSERT INTO repair_scope.repair(id) VALUES(7); SELECT setval('repair_scope.repair_id_seq',2,true);`
		if backend.PG {
			if _, err := backend.DB.ExecContext(p.Ctx, script); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select(`SELECT last_value FROM repair_scope.repair_id_seq`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Select(`SELECT id FROM repair_scope.repair`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			return
		}
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, q := range []string{`CREATE TEMP SEQUENCE retained_one`, `CREATE TEMP SEQUENCE retained_two`} {
			if _, err := conn.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec(`CREATE TABLE sentinel(a INT)`, 0))
		run(fixture.Exec(`INSERT INTO sentinel VALUES(99)`, 1))
		held, err := backend.DB.QueryContext(p.Ctx, `SELECT a FROM sentinel`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		prepared, err := backend.DB.PrepareContext(p.Ctx, `SELECT a FROM sentinel`)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "repair" violates check constraint "repair_check"`, Schema: "repair_scope", Table: "repair", Constraint: "repair_check"})
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if before.String() != after.String() {
			t.Fatal("failed repair changed destination")
		}
		run(fixture.Select(`SELECT a FROM sentinel`, []string{"a"}, []string{"INT4"}, [][]any{{int64(99)}}))
		var value int64
		if err := prepared.QueryRowContext(p.Ctx).Scan(&value); err != nil || value != 99 {
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
			if err := conn.QueryRowContext(p.Ctx, q).Scan(&value); err != nil || value != 1 {
				t.Fatal(value, err)
			}
		}
		// Replay leaves the detached generator at 2; the successful repair must
		// publish its real CHECK effect (3), without touching the TEMP generators.
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(strings.Replace(script, "<=2", "<=3", 1))); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select(`SELECT id FROM repair_scope.repair`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Select(`SELECT last_value FROM repair_scope.repair_id_seq`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}

func TestReferenceGeneratedStoredTableOIDOwnerAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"generated_restore_owner"})
	fixture.UniqueReferenceSchemaCleanup(t, "generated_owned")
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE ROLE generated_restore_owner`, 0))
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		const script = `CREATE SCHEMA generated_owned AUTHORIZATION generated_restore_owner; SET SESSION AUTHORIZATION generated_restore_owner; CREATE TABLE generated_owned.owned_generated(a INT,b OID GENERATED ALWAYS AS(tableoid) STORED,CHECK(current_user='generated_restore_owner'),CHECK(session_user='generated_restore_owner')); INSERT INTO generated_owned.owned_generated(a) VALUES(7);`
		if backend.PG {
			defer conn.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`)
			if _, err := conn.ExecContext(p.Ctx, script); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := conn.ExecContext(p.Ctx, `CREATE TEMP SEQUENCE owner_collision`); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Select(`SELECT a FROM generated_owned.owned_generated`, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		var owner, cell int64
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_class WHERE relname='owned_generated'`).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		if err := backend.DB.QueryRowContext(p.Ctx, `SELECT b FROM generated_owned.owned_generated`).Scan(&cell); err != nil || cell != owner {
			t.Fatal(owner, cell, err)
		}
	})
}
