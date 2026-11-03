package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const ksAlterHeadersSQL = "SELECT k,coalesce(v::text,'<null>') AS v,ctid,xmin,xmax FROM ks_part.heap ORDER BY k"

// Historical validation must ignore a killed physical payload without VACUUM.
// Each same-input control first proves that an unkilled violation rejects the
// whole candidate, including companion DROP, shape and type changes.
func TestReferencePgsurgeryKillAlterValidation(t *testing.T) {
	for _, constraint := range []string{"check", "not_null"} {
		for _, shape := range []string{"plain", "add_column", "drop_column", "binary_type"} {
			for _, route := range []string{"direct", "prepared"} {
				t.Run(constraint+"/"+shape+"/"+route, func(t *testing.T) {
					ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						ksAlterValidation(t, p, b, c, constraint, shape, route == "prepared")
					})
				})
			}
		}
		for _, route := range []string{"direct", "prepared"} {
			t.Run(constraint+"/repaired_additional/"+route, func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					ksAlterRepairedValidation(t, p, b, c, constraint, route == "prepared")
				})
			})
		}
	}
}

func ksAlterValidation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, constraint, shape string, prepared bool) {
	t.Helper()
	freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE ks_part.heap ADD CONSTRAINT keep_positive CHECK(k>0)", 0))
	if shape == "drop_column" {
		freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE ks_part.heap ADD COLUMN scratch integer DEFAULT 9", 0))
	}
	var violating any = "violation"
	violatingText := "violation"
	if constraint == "not_null" {
		violating, violatingText = nil, "<null>"
	}
	freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap(k,v) VALUES(1,$1),(2,'survivor')", 2, violating))
	before := freezeCapture(t, p, c, ksAlterHeadersSQL)
	if len(before) != 2 || before[0].k != 1 || before[0].v != violatingText || before[1].k != 2 || before[1].v != "survivor" || before[0].tid == before[1].tid {
		t.Fatal("independent violating/surviving payloads and distinct tuple identities", before)
	}
	var tableOID, keepOID int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'ks_part.heap'::regclass::oid::bigint,oid::bigint FROM pg_constraint WHERE conrelid='ks_part.heap'::regclass AND conname='keep_positive'").Scan(&tableOID, &keepOID); err != nil || tableOID <= 0 || keepOID <= 0 {
		t.Fatal("genuine table/constraint identity", tableOID, keepOID, err)
	}

	// Fixed projections survive shape/type changes without SELECT *'s changed
	// result signature. Both read and parameterized write are genuinely held
	// across rejected ALTER, surgery and successful constraint publication.
	read := ksAlterHeld(t, p, c, ksAlterHeadersSQL)
	defer read.Statement.Close()
	const insertSQL = "INSERT INTO ks_part.heap(k,v) VALUES($1::integer,$2::text)"
	insert := ksAlterHeld(t, p, c, insertSQL)
	defer insert.Statement.Close()
	ksAlterHeaders(t, p, b, read, before...)
	ksAlterMetadata(t, p, b, c, constraint, shape, false)

	actions := "DROP CONSTRAINT keep_positive,"
	switch shape {
	case "add_column":
		actions += "ADD COLUMN extra integer DEFAULT 7,"
	case "drop_column":
		actions += "DROP COLUMN scratch,"
	case "binary_type":
		// Unbounded text -> varchar is binary-coercible, not a heap rewrite.
		actions += "ALTER COLUMN v TYPE varchar,"
	}
	want := fixture.ControlsReferenceDiagnostic("23514", `check constraint "survivor_check" of relation "heap" is violated by some row`)
	want.Schema, want.Table, want.Constraint = "ks_part", "heap", "survivor_check"
	if constraint == "check" {
		actions += "ADD CONSTRAINT survivor_check CHECK(v='survivor')"
	} else {
		actions += "ALTER COLUMN v SET NOT NULL"
		want = fixture.ControlsReferenceDiagnostic("23502", `column "v" of relation "heap" contains null values`)
		want.Schema, want.Table, want.Column = "ks_part", "heap", "v"
	}
	alterSQL := "ALTER TABLE ks_part.heap " + actions
	var alter fixture.Executor = c
	if prepared {
		// Reuse this exact prepared utility after its historical-scan error.
		heldAlter := ksAlterHeld(t, p, c, alterSQL)
		defer heldAlter.Statement.Close()
		alter = heldAlter
	}
	failure := fixture.ErrorDiagnostic(alterSQL, want)
	failure.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, alter, b.Recorder, failure)
	ksAlterMetadata(t, p, b, c, constraint, shape, false)
	freezeBoth(t, p, b, c, fixture.Select("SELECT 'ks_part.heap'::regclass::oid::bigint AS table_oid,oid::bigint AS constraint_oid FROM pg_constraint WHERE conrelid='ks_part.heap'::regclass AND conname='keep_positive'", []string{"table_oid", "constraint_oid"}, []string{"INT8", "INT8"}, [][]any{{tableOID, keepOID}}))
	ksAlterHeaders(t, p, b, c, before...)
	ksAlterHeaders(t, p, b, read, before...)
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v::text AS v FROM ks_part.heap ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), violating}, {int64(2), "survivor"}}))
	if shape == "drop_column" {
		freezeBoth(t, p, b, c, fixture.Select("SELECT scratch FROM ks_part.heap ORDER BY k", []string{"scratch"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(9)}}))
	}

	kill := ksVoid(killSQL, "ks_part.heap", freezeTIDs(before[0].tid))
	kill.Prepared = prepared
	freezeRun(t, p, b, c, kill)
	ksAlterHeaders(t, p, b, c, before[1])
	ksAlterHeaders(t, p, b, read, before[1])
	// No DELETE, UPDATE, VACUUM or replacement can remove the bad payload
	// between killing its exact slot and this historical validation.
	fixture.RunStep(t, p.Ctx, alter, b.Recorder, fixture.Exec(alterSQL, 0))
	ksAlterMetadata(t, p, b, c, constraint, shape, true)
	freezeBoth(t, p, b, c, fixture.Select("SELECT 'ks_part.heap'::regclass::oid::bigint AS table_oid", []string{"table_oid"}, []string{"INT8"}, [][]any{{tableOID}}))
	ksAlterHeaders(t, p, b, c, before[1])
	ksAlterHeaders(t, p, b, read, before[1])
	if shape == "add_column" {
		freezeBoth(t, p, b, c, fixture.Select("SELECT k,v,extra FROM ks_part.heap ORDER BY k", []string{"k", "v", "extra"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(2), "survivor", int64(7)}}))
	}

	// Success must install an enforced valid constraint, not merely suppress
	// history errors. Fresh direct/prepared and the pre-ALTER held writer all
	// reject the same new violation, leaving the survivor's header unchanged.
	future := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "heap" violates check constraint "survivor_check"`)
	future.Schema, future.Table, future.Constraint = "ks_part", "heap", "survivor_check"
	rowDetail := "3, violation"
	if constraint == "not_null" {
		future = fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "heap" violates not-null constraint`)
		future.Schema, future.Table, future.Column = "ks_part", "heap", "v"
		rowDetail = "3, null"
	}
	if shape == "add_column" {
		rowDetail += ", 7"
	}
	future.Detail = "Failing row contains (" + rowDetail + ")."
	invalid := fixture.ErrorDiagnostic(insertSQL, future, int64(3), violating)
	invalid.ErrorStage = "execute"
	freezeBoth(t, p, b, c, invalid)
	fixture.RunStep(t, p.Ctx, insert, b.Recorder, invalid)
	ksAlterHeaders(t, p, b, c, before[1])
	ksAlterHeaders(t, p, b, read, before[1])
}

