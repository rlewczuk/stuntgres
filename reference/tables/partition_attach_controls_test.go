package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE pa_control_seq START 2", 0))
			run(fixture.Exec("CREATE TABLE pa_control_parent(k INT CONSTRAINT pa_control_root_nn NOT NULL DEFAULT 1,payload TEXT DEFAULT 'parent',CONSTRAINT pa_control_check CHECK(k>0),CONSTRAINT pa_control_key UNIQUE(k)) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pa_control_child(obsolete INT,payload TEXT DEFAULT 'child',k INT CONSTRAINT pa_control_child_nn NOT NULL DEFAULT nextval('pa_control_seq'),CONSTRAINT pa_control_check CHECK(k>0),CONSTRAINT pa_control_local CHECK(length(payload)>0),CONSTRAINT pa_control_existing UNIQUE(k))", 0))
			run(fixture.Exec("ALTER TABLE pa_control_child DROP COLUMN obsolete", 0))
			run(fixture.Exec("ALTER SEQUENCE pa_control_seq OWNED BY pa_control_child.k", 0))
			run(fixture.Exec("INSERT INTO pa_control_child(payload,k) VALUES('one',1)", 1))
			run(fixture.Exec("ALTER TABLE pa_control_parent ATTACH PARTITION pa_control_child FOR VALUES FROM(1) TO(3)", 0))
			run(fixture.Exec("CREATE TABLE pa_control_mid(payload TEXT DEFAULT 'mid',k INT CONSTRAINT pa_control_mid_nn NOT NULL,CONSTRAINT pa_control_check CHECK(k>0),CONSTRAINT pa_control_mid_key UNIQUE(k)) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pa_control_leaf(k INT CONSTRAINT pa_control_leaf_nn NOT NULL,payload TEXT DEFAULT 'leaf',CONSTRAINT pa_control_check CHECK(k>0),CONSTRAINT pa_control_leaf_key UNIQUE(k))", 0))
			run(fixture.Exec("INSERT INTO pa_control_leaf VALUES(8,'eight')", 1))
			run(fixture.Exec("ALTER TABLE pa_control_leaf ADD CONSTRAINT pa_control_unvalidated CHECK(payload <> 'eight') NOT VALID", 0))
			run(fixture.Exec("ALTER TABLE pa_control_mid ATTACH PARTITION pa_control_leaf DEFAULT", 0))
			run(fixture.Exec("ALTER TABLE pa_control_parent ATTACH PARTITION pa_control_mid DEFAULT", 0))
			partitionAttachControlState(t, p, b.DB, b.Recorder, b.PG)
			if b.PG {
				partitionAttachControlFuture(t, p, b.DB, b.Recorder)
				return // Native replacement has no PostgreSQL control API.
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			query := "SELECT k,payload FROM pa_control_parent ORDER BY k"
			old, err := b.DB.QueryContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			held, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			partitionAttachControlState(t, p, b.DB, b.Recorder, false)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: query}, b.Recorder, fixture.Select(query, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(8), "eight"}}))
			var retained [][]any
			for old.Next() {
				var k, payload any
				if err := old.Scan(&k, &payload); err != nil {
					t.Fatal(err)
				}
				retained = append(retained, []any{k, payload})
			}
			if err := old.Err(); err != nil || !reflect.DeepEqual(retained, [][]any{{int64(1), "one"}, {int64(8), "eight"}}) {
				t.Fatalf("retained rows %#v: %v", retained, err)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatalf("roundtrip dump changed: %v\n%s", err, redump.String())
			}
			// The fixture owns a fresh Driver and a recorded SQL pool sharing
			// the same connector/engine as its unwrapped native control pool.
			destination := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(destination.Ctx, destination.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			partitionAttachControlState(t, destination, destination.Memory, destination.MemoryRecord, false)
			// Independently exercise preserved own defaults, sequence ownership,
			// root defaults, local checks and populated inherited key backings.
			partitionAttachControlFuture(t, destination, destination.Memory, destination.MemoryRecord)
			// Failed replacement must leave the live stream and captured root intact.
			run(fixture.Exec("CREATE TABLE pa_control_sentinel(k INT)", 0))
			run(fixture.Exec("INSERT INTO pa_control_sentinel VALUES(91)", 1))
			sentinelCursor, err := b.DB.QueryContext(p.Ctx, "SELECT k FROM pa_control_sentinel")
			if err != nil {
				t.Fatal(err)
			}
			defer sentinelCursor.Close()
			var beforeFailure bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &beforeFailure); err != nil {
				t.Fatal(err)
			}
			failed := dump.String() + "CREATE TABLE pa_control_bad(k INT NOT NULL,payload TEXT,CONSTRAINT pa_control_check CHECK(k>0)); INSERT INTO pa_control_bad VALUES(9,'bad'); ALTER TABLE pa_control_parent ATTACH PARTITION pa_control_bad FOR VALUES FROM(3) TO(5);"
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(failed)), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `partition constraint of relation "pa_control_bad" is violated by some row`, Schema: "public", Table: "pa_control_bad"})
			partitionAttachControlState(t, p, b.DB, b.Recorder, false)
			run(fixture.Select("SELECT k FROM pa_control_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(91)}}))
			if !sentinelCursor.Next() {
				t.Fatal("failed Restore lost retained sentinel cursor")
			}
			var sentinel int64
			if err := sentinelCursor.Scan(&sentinel); err != nil || sentinel != 91 {
				t.Fatalf("retained sentinel %d: %v", sentinel, err)
			}
			if sentinelCursor.Next() || sentinelCursor.Err() != nil {
				t.Fatal("retained sentinel cursor changed", sentinelCursor.Err())
			}
			redump.Reset()
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(beforeFailure.Bytes(), redump.Bytes()) {
				t.Fatal("failed Restore changed live stream", err)
			}
			if p.Postgres != nil {
				partitionAttachReplay(t, p, dump.String(), func(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
					partitionAttachControlState(t, p, x, r, pg)
					partitionAttachControlFuture(t, p, x, r)
				})
			}
		})
	}
}

