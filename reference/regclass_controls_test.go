package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func regclassControlChecks(t *testing.T, ctx context.Context, db referenceExecutor, recorder *referenceRecorder, numeric int64) {
	t.Helper()
	referenceRunStep(t, ctx, db, recorder, referenceSelect(`SELECT r::oid='rr_control."Moved.Target"'::regclass::oid AS same,d::text=r::text AS domain_same,a IS NULL AS array_null,da IS NULL AS domain_array_null FROM rr_control.a_payload ORDER BY id`, []string{"same", "domain_same", "array_null", "domain_array_null"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false, false}}))
	referenceRunStep(t, ctx, db, recorder, referenceSelect("SELECT a,da FROM rr_control.a_payload", []string{"a", "da"}, []string{"_REGCLASS", "_REGCLASS"}, [][]any{{`[-1:0]={"rr_control.\"Moved.Target\"",NULL}`, `{"rr_control.\"Moved.Target\"",NULL}`}}))
	referenceRunStep(t, ctx, db, recorder, referenceSelect(`SELECT v='rr_control."Moved.Target"'::regclass AS same FROM rr_control.scalar_view`, []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	referenceRunStep(t, ctx, db, recorder, referenceSelect("SELECT v FROM rr_control.a_opaque_view", []string{"v"}, []string{"_REGCLASS"}, [][]any{{`{"rr_control.\"Moved.Target\""}`}}))
	referenceRunStep(t, ctx, db, recorder, referenceExec("INSERT INTO rr_control.numeric DEFAULT VALUES", 1))
	referenceRunStep(t, ctx, db, recorder, referenceSelect("SELECT v::bigint AS bits FROM rr_control.numeric", []string{"bits"}, []string{"INT8"}, [][]any{{numeric}}))
	referenceRunStep(t, ctx, db, recorder, referenceExec("DELETE FROM rr_control.numeric", 1))
	referenceRunStep(t, ctx, db, recorder, referenceExec("INSERT INTO rr_control.defaults DEFAULT VALUES", 1))
	referenceRunStep(t, ctx, db, recorder, referenceSelect(`SELECT r,a FROM rr_control.defaults`, []string{"r", "a"}, []string{"REGCLASS", "_REGCLASS"}, [][]any{{`rr_control."Moved.Target"`, `{"rr_control.\"Moved.Target\"",NULL}`}}))
	referenceRunStep(t, ctx, db, recorder, referenceExec("DELETE FROM rr_control.defaults", 1))
	referenceRunStep(t, ctx, db, recorder, referenceSelect(`SELECT refs FROM rr_control.a_payload`, []string{"refs"}, []string{"_REGCLASS"}, [][]any{{"{rr_control.moved_view,rr_control.moved_index,rr_control.moved_sequence,NULL,4294967295}"}}))
	referenceRunStep(t, ctx, db, recorder, referenceSelect(`SELECT 'rr_control.moved_view'::regclass AS v,'rr_control.moved_index'::regclass AS i,'rr_control.moved_sequence'::regclass AS s`, []string{"v", "i", "s"}, []string{"REGCLASS", "REGCLASS", "REGCLASS"}, [][]any{{"rr_control.moved_view", "rr_control.moved_index", "rr_control.moved_sequence"}}))
	referenceRunStep(t, ctx, db, recorder, referenceSelect("SELECT last_value,is_called FROM rr_control.moved_sequence", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), false}}))
}

