package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTempTablespacesCreationEntrypoints(t *testing.T) {
	cases := []struct {
		name string
		setup []string
		query string
		absent []string
	}{
		{"table", nil, "CREATE TEMP TABLE tt_new(v INT)", []string{"tt_new"}},
		{"qualified-table", nil, "CREATE TABLE pg_temp.tt_new(v INT)", []string{"tt_new"}},
		{"sequence", nil, "CREATE TEMP SEQUENCE tt_new", []string{"tt_new"}},
		{"view", nil, "CREATE TEMP VIEW tt_new AS SELECT v FROM tt_sentinel", []string{"tt_new"}},
		{"auto-temp-view", nil, "CREATE VIEW tt_new AS SELECT v FROM tt_sentinel", []string{"tt_new"}},
		{"new-replace-view", nil, "CREATE OR REPLACE TEMP VIEW tt_new AS SELECT v FROM tt_sentinel", []string{"tt_new"}},
		{"index", nil, "CREATE INDEX tt_new ON tt_sentinel(v)", []string{"tt_new"}},
		{"unique-index", nil, "CREATE UNIQUE INDEX tt_new ON tt_sentinel(v)", []string{"tt_new"}},
		{"create-primary", nil, "CREATE TEMP TABLE tt_new(v INT PRIMARY KEY)", []string{"tt_new", "tt_new_pkey"}},
		{"create-unique", nil, "CREATE TEMP TABLE tt_new(v INT UNIQUE)", []string{"tt_new", "tt_new_v_key"}},
		{"two-unique", nil, "CREATE TEMP TABLE tt_new(a INT UNIQUE,b INT UNIQUE)", []string{"tt_new", "tt_new_a_key", "tt_new_b_key"}},
		{"create-exclude", nil, "CREATE TEMP TABLE tt_new(v INT, EXCLUDE USING btree(v WITH =))", []string{"tt_new", "tt_new_v_excl"}},
		{"alter-primary", nil, "ALTER TABLE tt_sentinel ADD CONSTRAINT tt_new PRIMARY KEY(v)", []string{"tt_new"}},
		{"alter-unique", nil, "ALTER TABLE tt_sentinel ADD CONSTRAINT tt_new UNIQUE(v)", []string{"tt_new"}},
		{"alter-exclude", nil, "ALTER TABLE tt_sentinel ADD CONSTRAINT tt_new EXCLUDE USING btree(v WITH =)", []string{"tt_new"}},
		{"serial", nil, "CREATE TEMP TABLE tt_new(v SERIAL)", []string{"tt_new", "tt_new_v_seq"}},
		{"identity", nil, "CREATE TEMP TABLE tt_new(v INT GENERATED ALWAYS AS IDENTITY)", []string{"tt_new", "tt_new_v_seq"}},
		{"alter-identity", []string{"CREATE TEMP TABLE tt_identity(v INT NOT NULL)"}, "ALTER TABLE tt_identity ALTER v ADD GENERATED ALWAYS AS IDENTITY", []string{"tt_identity_v_seq"}},
		{"like-indexes", []string{"CREATE TEMP TABLE tt_like(v INT UNIQUE)"}, "CREATE TEMP TABLE tt_new(LIKE tt_like INCLUDING INDEXES)", []string{"tt_new", "tt_new_v_key"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared-%t", prepared), func(t *testing.T) {
					for _, global := range []bool{false, true} {
						t.Run(fmt.Sprintf("global-%t", global), func(t *testing.T) {
							physicalCases(t, nil, func(s *physicalCase) {
								s.run(fixture.Exec("CREATE TEMP TABLE tt_sentinel(v INT)", 0))
								s.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(73)", 1))
								for _, q := range tc.setup { s.run(fixture.Exec(q, 0)) }
								if global {
									s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
									s.run(physicalError(tc.query, defaultTablespaceGlobal(), prepared, false))
									s.tempTablespacesAbsent(tc.absent...)
									if tc.name == "alter-identity" {
										s.run(fixture.Select("SELECT attidentity::text,atthasdef FROM pg_attribute WHERE attrelid='tt_identity'::regclass AND attname='v'", []string{"attidentity", "atthasdef"}, []string{"TEXT", "BOOL"}, [][]any{{"", false}}))
										s.run(fixture.Select("SELECT count(*) FROM pg_attrdef WHERE adrelid='tt_identity'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
									}
								} else {
									step := fixture.Exec(tc.query, 0)
									step.Prepared = prepared
									s.run(step)
								}
								s.tempTablespacesSentinel()
							})
						})
					}
				})
			}
		})
	}
	t.Run("noncreating-paths", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			s.run(fixture.Exec("CREATE TEMP TABLE tt_sentinel(v INT)", 0))
			s.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(73)", 1))
			s.run(fixture.Exec("CREATE TEMP VIEW tt_existing AS SELECT v FROM tt_sentinel", 0))
			s.run(fixture.Exec("CREATE UNIQUE INDEX tt_attach ON tt_sentinel(v)", 0))
			s.run(fixture.Exec("SET temp_tablespaces='pg_global'", 0))
			s.run(fixture.Exec("CREATE OR REPLACE TEMP VIEW tt_existing AS SELECT v FROM tt_sentinel", 0))
			s.run(fixture.Exec("CREATE TEMP TABLE IF NOT EXISTS tt_sentinel(v INT)", 0))
			// PostgreSQL resolves/checks the index destination before the
			// IF NOT EXISTS skip, unlike the table's early skip above.
			s.run(physicalError("CREATE INDEX IF NOT EXISTS tt_attach ON tt_sentinel(v)", defaultTablespaceGlobal(), false, false))
			s.run(fixture.Exec("ALTER TABLE tt_sentinel ADD CONSTRAINT tt_attached UNIQUE USING INDEX tt_attach", 0))
			s.run(fixture.Exec("INSERT INTO tt_sentinel VALUES(74)", 1))
			s.run(fixture.Select("SELECT v FROM tt_existing ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}, {int64(74)}}))
		})
	})
}