func partitionAttachControlState(t *testing.T, p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT k,payload FROM pa_control_parent ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(8), "eight"}})
		s.Prepared = prepared
		s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{false, true})
		run(s)
		s = fixture.Select("SELECT * FROM pa_control_child", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}})
		s.Prepared = prepared
		s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, false})
		run(s)
	}
	run(partitionConstraintEdge("pa_control_existing", "pa_control_key", "pa_control_child"))
	run(partitionConstraintEdge("pa_control_mid_key", "pa_control_key", "pa_control_mid"))
	run(partitionConstraintEdge("pa_control_leaf_key", "pa_control_mid_key", "pa_control_leaf"))
	run(fixture.Select("SELECT last_value,is_called FROM pa_control_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), false}}))
	run(fixture.Select("SELECT c.relname,c.relowner=p.relowner AS same_owner FROM pg_class c JOIN pg_class p ON p.relname='pa_control_parent' WHERE c.relname='pa_control_child' OR c.relname='pa_control_mid' OR c.relname='pa_control_leaf' ORDER BY c.relname", []string{"relname", "same_owner"}, []string{"NAME", "BOOL"}, [][]any{{"pa_control_child", true}, {"pa_control_leaf", true}, {"pa_control_mid", true}}))
	run(fixture.Select("SELECT conname,contype,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pa_control_child'::regclass::OID ORDER BY conname", []string{"conname", "contype", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "INT2"}, [][]any{{"pa_control_check", "c", false, int64(1)}, {"pa_control_child_nn", "n", false, int64(1)}, {"pa_control_existing", "u", false, int64(1)}, {"pa_control_local", "c", true, int64(0)}}))
	run(fixture.Select("SELECT conname,contype,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pa_control_leaf'::regclass::OID ORDER BY conname", []string{"conname", "contype", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "INT2"}, [][]any{{"pa_control_check", "c", false, int64(1)}, {"pa_control_leaf_key", "u", false, int64(1)}, {"pa_control_leaf_nn", "n", false, int64(1)}, {"pa_control_unvalidated", "c", true, int64(0)}}))
	run(fixture.Select("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='pa_control_leaf'::regclass::OID AND conname='pa_control_unvalidated'", []string{"definition"}, []string{"TEXT"}, [][]any{{"CHECK ((payload <> 'eight'::text)) NOT VALID"}}))
	run(fixture.Select("SELECT count(*) FROM pg_attrdef WHERE adrelid='pa_control_leaf'::regclass::OID", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
}

func partitionAttachControlFuture(t *testing.T, p *fixture.Pair, x fixture.Executor, record *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
	run(fixture.Exec("INSERT INTO pa_control_child(payload) VALUES('two')", 1))
	run(fixture.Select("SELECT payload,k FROM pa_control_child ORDER BY k", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}, {"two", int64(2)}}))
	run(partitionConstraintDuplicate("INSERT INTO pa_control_parent(k) VALUES(1)", "pa_control_child", "pa_control_existing", "Key (k)=(1) already exists."))
	run(referenceRangeLeafMiss("INSERT INTO pa_control_child VALUES('outside',3)", "pa_control_child", "outside, 3"))
	run(fixture.Exec("INSERT INTO pa_control_parent(k) VALUES(9)", 1))
	run(fixture.Exec("INSERT INTO pa_control_mid(k) VALUES(10)", 1))
	run(fixture.Exec("INSERT INTO pa_control_leaf(k) VALUES(11)", 1))
	run(fixture.Select("SELECT k,payload FROM pa_control_leaf ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "eight"}, {int64(9), "parent"}, {int64(10), "mid"}, {int64(11), "leaf"}}))
	run(fixture.Error("INSERT INTO pa_control_leaf(payload) VALUES('no key')", "23502"))
	run(fixture.Error("INSERT INTO pa_control_child(payload,k) VALUES('',2)", "23514"))
	run(fixture.Error("INSERT INTO pa_control_leaf VALUES(12,'eight')", "23514"))
}

func partitionAttachReplay(t *testing.T, p *fixture.Pair, stream string, check func(*testing.T, *fixture.Pair, fixture.Executor, *fixture.Recorder, bool)) {
	t.Helper()
	conn, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
			if _, err := conn.ExecContext(ctx, q); err != nil {
				t.Error(err)
			}
		}
	}()
	if _, err := conn.ExecContext(p.Ctx, stream); err != nil {
		t.Fatal("PostgreSQL rejected actual attachment stream", err)
	}
	check(t, p, conn, p.PostgresRecord, true)
}

type partitionAttachReplayCase struct {
	reorderedOther     bool
	localLeafKey       bool
	onlyReferenceCheck bool
	lateGoodData       bool
}

// A local incomplete ONLY index is deliberately not the parent's backing.
// An old row refers to that exact index and predates a local NOT VALID CHECK.
// Both the earlier partial tree and the separate valid upper tree must survive
// actual replay, including when every child needs its original physical layout.
func TestReferencePartitionAttachControlIndexDependencies(t *testing.T) {
	cases := []struct {
		name   string
		replay partitionAttachReplayCase
	}{
		{"faithful_other", partitionAttachReplayCase{}},
		{"reordered_other", partitionAttachReplayCase{reorderedOther: true}},
		{"constraint_leaf", partitionAttachReplayCase{reorderedOther: true, localLeafKey: true}},
		{"only_reference_check", partitionAttachReplayCase{reorderedOther: true, onlyReferenceCheck: true}},
		{"late_good_index_data", partitionAttachReplayCase{reorderedOther: true, lateGoodData: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { partitionAttachIndexDependencies(t, c.replay) })
	}
}

func partitionAttachIndexDependencies(t *testing.T, c partitionAttachReplayCase) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			check := func(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
				partitionAttachIndexDependencyState(t, p, x, r, pg, c)
				columns, types := []string{"payload", "ref", "k"}, []string{"TEXT", "REGCLASS", "INT4"}
				if c.reorderedOther {
					columns, types = []string{"k", "payload", "ref"}, []string{"INT4", "TEXT", "REGCLASS"}
				}
				fixture.RunStep(t, p.Ctx, x, r, fixture.Select("SELECT * FROM pa_phase_other", columns, types, nil))
			}
			run(fixture.Exec("CREATE TABLE pa_phase_root(k INT NOT NULL,ref REGCLASS,payload TEXT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX pa_phase_root_idx ON pa_phase_root(k)", 0))
			run(fixture.Exec("CREATE TABLE pa_phase_mid(payload TEXT,ref REGCLASS,k INT NOT NULL) PARTITION BY RANGE(k)", 0))
			otherBound, upper := "FOR VALUES FROM(2) TO(3)", "3"
			if c.lateGoodData {
				otherBound, upper = "FOR VALUES FROM(3) TO(4)", "5"
			}
			if c.reorderedOther {
				run(fixture.Exec("CREATE TABLE pa_phase_other(k INT NOT NULL,payload TEXT,ref REGCLASS)", 0))
				run(fixture.Exec("ALTER TABLE pa_phase_mid ATTACH PARTITION pa_phase_other "+otherBound, 0))
			} else {
				run(fixture.Exec("CREATE TABLE pa_phase_other PARTITION OF pa_phase_mid "+otherBound, 0))
			}
			run(fixture.Exec("CREATE UNIQUE INDEX pa_phase_only ON ONLY pa_phase_mid(k)", 0))
			run(fixture.Exec(partitionAttachReplayLeafSQL(c), 0))
			run(fixture.Exec("INSERT INTO pa_phase_leaf VALUES('old','pa_phase_only'::regclass,1)", 1))
			run(fixture.Exec("ALTER TABLE pa_phase_leaf ADD CONSTRAINT pa_phase_late CHECK(payload <> 'old') NOT VALID", 0))
			run(fixture.Exec("ALTER TABLE pa_phase_mid ATTACH PARTITION pa_phase_leaf DEFAULT", 0))
			run(fixture.Exec("ALTER TABLE pa_phase_root ATTACH PARTITION pa_phase_mid FOR VALUES FROM(1) TO("+upper+")", 0))
			if c.lateGoodData {
				run(fixture.Exec("INSERT INTO pa_phase_leaf VALUES('good','pa_phase_mid_k_idx'::regclass,2)", 1))
			}
			check(t, p, b.DB, b.Recorder, b.PG)
			if b.PG {
				// A faithfully equivalent SQL ordering, not a fabricated native
				// dump, establishes the previously missing late-leaf ALTER case.
				run(fixture.Exec("DROP TABLE pa_phase_root CASCADE", 0))
				for _, q := range partitionAttachIndexDependencyReplaySQL(c) {
					count := int64(0)
					if strings.HasPrefix(q, "INSERT ") {
						count = 1
					}
					run(fixture.Exec(q, count))
				}
				check(t, p, b.DB, b.Recorder, true)
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(t, p, b.DB, b.Recorder, false)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatalf("index dependency roundtrip changed: %v\n%s", err, again.String())
			}
			fresh := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(t, fresh, fresh.Memory, fresh.MemoryRecord, false)
			if p.Postgres != nil {
				partitionAttachReplay(t, p, dump.String(), check)
			}
		})
	}
}

