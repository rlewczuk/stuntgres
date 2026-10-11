package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Keep SQL oracle discovery separate from the native-control definition: the
// postgres core subtests need no successful native Dump to reach their checks.
func TestReferencePartitionConstraintsRegclassReferences(t *testing.T) {
	partitionConstraintCase(t, "stored_virtual_backings", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionRegclassSetup(run)
		before := partitionRegclassIDs(t, p.Ctx, b.DB, false)
		partitionRegclassRename(run)
		partitionRegclassRequireIDs(t, before, partitionRegclassIDs(t, p.Ctx, b.DB, true))
		partitionRegclassChecks(t, run, b.PG)
		partitionRegclassFuture(run)
	})
}

func partitionRegclassSetup(run func(fixture.Step)) {
	for _, query := range []string{
		"CREATE TABLE pc_rr_root(k INT,b INT,CONSTRAINT pc_rr_key UNIQUE(k,b)) PARTITION BY RANGE(k)",
		"CREATE TABLE pc_rr_mid PARTITION OF pc_rr_root(CONSTRAINT pc_rr_mid_pk PRIMARY KEY(k,b)) DEFAULT PARTITION BY RANGE(b)",
		"CREATE TABLE pc_rr_leaf PARTITION OF pc_rr_mid DEFAULT",
		// Ordinary scalar/array defaults are eligible for deferred emission.
		"CREATE TABLE pc_rr_defaults(r REGCLASS DEFAULT 'pc_rr_key'::regclass,m REGCLASS DEFAULT 'pc_rr_mid_k_b_key'::regclass,a REGCLASS[] DEFAULT '{pc_rr_key,pc_rr_mid_k_b_key,pc_rr_mid_pk,NULL}'::regclass[])",
		// BOOL defaults cannot be deferred by the scalar regclass default path.
		"CREATE TABLE pc_rr_inline(ok BOOL DEFAULT ('{pc_rr_key,pc_rr_mid_k_b_key,pc_rr_mid_pk}'::regclass[] IS NOT NULL),CONSTRAINT pc_rr_inline_ck CHECK ('{pc_rr_key,pc_rr_mid_k_b_key}'::regclass[] IS NOT NULL))",
		// Inheritance participants retain inline defaults and CHECK literals.
		"CREATE TABLE pc_rr_parent(r REGCLASS DEFAULT 'pc_rr_key'::regclass,a REGCLASS[] DEFAULT '{pc_rr_mid_k_b_key,pc_rr_mid_pk,NULL}'::regclass[],CONSTRAINT pc_rr_parent_ck CHECK ('{pc_rr_key,pc_rr_mid_k_b_key}'::regclass[] IS NOT NULL))",
		"CREATE TABLE pc_rr_child() INHERITS(pc_rr_parent)",
		"CREATE VIEW pc_rr_scalar_view AS SELECT 'pc_rr_key'::regclass AS r,'pc_rr_mid_k_b_key'::regclass AS m,'pc_rr_mid_pk'::regclass AS local_key",
		"CREATE VIEW pc_rr_opaque_view AS SELECT '{pc_rr_key,pc_rr_mid_k_b_key,pc_rr_mid_pk,NULL}'::regclass[] AS a,NULL::regclass[] AS null_a",
	} {
		run(fixture.Exec(query, 0))
	}
	for _, table := range []string{"pc_rr_defaults", "pc_rr_inline", "pc_rr_child"} {
		run(fixture.Exec("INSERT INTO "+table+" DEFAULT VALUES", 1))
	}
	run(fixture.Exec("INSERT INTO pc_rr_root VALUES(1,2)", 1))
	run(fixture.Select("SELECT r,m,a FROM pc_rr_defaults", []string{"r", "m", "a"}, []string{"REGCLASS", "REGCLASS", "_REGCLASS"}, [][]any{{"pc_rr_key", "pc_rr_mid_k_b_key", "{pc_rr_key,pc_rr_mid_k_b_key,pc_rr_mid_pk,NULL}"}}))
	run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pc_rr_leaf'::regclass::oid AND (contype='u' OR contype='p') ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"pc_rr_leaf_k_b_key"}, {"pc_rr_leaf_pkey"}}))
}

