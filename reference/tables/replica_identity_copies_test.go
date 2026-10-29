package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func replicaCopyFlags(table string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT c.relname,i.indisreplident FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='"+table+"'::regclass ORDER BY c.relname", []string{"relname", "indisreplident"}, []string{"NAME", "BOOL"}, rows)
}

// Exercise the actual persistent bytes, not a second reconstruction of setup.
// Shared setup already executes LIKE/partition construction on both backends.
func replicaCopyRoundTrip(t *testing.T, p *fixture.Pair, root, drop string, check func()) {
	t.Helper()
	var original bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &original); err != nil {
		t.Fatal("native cross-parent Dump", err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal("native cross-parent Reset", err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT relname FROM pg_class WHERE relname='"+root+"'", []string{"relname"}, []string{"NAME"}, nil))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(original.Bytes())); err != nil {
		t.Fatalf("native cross-parent Restore: %v\nactual dump stream:\n%s", err, original.String())
	}
	if p.Postgres != nil {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(drop, 0))
		if _, err := p.Postgres.ExecContext(p.Ctx, original.String()); err != nil {
			t.Fatal("actual cross-parent native bytes PostgreSQL replay", err)
		}
	}
	check()
	var restored bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &restored); err != nil || restored.String() != original.String() {
		t.Fatal("cross-parent identity replay was not canonical", err, restored.String())
	}
}

