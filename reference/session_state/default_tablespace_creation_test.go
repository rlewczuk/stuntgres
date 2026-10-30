package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func defaultTablespaceGlobal() fixture.Diagnostic {
	return physicalDiagnostic("22023", "only shared relations can be placed in pg_global tablespace")
}

func defaultTablespacePartition() fixture.Diagnostic {
	return physicalDiagnostic("0A000", "cannot specify default tablespace for partitioned relations")
}

func (s *physicalCase) tablespaceAbsent(catalog, column, name string) {
	s.run(fixture.Select("SELECT count(*) FROM "+catalog+" WHERE "+column+"="+physicalLiteral(name), []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func (s *physicalCase) tablespaceSentinel() {
	s.run(fixture.Select("SELECT v FROM ts_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
}

// Each owner has an independent successful empty-default control. In particular,
// ALTER and explicit-index cases cannot pass by relying on CREATE TABLE's guard.
func TestReferenceDefaultTablespaceCreationEntrypoints(t *testing.T) {
	cases := []struct {
		name string
		setup []string
		query string
		count int64
		absent []string
	}{
		{"table", nil, "CREATE TABLE ts_new(v INT)", 0, []string{"ts_new"}},
		{"unlogged", nil, "CREATE UNLOGGED TABLE ts_new(v INT)", 0, []string{"ts_new"}},
		{"typed", []string{"CREATE TYPE ts_pair AS(v INT)"}, "CREATE TABLE ts_new OF ts_pair", 0, []string{"ts_new"}},
		{"inherited", []string{"CREATE TABLE ts_parent(v INT)"}, "CREATE TABLE ts_new() INHERITS(ts_parent)", 0, []string{"ts_new"}},
		{"schema", nil, "CREATE SCHEMA ts_schema CREATE TABLE ts_new(v INT)", 0, []string{"ts_new"}},
		{"ctas", nil, "CREATE TABLE ts_new AS SELECT v FROM ts_sentinel", 1, []string{"ts_new"}},
		{"ctas-no-data", nil, "CREATE TABLE ts_new AS SELECT v FROM ts_sentinel WITH NO DATA", 0, []string{"ts_new"}},
		{"select-into", nil, "SELECT v INTO ts_new FROM ts_sentinel", 1, []string{"ts_new"}},
		{"matview", nil, "CREATE MATERIALIZED VIEW ts_new AS SELECT v FROM ts_sentinel", 1, []string{"ts_new"}},
		{"matview-no-data", nil, "CREATE MATERIALIZED VIEW ts_new AS SELECT v FROM ts_sentinel WITH NO DATA", 0, []string{"ts_new"}},
		{"partition-root", nil, "CREATE TABLE ts_new(v INT) PARTITION BY RANGE(v)", 0, []string{"ts_new"}},
		{"partition-leaf", []string{"CREATE TABLE ts_parent(v INT) PARTITION BY RANGE(v)"}, "CREATE TABLE ts_new PARTITION OF ts_parent DEFAULT", 0, []string{"ts_new"}},
		{"subpartition", []string{"CREATE TABLE ts_parent(v INT) PARTITION BY RANGE(v)"}, "CREATE TABLE ts_new PARTITION OF ts_parent DEFAULT PARTITION BY RANGE(v)", 0, []string{"ts_new"}},
		{"index", nil, "CREATE INDEX ts_new ON ts_sentinel(v)", 0, []string{"ts_new"}},
		{"unique-index", nil, "CREATE UNIQUE INDEX ts_new ON ts_sentinel(v)", 0, []string{"ts_new"}},
		{"concurrent-index", nil, "CREATE INDEX CONCURRENTLY ts_new ON ts_sentinel(v)", 0, []string{"ts_new"}},
		{"matview-index", []string{"CREATE MATERIALIZED VIEW ts_mv AS SELECT v FROM ts_sentinel WITH NO DATA"}, "CREATE INDEX ts_new ON ts_mv(v)", 0, []string{"ts_new"}},
		{"partition-index-children", []string{"CREATE TABLE ts_parent(v INT) PARTITION BY RANGE(v)", "CREATE TABLE ts_leaf PARTITION OF ts_parent DEFAULT"}, "CREATE UNIQUE INDEX ts_new ON ts_parent(v)", 0, []string{"ts_new", "ts_leaf_v_idx"}},
		{"create-primary", nil, "CREATE TABLE ts_new(v INT PRIMARY KEY)", 0, []string{"ts_new", "ts_new_pkey"}},
		{"create-unique", nil, "CREATE TABLE ts_new(v INT UNIQUE)", 0, []string{"ts_new", "ts_new_v_key"}},
		{"alter-primary", nil, "ALTER TABLE ts_sentinel ADD CONSTRAINT ts_new PRIMARY KEY(v)", 0, []string{"ts_new"}},
		{"alter-unique", nil, "ALTER TABLE ts_sentinel ADD CONSTRAINT ts_new UNIQUE(v)", 0, []string{"ts_new"}},
		{"alter-exclude", nil, "ALTER TABLE ts_sentinel ADD CONSTRAINT ts_new EXCLUDE USING btree(v WITH =)", 0, []string{"ts_new"}},
		{"create-exclude", nil, "CREATE TABLE ts_new(v INT, EXCLUDE USING btree(v WITH =))", 0, []string{"ts_new", "ts_new_v_excl"}},
		{"like-indexes", []string{"CREATE TABLE ts_like(v INT UNIQUE)"}, "CREATE TABLE ts_new(LIKE ts_like INCLUDING INDEXES)", 0, []string{"ts_new", "ts_new_v_key"}},
		{"sequence", nil, "CREATE SEQUENCE ts_new", 0, []string{"ts_new"}},
		{"view", nil, "CREATE VIEW ts_new AS SELECT v FROM ts_sentinel", 0, []string{"ts_new"}},
		{"new-replace-view", nil, "CREATE OR REPLACE VIEW ts_new AS SELECT v FROM ts_sentinel", 0, []string{"ts_new"}},
		{"composite", nil, "CREATE TYPE ts_new AS(v INT)", 0, []string{"ts_new"}},
		{"serial", nil, "CREATE TABLE ts_new(v SERIAL)", 0, []string{"ts_new", "ts_new_v_seq"}},
		{"identity", nil, "CREATE TABLE ts_new(v INT GENERATED ALWAYS AS IDENTITY)", 0, []string{"ts_new", "ts_new_v_seq"}},
		{"alter-add-identity", []string{"CREATE TABLE ts_identity(v INT NOT NULL)"}, "ALTER TABLE ts_identity ALTER v ADD GENERATED ALWAYS AS IDENTITY", 0, []string{"ts_identity_v_seq"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, global := range []bool{false, true} {
				name := "empty"
				if global { name = "global" }
				t.Run(name, func(t *testing.T) {
					physicalCases(t, nil, func(s *physicalCase) {
						if tc.name == "schema" {
							defer func() {
								if _, err := s.conn.ExecContext(s.p.Ctx, "DROP SCHEMA IF EXISTS ts_schema CASCADE"); err != nil { s.t.Error(err) }
							}()
						}
						s.run(fixture.Exec("CREATE TABLE ts_sentinel(v INT)", 0))
						s.run(fixture.Exec("INSERT INTO ts_sentinel VALUES(73)", 1))
						for _, q := range tc.setup { s.run(fixture.Exec(q, 0)) }
						if global {
							s.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
							s.run(physicalError(tc.query, defaultTablespaceGlobal(), false, false))
							for _, object := range tc.absent {
								s.tablespaceAbsent("pg_class", "relname", object)
								s.tablespaceAbsent("pg_type", "typname", object)
								s.tablespaceAbsent("pg_constraint", "conname", object)
							}
							if tc.name == "schema" { s.tablespaceAbsent("pg_namespace", "nspname", "ts_schema") }
							if tc.name == "alter-add-identity" {
								s.run(fixture.Select("SELECT attidentity::text,atthasdef FROM pg_attribute WHERE attrelid='ts_identity'::regclass AND attname='v'", []string{"attidentity", "atthasdef"}, []string{"TEXT", "BOOL"}, [][]any{{"", false}}))
								s.run(fixture.Select("SELECT count(*) FROM pg_attrdef WHERE adrelid='ts_identity'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							}
						} else { s.run(fixture.Exec(tc.query, tc.count)) }
						s.tablespaceSentinel()
					})
				})
			}
		})
	}
}

func TestReferenceDefaultTablespaceTemporaryAndPartitionDefaults(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE ts_parent(v INT) PARTITION BY RANGE(v)", 0))
		s.run(fixture.Exec("CREATE VIEW ts_existing AS SELECT 1 AS v", 0))
		s.run(fixture.Exec("SET default_tablespace='pg_global'", 0))
		for _, q := range []string{
			"CREATE TEMP TABLE ts_temp(v INT)", "CREATE INDEX ts_temp_idx ON ts_temp(v)",
			"CREATE TABLE pg_temp.ts_qualified(v INT)", "CREATE INDEX ts_qualified_idx ON pg_temp.ts_qualified(v)",
			"CREATE TEMP SEQUENCE ts_temp_seq", "CREATE TEMP VIEW ts_temp_view AS SELECT 1 AS v",
			"CREATE OR REPLACE VIEW ts_existing AS SELECT 2 AS v", "CREATE DOMAIN ts_domain AS INT", "CREATE TYPE ts_enum AS ENUM('one')",
		} { s.run(fixture.Exec(q, 0)) }
		s.run(fixture.Select("SELECT v FROM ts_existing", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
		s.run(fixture.Exec("INSERT INTO ts_temp VALUES(73)", 1))
		s.run(fixture.Exec("SET default_tablespace='pg_default'", 0))
		s.run(physicalError("CREATE TABLE ts_bad(v INT) PARTITION BY RANGE(v)", defaultTablespacePartition(), false, false))
		s.tablespaceAbsent("pg_class", "relname", "ts_bad")
		s.run(physicalError("CREATE TABLE ts_sub PARTITION OF ts_parent DEFAULT PARTITION BY RANGE(v)", defaultTablespacePartition(), false, false))
		s.tablespaceAbsent("pg_class", "relname", "ts_sub")
		s.run(fixture.Exec("CREATE TABLE ts_leaf PARTITION OF ts_parent DEFAULT", 0))
	})
}