func partitionRegclassRename(run func(fixture.Step)) {
	for _, query := range []string{
		"ALTER INDEX pc_rr_key RENAME TO pc_rr_root_final",
		"ALTER INDEX pc_rr_mid_k_b_key RENAME TO pc_rr_mid_final",
		"ALTER INDEX pc_rr_mid_pk RENAME TO pc_rr_local_final",
	} {
		run(fixture.Exec(query, 0))
	}
}

func partitionRegclassChecks(t *testing.T, run func(fixture.Step), pg bool) {
	t.Helper()
	values := fixture.Select("SELECT r,m,a FROM pc_rr_defaults", []string{"r", "m", "a"}, []string{"REGCLASS", "REGCLASS", "_REGCLASS"}, [][]any{{"pc_rr_root_final", "pc_rr_mid_final", "{pc_rr_root_final,pc_rr_mid_final,pc_rr_local_final,NULL}"}})
	values.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			scan := reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
			length, lengthKnown := column.Length()
			precision, scale, decimalKnown := column.DecimalSize()
			nullable, nullKnown := column.Nullable()
			if column.ScanType() != scan || length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown || (pg && (nullable || nullKnown)) || (!pg && (!nullable || !nullKnown)) {
				t.Fatalf("unexpected stored regclass metadata for %s: %v", column.Name(), column)
			}
		}
	}
	run(values)
	empty := fixture.Select("SELECT r,m,a FROM pc_rr_defaults WHERE FALSE", values.Columns, values.Types, nil)
	empty.CheckMetadata = values.CheckMetadata
	run(empty)
	run(fixture.Select("SELECT r::oid='pc_rr_root_final'::regclass::oid AS root_same,m::oid='pc_rr_mid_final'::regclass::oid AS mid_same,a[1]::oid='pc_rr_root_final'::regclass::oid AND a[2]::oid='pc_rr_mid_final'::regclass::oid AND a[3]::oid='pc_rr_local_final'::regclass::oid AND a[4] IS NULL AS opaque_same FROM pc_rr_defaults", []string{"root_same", "mid_same", "opaque_same"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	run(fixture.Select("SELECT r,m,local_key FROM pc_rr_scalar_view", []string{"r", "m", "local_key"}, []string{"REGCLASS", "REGCLASS", "REGCLASS"}, [][]any{{"pc_rr_root_final", "pc_rr_mid_final", "pc_rr_local_final"}}))
	run(fixture.Select("SELECT r::oid='pc_rr_root_final'::regclass::oid AS root_same,m::oid='pc_rr_mid_final'::regclass::oid AS mid_same,local_key::oid='pc_rr_local_final'::regclass::oid AS local_same FROM pc_rr_scalar_view", []string{"root_same", "mid_same", "local_same"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	run(fixture.Select("SELECT a,null_a FROM pc_rr_opaque_view", []string{"a", "null_a"}, []string{"_REGCLASS", "_REGCLASS"}, [][]any{{"{pc_rr_root_final,pc_rr_mid_final,pc_rr_local_final,NULL}", nil}}))
	run(fixture.Select("SELECT a[1]::oid='pc_rr_root_final'::regclass::oid AND a[2]::oid='pc_rr_mid_final'::regclass::oid AND a[3]::oid='pc_rr_local_final'::regclass::oid AND a[4] IS NULL AS same FROM pc_rr_opaque_view", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	run(fixture.Select("SELECT ok FROM pc_rr_inline", []string{"ok"}, []string{"BOOL"}, [][]any{{true}}))
	run(fixture.Select("SELECT r,a FROM pc_rr_child", []string{"r", "a"}, []string{"REGCLASS", "_REGCLASS"}, [][]any{{"pc_rr_root_final", "{pc_rr_mid_final,pc_rr_local_final,NULL}"}}))
	run(fixture.Select("SELECT r::oid='pc_rr_root_final'::regclass::oid AS same,a[1]::oid='pc_rr_mid_final'::regclass::oid AND a[2]::oid='pc_rr_local_final'::regclass::oid AND a[3] IS NULL AS opaque_same FROM pc_rr_child", []string{"same", "opaque_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	run(fixture.Select("SELECT conname,conislocal,coninhcount FROM pg_constraint WHERE conname='pc_rr_parent_ck' ORDER BY conislocal", []string{"conname", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"pc_rr_parent_ck", false, int64(1)}, {"pc_rr_parent_ck", true, int64(0)}}))
	for _, row := range []struct {
		name, table, parent, kind string
		local                     bool
		count                     int64
		primary                   bool
	}{
		{"pc_rr_root_final", "pc_rr_root", "", "I", true, 0, false},
		{"pc_rr_mid_final", "pc_rr_mid", "pc_rr_root_final", "I", false, 1, false},
		{"pc_rr_local_final", "pc_rr_mid", "", "I", true, 0, true},
		{"pc_rr_leaf_k_b_key", "pc_rr_leaf", "pc_rr_mid_final", "i", false, 1, false},
		{"pc_rr_leaf_pkey", "pc_rr_leaf", "pc_rr_local_final", "i", false, 1, true},
	} {
		run(fixture.Select("SELECT c.conname,c.conindid=r.oid AS backing,r.relkind,i.indisprimary,i.indisvalid,c.conislocal,c.coninhcount FROM pg_constraint c JOIN pg_class r ON r.oid=c.conindid AND r.relname=c.conname JOIN pg_index i ON i.indexrelid=r.oid AND i.indrelid=c.conrelid WHERE c.conname='"+row.name+"' AND c.conrelid='"+row.table+"'::regclass::oid", []string{"conname", "backing", "relkind", "indisprimary", "indisvalid", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "CHAR", "BOOL", "BOOL", "BOOL", "INT2"}, [][]any{{row.name, true, row.kind, row.primary, true, row.local, row.count}}))
		if row.parent == "" {
			run(fixture.Select("SELECT conparentid=0::oid AS no_parent FROM pg_constraint WHERE conname='"+row.name+"'", []string{"no_parent"}, []string{"BOOL"}, [][]any{{true}}))
		} else {
			run(fixture.Select("SELECT c.conparentid=p.oid AS immediate FROM pg_constraint c JOIN pg_constraint p ON p.oid=c.conparentid WHERE c.conname='"+row.name+"' AND p.conname='"+row.parent+"'", []string{"immediate"}, []string{"BOOL"}, [][]any{{true}}))
			run(partitionConstraintEdge(row.name, row.parent, row.table))
		}
	}
	run(fixture.Select("SELECT k,b FROM pc_rr_root", []string{"k", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
	for _, failure := range []struct{ query, column, row string }{
		{"INSERT INTO pc_rr_root VALUES(NULL,2)", "k", "null, 2"},
		{"INSERT INTO pc_rr_mid VALUES(1,NULL)", "b", "1, null"},
	} {
		run(referenceRangeError(failure.query, "23502", "null value in column \""+failure.column+"\" of relation \"pc_rr_leaf\" violates not-null constraint", fixture.Diagnostic{Schema: "public", Table: "pc_rr_leaf", Column: failure.column, Detail: "Failing row contains (" + failure.row + ")."}))
		run(fixture.Select("SELECT k,b FROM pc_rr_root", []string{"k", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
	}
}

// Future execution verifies captured definitions, not just their displayed text.
// Delete the extra rows so each canonical dump cycle observes identical data.
func partitionRegclassFuture(run func(fixture.Step)) {
	for _, table := range []string{"pc_rr_defaults", "pc_rr_inline", "pc_rr_child"} {
		run(fixture.Exec("INSERT INTO "+table+" DEFAULT VALUES", 1))
		run(fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	}
	run(fixture.Select("SELECT r,m,a FROM pc_rr_defaults", []string{"r", "m", "a"}, []string{"REGCLASS", "REGCLASS", "_REGCLASS"}, [][]any{{"pc_rr_root_final", "pc_rr_mid_final", "{pc_rr_root_final,pc_rr_mid_final,pc_rr_local_final,NULL}"}, {"pc_rr_root_final", "pc_rr_mid_final", "{pc_rr_root_final,pc_rr_mid_final,pc_rr_local_final,NULL}"}}))
	run(fixture.Select("SELECT r::oid='pc_rr_root_final'::regclass::oid AS root_same,m::oid='pc_rr_mid_final'::regclass::oid AS mid_same,a[1]::oid='pc_rr_root_final'::regclass::oid AND a[2]::oid='pc_rr_mid_final'::regclass::oid AND a[3]::oid='pc_rr_local_final'::regclass::oid AND a[4] IS NULL AS opaque_same FROM pc_rr_defaults", []string{"root_same", "mid_same", "opaque_same"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {true, true, true}}))
	run(fixture.Select("SELECT r,a FROM pc_rr_child", []string{"r", "a"}, []string{"REGCLASS", "_REGCLASS"}, [][]any{{"pc_rr_root_final", "{pc_rr_mid_final,pc_rr_local_final,NULL}"}, {"pc_rr_root_final", "{pc_rr_mid_final,pc_rr_local_final,NULL}"}}))
	run(fixture.Select("SELECT ok FROM pc_rr_inline", []string{"ok"}, []string{"BOOL"}, [][]any{{true}, {true}}))
	for _, table := range []string{"pc_rr_defaults", "pc_rr_inline", "pc_rr_child"} {
		run(fixture.Exec("DELETE FROM "+table, 2))
		run(fixture.Exec("INSERT INTO "+table+" DEFAULT VALUES", 1))
	}
}

func partitionRegclassIDs(t *testing.T, ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, renamed bool) map[string]int64 {
	t.Helper()
	ids := make(map[string]int64)
	seen := make(map[int64]string)
	for _, pair := range [][2]string{{"root", "pc_rr_root"}, {"mid", "pc_rr_mid"}, {"leaf", "pc_rr_leaf"}, {"root_key", "pc_rr_key"}, {"mid_key", "pc_rr_mid_k_b_key"}, {"local_key", "pc_rr_mid_pk"}, {"leaf_key", "pc_rr_leaf_k_b_key"}, {"leaf_pk", "pc_rr_leaf_pkey"}} {
		name := pair[1]
		if renamed {
			switch pair[0] {
			case "root_key":
				name = "pc_rr_root_final"
			case "mid_key":
				name = "pc_rr_mid_final"
			case "local_key":
				name = "pc_rr_local_final"
			}
		}
		var id int64
		if err := db.QueryRowContext(ctx, "SELECT oid::bigint FROM pg_class WHERE relname='"+name+"'").Scan(&id); err != nil {
			t.Fatal(name, err)
		}
		ids[pair[0]] = id
		if pair[0] != "root" && pair[0] != "mid" && pair[0] != "leaf" {
			if err := db.QueryRowContext(ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='"+name+"'").Scan(&id); err != nil {
				t.Fatal(name, err)
			}
			ids["constraint:"+pair[0]] = id
		}
	}
	for name, id := range ids {
		if id <= 0 || seen[id] != "" {
			t.Fatalf("identities must be positive and distinct: %s=%d aliases %s", name, id, seen[id])
		}
		seen[id] = name
	}
	return ids
}

func partitionRegclassRequireIDs(t *testing.T, want, got map[string]int64) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("constraint/backing identities changed: got %v want %v", got, want)
	}
}

func TestReferencePartitionConstraintsRegclassControls(t *testing.T) {
	partitionConstraintCase(t, "stored_virtual_backings", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionRegclassSetup(run)
		partitionRegclassRename(run)
		partitionRegclassChecks(t, run, b.PG)
		if b.PG {
			partitionRegclassFuture(run)
			return // PostgreSQL has no process-local native Restore API.
		}
		ids := partitionRegclassIDs(t, p.Ctx, b.DB, true)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal("actual virtual-backing dependency Dump", err)
		}
		for cycle := 1; cycle <= 2; cycle++ {
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatalf("actual virtual-backing Restore cycle %d: %v", cycle, err)
			}
			partitionRegclassRequireIDs(t, ids, partitionRegclassIDs(t, p.Ctx, b.DB, true))
			partitionRegclassChecks(t, run, false)
			partitionRegclassFuture(run)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatalf("virtual-backing cycle %d changed exact dump/redump bytes\nsource:\n%s\nrestored:\n%s", cycle, dump.Bytes(), redump.Bytes())
			}
		}
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, query := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := oracle.ExecContext(ctx, query); err != nil {
						t.Error(err)
					}
				}
			}()
			// Replay the complete unmodified emitted stream, including its initial
			// private names and final renames. No name rewrite or statement filter.
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL rejected actual virtual-backing emitted SQL", err)
			}
			apply := func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, s)
			}
			partitionRegclassIDs(t, p.Ctx, oracle, true)
			partitionRegclassChecks(t, apply, true)
			partitionRegclassFuture(apply)
		}
	})
}