func TestReferenceReplicaIdentityCrossParentCopies(t *testing.T) {
	t.Run("like", func(t *testing.T) {
		p := fixture.NewScenario(t)
		run := func(s fixture.Step) { p.Step(t, s) }
		for _, q := range []string{
			"CREATE TABLE ri_source(k INT NOT NULL,p TEXT,CONSTRAINT ri_source_u UNIQUE(k))",
			"CREATE UNIQUE INDEX ri_source_key ON ri_source(k)",
			"ALTER TABLE ri_source REPLICA IDENTITY USING INDEX ri_source_key",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ri_source VALUES(1,'source')", 1))
		source := func(selected string) {
			run(replicaMode("ri_source", "i"))
			run(replicaCopyFlags("ri_source", [][]any{{"ri_source_key", selected == "ri_source_key"}, {"ri_source_u", selected == "ri_source_u"}}))
			run(fixture.Select("SELECT k,p FROM ri_source", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "source"}}))
			run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='ri_source'::regclass AND attname='k'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}))
		}
		source("ri_source_key")
		run(fixture.Exec("CREATE TABLE ri_like_plain(LIKE ri_source INCLUDING ALL)", 0))
		run(replicaMode("ri_like_plain", "d"))
		run(replicaCopyFlags("ri_like_plain", [][]any{{"ri_like_plain_k_idx", false}, {"ri_like_plain_k_key", false}}))
		source("ri_source_key")
		run(fixture.Exec("ALTER TABLE ri_source REPLICA IDENTITY USING INDEX ri_source_u", 0))
		run(fixture.Prepared(fixture.Exec("CREATE TABLE ri_like_attached(LIKE ri_source INCLUDING ALL)", 0)))
		run(replicaMode("ri_like_attached", "d"))
		run(replicaCopyFlags("ri_like_attached", [][]any{{"ri_like_attached_k_idx", false}, {"ri_like_attached_k_key", false}}))
		source("ri_source_u")
		for _, table := range []string{"ri_like_plain", "ri_like_attached"} {
			// Neither destination selected an identity; UNIQUE is not PRIMARY KEY.
			run(fixture.Exec("ALTER TABLE "+table+" ALTER k DROP NOT NULL", 0))
			run(fixture.Exec("INSERT INTO "+table+" VALUES(NULL,'copy')", 1))
		}
		check := func() {
			source("ri_source_u")
			for _, table := range []string{"ri_like_plain", "ri_like_attached"} {
				run(replicaMode(table, "d"))
				run(replicaCopyFlags(table, [][]any{{table + "_k_idx", false}, {table + "_k_key", false}}))
				step := fixture.Select("SELECT k,p FROM "+table, []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{nil, "copy"}})
				run(step)
				run(fixture.Prepared(step))
				run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='"+table+"'::regclass AND attname='k'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{false}}))
			}
		}
		check()
		replicaCopyRoundTrip(t, p, "ri_source", "DROP TABLE ri_source,ri_like_plain,ri_like_attached", check)
	})
	t.Run("like_primary", func(t *testing.T) {
		p := fixture.NewScenario(t)
		run := func(s fixture.Step) { p.Step(t, s) }
		run(fixture.Exec("CREATE TABLE ri_pk_source(k INT PRIMARY KEY,p TEXT)", 0))
		run(fixture.Exec("ALTER TABLE ri_pk_source REPLICA IDENTITY USING INDEX ri_pk_source_pkey", 0))
		run(fixture.Exec("INSERT INTO ri_pk_source VALUES(1,'source')", 1))
		source := func() {
			run(replicaMode("ri_pk_source", "i"))
			run(replicaCopyFlags("ri_pk_source", [][]any{{"ri_pk_source_pkey", true}}))
			step := fixture.Select("SELECT k,p FROM ri_pk_source", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "source"}})
			run(step)
			run(fixture.Prepared(step))
		}
		source()
		for _, table := range []string{"ri_pk_plain", "ri_pk_prepared"} {
			step := fixture.Exec("CREATE TABLE "+table+"(LIKE ri_pk_source INCLUDING ALL)", 0)
			if table == "ri_pk_prepared" {
				step = fixture.Prepared(step)
			}
			run(step)
			run(replicaMode(table, "d"))
			run(replicaCopyFlags(table, [][]any{{table + "_pkey", false}}))
			source()
			run(fixture.Exec("INSERT INTO "+table+" VALUES(2,'copy'),(3,NULL)", 2))
		}
		check := func() {
			source()
			for _, table := range []string{"ri_pk_plain", "ri_pk_prepared"} {
				run(replicaMode(table, "d"))
				run(replicaCopyFlags(table, [][]any{{table + "_pkey", false}}))
				step := fixture.Select("SELECT k,p FROM "+table+" ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "copy"}, {int64(3), nil}})
				run(step)
				run(fixture.Prepared(step))
				run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='"+table+"'::regclass AND attname='k'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{true}}))
			}
		}
		check()
		replicaCopyRoundTrip(t, p, "ri_pk_source", "DROP TABLE ri_pk_source,ri_pk_plain,ri_pk_prepared", check)
	})
	t.Run("partitions", func(t *testing.T) {
		p := fixture.NewScenario(t)
		run := func(s fixture.Step) { p.Step(t, s) }
		for _, q := range []string{
			"CREATE TABLE ri_parent(k INT NOT NULL,p TEXT) PARTITION BY RANGE(k)",
			"CREATE UNIQUE INDEX ri_parent_key ON ri_parent(k)",
			"ALTER TABLE ri_parent REPLICA IDENTITY USING INDEX ri_parent_key",
			"CREATE TABLE ri_created PARTITION OF ri_parent FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE ri_attach_new(k INT NOT NULL,p TEXT)",
			"ALTER TABLE ri_parent ATTACH PARTITION ri_attach_new FOR VALUES FROM(10) TO(20)",
			"CREATE TABLE ri_adopt(k INT NOT NULL,p TEXT)",
			"CREATE UNIQUE INDEX ri_adopt_key ON ri_adopt(k)",
			"ALTER TABLE ri_adopt REPLICA IDENTITY USING INDEX ri_adopt_key",
		} {
			run(fixture.Exec(q, 0))
		}
		run(replicaMode("ri_parent", "i"))
		run(replicaCopyFlags("ri_parent", [][]any{{"ri_parent_key", true}}))
		run(replicaMode("ri_created", "d"))
		run(replicaCopyFlags("ri_created", [][]any{{"ri_created_k_idx", false}}))
		run(replicaMode("ri_attach_new", "d"))
		run(replicaCopyFlags("ri_attach_new", [][]any{{"ri_attach_new_k_idx", false}}))
		for _, backend := range p.Backends() {
			var before, after int64
			if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ri_adopt_key'").Scan(&before); err != nil || before <= 0 {
				t.Fatal("existing child index identity", backend.Name, before, err)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("ALTER TABLE ri_parent ATTACH PARTITION ri_adopt FOR VALUES FROM(20) TO(30)", 0)))
			if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ri_adopt_key'").Scan(&after); err != nil || after != before {
				t.Fatal("adoption replaced the independently selected index", backend.Name, before, after, err)
			}
		}
		run(fixture.Exec("INSERT INTO ri_parent VALUES(1,'created'),(11,'attached'),(21,'adopted')", 3))
		check := func() {
			for _, tc := range []struct {
				table, mode, index string
				selected           bool
			}{
				{"ri_parent", "i", "ri_parent_key", true},
				{"ri_created", "d", "ri_created_k_idx", false},
				{"ri_attach_new", "d", "ri_attach_new_k_idx", false},
				{"ri_adopt", "i", "ri_adopt_key", true},
			} {
				run(replicaMode(tc.table, tc.mode))
				run(replicaCopyFlags(tc.table, [][]any{{tc.index, tc.selected}}))
			}
			run(fixture.Select("SELECT c.relname,p.relname AS parent FROM pg_inherits h JOIN pg_class c ON c.oid=h.inhrelid JOIN pg_class p ON p.oid=h.inhparent WHERE p.relname='ri_parent_key' ORDER BY c.relname", []string{"relname", "parent"}, []string{"NAME", "NAME"}, [][]any{{"ri_adopt_key", "ri_parent_key"}, {"ri_attach_new_k_idx", "ri_parent_key"}, {"ri_created_k_idx", "ri_parent_key"}}))
			step := fixture.Select("SELECT k,p FROM ri_parent ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "created"}, {int64(11), "attached"}, {int64(21), "adopted"}})
			run(step)
			run(fixture.Prepared(step))
		}
		check()
		replicaCopyRoundTrip(t, p, "ri_parent", "DROP TABLE ri_parent CASCADE", check)
	})
}
