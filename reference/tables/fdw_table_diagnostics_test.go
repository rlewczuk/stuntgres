package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Each declaration has its own scenario so an unknown oracle subform cannot
// prevent discovery of the other diagnostics. Positions denote SQL characters.
func TestReferenceFDWTableConstraintDiagnostics(t *testing.T) {
	cases := []struct {
		name, sql, keyword, kind string
	}{
		{"column_primary", "CREATE FOREIGN TABLE fdw_rejected(id integer PRIMARY KEY) SERVER fdw_server", "PRIMARY", "primary key"},
		{"column_named_primary", "CREATE FOREIGN TABLE fdw_rejected(id integer CONSTRAINT named_pk PRIMARY KEY) SERVER fdw_server", "CONSTRAINT", "primary key"},
		{"table_primary", "CREATE FOREIGN TABLE fdw_rejected(id integer,PRIMARY KEY(id)) SERVER fdw_server", "PRIMARY", "primary key"},
		{"table_named_primary", "CREATE FOREIGN TABLE fdw_rejected(id integer,CONSTRAINT named_pk PRIMARY KEY(id)) SERVER fdw_server", "CONSTRAINT", "primary key"},
		{"column_unique", "CREATE FOREIGN TABLE fdw_rejected(id integer UNIQUE) SERVER fdw_server", "UNIQUE", "unique"},
		{"column_named_unique", "CREATE FOREIGN TABLE fdw_rejected(id integer CONSTRAINT named_uq UNIQUE) SERVER fdw_server", "CONSTRAINT", "unique"},
		{"table_unique", "CREATE FOREIGN TABLE fdw_rejected(id integer,UNIQUE(id)) SERVER fdw_server", "UNIQUE", "unique"},
		{"table_named_unique", "CREATE FOREIGN TABLE fdw_rejected(id integer,CONSTRAINT named_uq UNIQUE(id)) SERVER fdw_server", "CONSTRAINT", "unique"},
		{"column_references", "CREATE FOREIGN TABLE fdw_rejected(id integer REFERENCES fdw_key(id)) SERVER fdw_server", "REFERENCES", "foreign key"},
		{"column_named_references", "CREATE FOREIGN TABLE fdw_rejected(id integer CONSTRAINT named_fk REFERENCES fdw_key(id)) SERVER fdw_server", "CONSTRAINT", "foreign key"},
		{"table_foreign_key", "CREATE FOREIGN TABLE fdw_rejected(id integer,FOREIGN KEY(id) REFERENCES fdw_key(id)) SERVER fdw_server", "FOREIGN KEY", "foreign key"},
		{"table_named_foreign_key", "CREATE FOREIGN TABLE fdw_rejected(id integer,CONSTRAINT named_fk FOREIGN KEY(id) REFERENCES fdw_key(id)) SERVER fdw_server", "CONSTRAINT", "foreign key"},
		{"table_exclusion", "CREATE FOREIGN TABLE fdw_rejected(id integer,EXCLUDE USING btree(id WITH =)) SERVER fdw_server", "EXCLUDE", "exclusion"},
		{"table_named_exclusion", "CREATE FOREIGN TABLE fdw_rejected(id integer,CONSTRAINT named_ex EXCLUDE USING btree(id WITH =)) SERVER fdw_server", "CONSTRAINT", "exclusion"},
		{"unicode_named_unique", `CREATE FOREIGN TABLE "é_rejected"(id integer CONSTRAINT "clé" UNIQUE) SERVER fdw_server`, "CONSTRAINT", "unique"},
		{"identity_then_unique", "CREATE FOREIGN TABLE fdw_rejected(id integer GENERATED ALWAYS AS IDENTITY,other integer UNIQUE) SERVER fdw_server", "UNIQUE", "unique"},
		{"alter_column_primary", "ALTER FOREIGN TABLE fdw_altered ADD COLUMN extra integer PRIMARY KEY", "PRIMARY", "primary key"},
		{"alter_column_unique", "ALTER FOREIGN TABLE fdw_altered ADD COLUMN extra integer CONSTRAINT named_uq UNIQUE", "CONSTRAINT", "unique"},
		{"alter_column_references", "ALTER FOREIGN TABLE fdw_altered ADD COLUMN extra integer REFERENCES fdw_key(id)", "REFERENCES", "foreign key"},
		{"alter_table_primary", "ALTER FOREIGN TABLE fdw_altered ADD CONSTRAINT named_pk PRIMARY KEY(id)", "CONSTRAINT", "primary key"},
		{"alter_table_unique", "ALTER FOREIGN TABLE fdw_altered ADD CONSTRAINT named_uq UNIQUE(id)", "CONSTRAINT", "unique"},
		{"alter_table_foreign_key", "ALTER FOREIGN TABLE fdw_altered ADD CONSTRAINT named_fk FOREIGN KEY(id) REFERENCES fdw_key(id)", "CONSTRAINT", "foreign key"},
		{"alter_table_exclusion", "ALTER FOREIGN TABLE fdw_altered ADD CONSTRAINT named_ex EXCLUDE USING btree(id WITH =)", "CONSTRAINT", "exclusion"},
		{"alter_later_failure", "ALTER FOREIGN TABLE fdw_altered ADD COLUMN earlier integer,ADD COLUMN extra integer UNIQUE", "UNIQUE", "unique"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
						fdwMetadata(run)
						run(fixture.Exec("CREATE TABLE fdw_key(id integer PRIMARY KEY)", 0))
						run(fixture.Exec("CREATE FOREIGN TABLE fdw_altered(id integer OPTIONS(tag 'original')) SERVER fdw_server OPTIONS(tag 'original')", 0))
						want := fdwDiagnostic("0A000", tc.kind+" constraints are not supported on foreign tables")
						want.Position = fixture.Position(utf8.RuneCountInString(tc.sql[:strings.LastIndex(tc.sql, tc.keyword)]) + 1)
						step := fixture.ErrorDiagnostic(tc.sql, want)
						step.Prepared, step.ErrorStage = prepared, "execute"
						run(step)
						run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('fdw_rejected','é_rejected','fdw_rejected_id_seq')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						run(fixture.Select("SELECT attname,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_altered'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"id", "{tag=original}"}}))
						run(fixture.Select("SELECT ftoptions::text AS options FROM pg_foreign_table WHERE ftrelid='fdw_altered'::regclass::oid", []string{"options"}, []string{"TEXT"}, [][]any{{"{tag=original}"}}))
						run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='fdw_altered'::regclass::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					})
				})
			}
		})
	}
}