func partitionAttachIndexDependencyState(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool, c partitionAttachReplayCase) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	partial, inherited := "pa_phase_leaf_k_idx", "pa_phase_leaf_k_idx1"
	if c.localLeafKey {
		partial, inherited = "pa_phase_leaf_local", "pa_phase_leaf_k_idx"
	}
	rows := [][]any{{int64(1), "pa_phase_only", "old"}}
	if c.lateGoodData {
		rows = append(rows, []any{int64(2), "pa_phase_mid_k_idx", "good"})
	}
	run(fixture.Select("SELECT k,ref::TEXT,payload FROM pa_phase_root ORDER BY k", []string{"k", "ref", "payload"}, []string{"INT4", "TEXT", "TEXT"}, rows))
	run(partitionConstraintIndexState("pa_phase_only", false))
	run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_phase_only'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
	run(partitionConstraintIndexState("pa_phase_mid_k_idx", true))
	run(partitionConstraintIndexState(partial, true))
	run(partitionConstraintIndexState(inherited, true))
	run(partitionConstraintIndexState("pa_phase_other_k_idx", true))
	run(partitionConstraintEdge("pa_phase_mid_k_idx", "pa_phase_root_idx", "pa_phase_mid"))
	run(partitionConstraintEdge(partial, "pa_phase_only", "pa_phase_leaf"))
	run(partitionConstraintEdge(inherited, "pa_phase_mid_k_idx", "pa_phase_leaf"))
	run(partitionConstraintEdge("pa_phase_other_k_idx", "pa_phase_mid_k_idx", "pa_phase_other"))
	run(fixture.Select("SELECT count(*) FROM pg_index WHERE indrelid='pa_phase_mid'::regclass::OID", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(fixture.Select("SELECT count(*) FROM pg_index WHERE indrelid='pa_phase_leaf'::regclass::OID", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(fixture.Select("SELECT conname,conislocal,coninhcount,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='pa_phase_leaf'::regclass::OID AND conname='pa_phase_late'", []string{"conname", "conislocal", "coninhcount", "definition"}, []string{"NAME", "BOOL", "INT2", "TEXT"}, [][]any{{"pa_phase_late", true, int64(0), "CHECK ((payload <> 'old'::text)) NOT VALID"}}))
	if c.localLeafKey {
		run(fixture.Select("SELECT conname,conparentid::TEXT AS parent,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pa_phase_leaf'::regclass::OID AND conname='pa_phase_leaf_local'", []string{"conname", "parent", "conislocal", "coninhcount"}, []string{"NAME", "TEXT", "BOOL", "INT2"}, [][]any{{"pa_phase_leaf_local", "0", true, int64(0)}}))
		run(partitionConstraintDuplicate("INSERT INTO pa_phase_root VALUES(1,NULL,'good')", "pa_phase_leaf", partial, "Key (k)=(1) already exists."))
		run(fixture.Error("INSERT INTO pa_phase_leaf VALUES('old',NULL,1)", "23514"))
	}
	if c.onlyReferenceCheck {
		run(fixture.Select("SELECT conname,conislocal,coninhcount,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='pa_phase_leaf'::regclass::OID AND conname='pa_phase_ref'", []string{"conname", "conislocal", "coninhcount", "definition"}, []string{"NAME", "BOOL", "INT2", "TEXT"}, [][]any{{"pa_phase_ref", true, int64(0), "CHECK (('pa_phase_only'::regclass IS NOT NULL))"}}))
	}
}

func partitionAttachReplayLeafSQL(c partitionAttachReplayCase) string {
	text := "CREATE TABLE pa_phase_leaf(payload TEXT,ref REGCLASS,k INT NOT NULL"
	if c.localLeafKey {
		text += ",CONSTRAINT pa_phase_leaf_local UNIQUE(k)"
	}
	if c.onlyReferenceCheck {
		text += ",CONSTRAINT pa_phase_ref CHECK('pa_phase_only'::regclass IS NOT NULL)"
	}
	return text + ")"
}

func partitionAttachIndexDependencyReplaySQL(c partitionAttachReplayCase) []string {
	other := "CREATE TABLE pa_phase_other(payload TEXT,ref REGCLASS,k INT NOT NULL)"
	if c.reorderedOther {
		other = "CREATE TABLE pa_phase_other(k INT NOT NULL,payload TEXT,ref REGCLASS)"
	}
	otherBound, upper := "FOR VALUES FROM(2) TO(3)", "3"
	if c.lateGoodData {
		otherBound, upper = "FOR VALUES FROM(3) TO(4)", "5"
	}
	partial, inherited := "pa_phase_leaf_k_idx", "pa_phase_leaf_k_idx1"
	if c.localLeafKey {
		partial, inherited = "pa_phase_leaf_local", "pa_phase_leaf_k_idx"
	}
	statements := []string{
		"CREATE TABLE pa_phase_root(k INT NOT NULL,ref REGCLASS,payload TEXT) PARTITION BY RANGE(k)",
		"CREATE TABLE pa_phase_mid(payload TEXT,ref REGCLASS,k INT NOT NULL) PARTITION BY RANGE(k)",
		other,
		"CREATE UNIQUE INDEX pa_phase_root_idx ON ONLY pa_phase_root(k)",
		"CREATE UNIQUE INDEX pa_phase_other_k_idx ON pa_phase_other(k)",
		"ALTER TABLE pa_phase_mid ATTACH PARTITION pa_phase_other " + otherBound,
		"CREATE UNIQUE INDEX pa_phase_only ON ONLY pa_phase_mid(k)",
		partitionAttachReplayLeafSQL(c),
	}
	if !c.localLeafKey {
		statements = append(statements, "CREATE UNIQUE INDEX "+partial+" ON pa_phase_leaf(k)")
	}
	statements = append(statements,
		"CREATE UNIQUE INDEX "+inherited+" ON pa_phase_leaf(k)",
		"ALTER TABLE pa_phase_mid ATTACH PARTITION pa_phase_leaf DEFAULT",
		"CREATE UNIQUE INDEX pa_phase_mid_k_idx ON ONLY pa_phase_mid(k)",
		"ALTER INDEX pa_phase_mid_k_idx ATTACH PARTITION "+inherited,
		"ALTER INDEX pa_phase_mid_k_idx ATTACH PARTITION pa_phase_other_k_idx",
		"ALTER INDEX pa_phase_only ATTACH PARTITION "+partial,
		"INSERT INTO pa_phase_leaf VALUES('old','pa_phase_only'::regclass,1)",
	)
	if c.lateGoodData {
		statements = append(statements, "INSERT INTO pa_phase_leaf VALUES('good','pa_phase_mid_k_idx'::regclass,2)")
	}
	return append(statements,
		"ALTER TABLE pa_phase_leaf ADD CONSTRAINT pa_phase_late CHECK(payload <> 'old') NOT VALID",
		"ALTER TABLE pa_phase_root ATTACH PARTITION pa_phase_mid FOR VALUES FROM(1) TO("+upper+")",
	)
}