func regclassNativeControlScenario(t *testing.T) {
	t.Helper()
	schemaReferenceCleanup(t, "rr_control")
	p := newReferenceScenario(t)
	c, err := p.memory.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{
		"CREATE SCHEMA rr_control",
		"CREATE TABLE rr_control.z_target(id INT)",
		"CREATE VIEW rr_control.control_v AS SELECT id FROM rr_control.z_target",
		"CREATE INDEX control_i ON rr_control.z_target(id)",
		"CREATE SEQUENCE rr_control.control_s START 7",
		"CREATE DOMAIN rr_control.rc AS regclass",
		"CREATE DOMAIN rr_control.rca AS regclass[]",
		`CREATE TABLE rr_control.a_payload(id INT,r regclass,a regclass[],d rr_control.rc,da rr_control.rca,refs regclass[],CONSTRAINT opaque_check CHECK(a IS NOT NULL AND ('[-1:0]={rr_control.z_target,NULL}'::regclass[]) IS NOT NULL))`,
		`CREATE TABLE rr_control.defaults(r regclass DEFAULT 'rr_control.z_target',a regclass[] DEFAULT ARRAY['rr_control.z_target'::regclass,NULL])`,
		`CREATE VIEW rr_control.scalar_view AS SELECT 'rr_control.z_target'::regclass AS v`,
		`CREATE VIEW rr_control.a_opaque_view AS SELECT '{rr_control.z_target}'::regclass[] AS v`,
	} {
		referenceLockExec(t, p.ctx, c, q, 0)
	}
	referenceLockExec(t, p.ctx, c, `INSERT INTO rr_control.a_payload VALUES(1,'rr_control.z_target','[-1:0]={rr_control.z_target,NULL}','rr_control.z_target',ARRAY['rr_control.z_target'::regclass,NULL],'{rr_control.control_v,rr_control.control_i,rr_control.control_s,NULL,4294967295}')`, 1)
	var sourceOID int64
	if err = c.QueryRowContext(p.ctx, "SELECT 'rr_control.z_target'::regclass::oid::bigint").Scan(&sourceOID); err != nil {
		t.Fatal(err)
	}
	referenceLockExec(t, p.ctx, c, "CREATE TABLE rr_control.numeric(v regclass DEFAULT "+strconv.FormatInt(sourceOID, 10)+"::bigint::regclass)", 0)
	referenceLockExec(t, p.ctx, c, `ALTER TABLE rr_control.z_target RENAME TO "Moved.Target"`, 0)
	for _, q := range []string{"ALTER VIEW rr_control.control_v RENAME TO moved_view", "ALTER INDEX rr_control.control_i RENAME TO moved_index", "ALTER SEQUENCE rr_control.control_s RENAME TO moved_sequence"} {
		referenceLockExec(t, p.ctx, c, q, 0)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	held, err := c.QueryContext(p.ctx, "SELECT r,a FROM rr_control.a_payload")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	columns, err := held.ColumnTypes()
	if err != nil || len(columns) != 2 || columns[0].DatabaseTypeName() != "REGCLASS" || columns[1].DatabaseTypeName() != "_REGCLASS" {
		t.Fatal(columns, err)
	}
	if err = stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	referenceLockExec(t, p.ctx, c, "CREATE TABLE rr_replacement(v TEXT)", 0)
	referenceLockExec(t, p.ctx, c, "INSERT INTO rr_replacement VALUES('new')", 1)
	arrayReferenceConsumeHeld(t, held, [][]any{{`rr_control."Moved.Target"`, `[-1:0]={"rr_control.\"Moved.Target\"",NULL}`}}, []string{"REGCLASS", "_REGCLASS"})
	if err = stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	regclassControlChecks(t, p.ctx, c, p.memoryRecord, sourceOID)
	for _, input := range []string{`CREATE TABLE rr_missing_restore(r regclass DEFAULT 'rr_missing');`, `CREATE TABLE rr_malformed_restore(r regclass DEFAULT 'a..b');`} {
		if err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(input)); err == nil {
			t.Fatal("invalid regclass restore succeeded")
		}
		regclassControlChecks(t, p.ctx, c, p.memoryRecord, sourceOID)
	}
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err = stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	regclassControlChecks(t, p.ctx, c, p.memoryRecord, sourceOID)
	result, failure := c.ExecContext(p.ctx, `DROP TABLE rr_control."Moved.Target"`)
	if result != nil || failure == nil || referenceExtractDiagnostic(t, failure).Code != "2BP01" {
		t.Fatal(result, failure)
	}
	cursorCtx, stop := context.WithCancel(p.ctx)
	cursor, err := c.QueryContext(cursorCtx, "SELECT r FROM rr_control.a_payload")
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if cursor.Next() || !errors.Is(cursor.Err(), context.Canceled) {
		cursor.Close()
		t.Fatal(cursor.Err())
	}
	cursor.Close()
	target := newReferenceFixture(t, false, true)
	for _, q := range []string{"CREATE DOMAIN rr_allocator_domain AS integer", "CREATE TABLE rr_allocator_a(v INT)", "CREATE TABLE rr_allocator_b(v TEXT)", "CREATE VIEW rr_allocator_v AS SELECT v FROM rr_allocator_a"} {
		referenceLockExec(t, target.ctx, target.memory, q, 0)
	}
	if err = stuntgres.Restore(target.ctx, target.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	tc, err := target.memory.Conn(target.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	regclassControlChecks(t, target.ctx, tc, target.memoryRecord, sourceOID)
	var targetOID int64
	if err = tc.QueryRowContext(target.ctx, `SELECT 'rr_control."Moved.Target"'::regclass::oid::bigint`).Scan(&targetOID); err != nil || targetOID == sourceOID {
		t.Fatal("allocator perturbation did not change identity", sourceOID, targetOID, err)
	}
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		}
		pg, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		if _, err = pg.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL rejected actual native regclass dump", err)
		}
		referenceLockExec(t, p.ctx, pg, "RESET SESSION AUTHORIZATION", 0)
		regclassControlChecks(t, p.ctx, pg, backend.recorder, sourceOID)
		var owner string
		if err = pg.QueryRowContext(p.ctx, `SELECT pg_get_userbyid(c.relowner) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='rr_control' AND c.relname='Moved.Target'`).Scan(&owner); err != nil || owner != "postgres" {
			t.Fatal(owner, err)
		}
	}
}