func tempTablespacesListSelection(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		// Initialize the temporary namespace outside the measured transaction.
		s.run(fixture.Exec("CREATE TEMP TABLE tt_bootstrap(v INT)", 0))
		s.run(fixture.Exec("DROP TABLE tt_bootstrap", 0))
		s.run(fixture.Exec("BEGIN", 0))
		s.run(clientText("SELECT set_config('temp_tablespaces','pg_default, pg_global',false)", "set_config", "pg_default, pg_global"))
		var successes [6]bool
		for i := range successes {
			s.run(fixture.Exec("SAVEPOINT tt_attempt", 0))
			query := fmt.Sprintf("CREATE TEMP TABLE tt_cycle_%d(v INT)", i)
			result, err := s.conn.ExecContext(s.p.Ctx, query)
			if err == nil {
				n, e := result.RowsAffected()
				if e != nil || n != 0 { s.t.Fatalf("attempt %d affected=%d: %v", i, n, e) }
				successes[i] = true
			} else {
				d := defaultTablespaceGlobal()
				fixture.AssertDiagnostic(s.t, err, &d)
				s.run(fixture.Exec("ROLLBACK TO tt_attempt", 0))
			}
			s.run(fixture.Exec("RELEASE tt_attempt", 0))
		}
		// Initial phase is random, but circular advancement and failed-CREATE
		// consumption survive unchanged-setting savepoint rollback.
		for i := 1; i < len(successes); i++ {
			if successes[i] == successes[i-1] { s.t.Fatalf("expected alternating default/global selection, got %v", successes) }
		}
		for i, success := range successes {
			count := int64(0)
			if success { count = 1 }
			s.run(fixture.Select(fmt.Sprintf("SELECT count(*) FROM pg_class WHERE relnamespace=pg_my_temp_schema() AND relname='tt_cycle_%d'", i), []string{"count"}, []string{"INT8"}, [][]any{{count}}))
			if !success { s.tempTablespacesAbsent(fmt.Sprintf("tt_cycle_%d", i)) }
		}
		s.run(fixture.Exec("COMMIT", 0))
		s.tempTablespacesReads("pg_default, pg_global", "session")
		s.run(fixture.Exec("BEGIN", 0))
		s.run(clientText(`SELECT set_config('temp_tablespaces','pg_default, ""',false)`, "set_config", `pg_default, ""`))
		for i := range 4 { s.run(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE tt_duplicate_%d(v INT)", i), 0)) }
		s.run(fixture.Exec("COMMIT", 0))
	})
}

func TestReferenceTempTablespacesListSelection(t *testing.T) {
	t.Run("circular", tempTablespacesListSelection)
	t.Run("same-value-assignment", tempTablespacesSameValueAssignment)
}