// A repaired aborted INSERT is a real additional physical version, not an
// ordinary committed head. Killing another violation cannot exempt this live
// repaired payload from historical validation; killing both permits the ALTER.
func ksAlterRepairedValidation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, constraint string, prepared bool) {
	t.Helper()
	freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE ks_part.heap ADD CONSTRAINT keep_positive CHECK(k>0)", 0))
	horizon := ksLifeHorizon(t, p, b)
	defer horizon.ExecContext(p.Ctx, "ROLLBACK")
	var bad any = "violation"
	badText := "violation"
	action := "ADD CONSTRAINT survivor_check CHECK(v='survivor')"
	want := fixture.ControlsReferenceDiagnostic("23514", `check constraint "survivor_check" of relation "heap" is violated by some row`)
	want.Schema, want.Table, want.Constraint = "ks_part", "heap", "survivor_check"
	if constraint == "not_null" {
		bad, badText = nil, "<null>"
		action = "ALTER COLUMN v SET NOT NULL"
		want = fixture.ControlsReferenceDiagnostic("23502", `column "v" of relation "heap" contains null values`)
		want.Schema, want.Table, want.Column = "ks_part", "heap", "v"
	}
	freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(1,$1),(2,'survivor')", 2, bad))
	ordinary := freezeCapture(t, p, c, ksAlterHeadersSQL)
	if len(ordinary) != 2 || ordinary[0].k != 1 || ordinary[0].v != badText || ordinary[1].k != 2 || ordinary[1].v != "survivor" {
		t.Fatal("ordinary violation and survivor", ordinary)
	}
	freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
	defer c.ExecContext(p.Ctx, "ROLLBACK")
	repaired := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(3,$1) RETURNING k,coalesce(v,'<null>') AS v,ctid,xmin,xmax", bad)
	if repaired.k != 3 || repaired.v != badText || repaired.tid == ordinary[0].tid || repaired.tid == ordinary[1].tid {
		t.Fatal("genuine private violating version", repaired)
	}
	freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
	// Repair the genuine aborted slot before any scan/pruning can retire it.
	repair := freezeVoid("SELECT ks_part.heap_force_freeze($1::regclass,$2::tid[]) AS repaired", "ks_part.heap", freezeTIDs(repaired.tid))
	repair.Prepared = prepared
	freezeRun(t, p, b, c, repair)
	repaired = freezeRepaired(repaired)
	// Release the horizon's AccessShare before constraint AccessExclusive.
	freezeRun(t, p, b, horizon, fixture.Exec("ROLLBACK", 0))
	ksAlterHeaders(t, p, b, c, ordinary[0], ordinary[1], repaired)
	kill := ksVoid(killSQL, "ks_part.heap", freezeTIDs(ordinary[0].tid))
	kill.Prepared = prepared
	freezeRun(t, p, b, c, kill)
	ksAlterHeaders(t, p, b, c, ordinary[1], repaired)
	q := "ALTER TABLE ks_part.heap DROP CONSTRAINT keep_positive," + action
	var source fixture.Executor = c
	if prepared {
		held := ksAlterHeld(t, p, c, q)
		defer held.Statement.Close()
		source = held
	}
	failure := fixture.ErrorDiagnostic(q, want)
	failure.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, source, b.Recorder, failure)
	ksAlterMetadata(t, p, b, c, constraint, "plain", false)
	ksAlterHeaders(t, p, b, c, ordinary[1], repaired)
	kill = ksVoid(killSQL, "ks_part.heap", freezeTIDs(repaired.tid))
	kill.Prepared = prepared
	freezeRun(t, p, b, c, kill)
	fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec(q, 0))
	ksAlterMetadata(t, p, b, c, constraint, "plain", true)
	ksAlterHeaders(t, p, b, c, ordinary[1])
}

func ksAlterHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	st, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	return fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
}

func ksAlterHeaders(t *testing.T, p *fixture.Pair, b fixture.Backend, source fixture.Executor, expected ...freezeHeader) {
	t.Helper()
	rows := make([][]any, len(expected))
	for i, h := range expected {
		rows[i] = []any{h.k, h.v, h.tid, h.xmin, h.xmax}
	}
	fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Select(ksAlterHeadersSQL, []string{"k", "v", "ctid", "xmin", "xmax"}, []string{"INT4", "TEXT", "TID", "XID", "XID"}, rows))
}

func ksAlterMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, constraint, shape string, installed bool) {
	t.Helper()
	valueType := int64(25) // text
	if installed && shape == "binary_type" {
		valueType = 1043 // varchar
	}
	attributes := [][]any{{"k", int64(23), false, false}, {"v", valueType, installed && constraint == "not_null", false}}
	if !installed && shape == "drop_column" {
		attributes = append(attributes, []any{"scratch", int64(23), false, true})
	}
	if installed && shape == "add_column" {
		attributes = append(attributes, []any{"extra", int64(23), false, true})
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT attname::text AS name,atttypid::bigint AS type,attnotnull,atthasdef FROM pg_attribute WHERE attrelid='ks_part.heap'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"name", "type", "attnotnull", "atthasdef"}, []string{"TEXT", "INT8", "BOOL", "BOOL"}, attributes))
	kind := "c"
	if installed && constraint == "not_null" {
		kind = "n"
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT contype::text AS kind,convalidated,conenforced FROM pg_constraint WHERE conrelid='ks_part.heap'::regclass ORDER BY conname", []string{"kind", "convalidated", "conenforced"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{kind, true, true}}))
	checks := [][]any{{"keep_positive", true, true}}
	if installed {
		checks = nil
		if constraint == "check" {
			checks = [][]any{{"survivor_check", true, true}}
		}
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT conname::text AS name,convalidated,conenforced FROM pg_constraint WHERE conrelid='ks_part.heap'::regclass AND contype='c' ORDER BY conname", []string{"name", "convalidated", "conenforced"}, []string{"TEXT", "BOOL", "BOOL"}, checks))
}