func TestReferenceRegclassNativeControls(t *testing.T) { regclassNativeControlScenario(t) }

func TestReferenceRegclassNativeCycles(t *testing.T) {
	p := newReferenceScenario(t)
	replayed := false
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if !backend.pg || !replayed {
				for _, q := range []string{"CREATE TABLE rr_self(r regclass)", "ALTER TABLE rr_self ALTER COLUMN r SET DEFAULT 'rr_self'::regclass", "CREATE TABLE rr_left(r regclass)", "CREATE TABLE rr_right(r regclass DEFAULT 'rr_left')", "ALTER TABLE rr_left ALTER COLUMN r SET DEFAULT 'rr_right'::regclass"} {
					referenceLockExec(t, p.ctx, c, q, 0)
				}
				for _, table := range []string{"rr_self", "rr_left", "rr_right"} {
					referenceLockExec(t, p.ctx, c, "INSERT INTO "+table+" DEFAULT VALUES", 1)
				}
			}
			check := referenceSelect("SELECT r FROM rr_self", []string{"r"}, []string{"REGCLASS"}, [][]any{{"rr_self"}})
			referenceRunStep(t, p.ctx, c, backend.recorder, check)
			if !backend.pg {
				var dump bytes.Buffer
				if err = stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				arrayReferenceNativeRoundtrip(t, p, c, backend, check, referenceExec("CREATE TABLE rr_cycle_allocator(v INT)", 0))
				if p.postgres != nil {
					if _, err = p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
						t.Fatal("PostgreSQL rejected actual cyclic regclass dump", err)
					}
					replayed = true
				}
			}
			for _, pair := range []struct{ table, value string }{{"rr_left", "rr_right"}, {"rr_right", "rr_left"}} {
				referenceRunStep(t, p.ctx, c, backend.recorder, referenceSelect("SELECT r FROM "+pair.table, []string{"r"}, []string{"REGCLASS"}, [][]any{{pair.value}}))
				referenceLockExec(t, p.ctx, c, "INSERT INTO "+pair.table+" DEFAULT VALUES", 1)
			}
			referenceLockExec(t, p.ctx, c, "INSERT INTO rr_self DEFAULT VALUES", 1)
			referenceRunStep(t, p.ctx, c, backend.recorder, referenceSelect("SELECT r FROM rr_self", []string{"r"}, []string{"REGCLASS"}, [][]any{{"rr_self"}, {"rr_self"}}))
			referenceLockExec(t, p.ctx, c, "DROP TABLE rr_self", 0)
			referenceLockExec(t, p.ctx, c, "DROP TABLE rr_left,rr_right", 0)
			referenceRunStep(t, p.ctx, c, backend.recorder, referenceSelect("SELECT count(*) AS n FROM pg_class WHERE relname='rr_self' OR relname='rr_left' OR relname='rr_right'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	}
}
