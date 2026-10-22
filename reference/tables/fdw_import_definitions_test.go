package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwImportSource(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	fdwLive(t, p, b, r, run)
	for _, q := range []string{
		"CREATE TABLE fdw_source.import_a(id integer NOT NULL,label varchar(7),amount numeric(6,2),flag boolean)",
		"CREATE TABLE fdw_source.import_b(key bigint NOT NULL,note text)",
	} {
		fdwSourceExec(t, p, b, r, run, q, 0)
	}
	fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.import_a VALUES(1,'alpha',12.34,true),(2,NULL,NULL,false)", 2)
	fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.import_b VALUES(9,'kept')", 1)
	run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
}

const fdwImportBagSQL = "SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' ORDER BY c.relname"

func fdwImportBag(run func(fixture.Step), names ...string) {
	rows := make([][]any, 0, len(names))
	for _, name := range names {
		rows = append(rows, []any{name})
	}
	run(fixture.Select(fdwImportBagSQL, []string{"relname"}, []string{"NAME"}, rows))
}

func fdwImportMetadata(run func(fixture.Step), notNull bool) {
	run(fixture.Select("SELECT c.relname,a.attname,a.atttypid::regtype::text AS type,a.atttypmod,a.attnotnull,a.attfdwoptions::text AS options FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname='fdw_import' AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum", []string{"relname", "attname", "type", "atttypmod", "attnotnull", "options"}, []string{"NAME", "NAME", "TEXT", "INT4", "BOOL", "TEXT"}, [][]any{
		{"import_a", "id", "integer", int64(-1), notNull, "{column_name=id}"},
		{"import_a", "label", "character varying", int64(11), false, "{column_name=label}"},
		{"import_a", "amount", "numeric", int64(393222), false, "{column_name=amount}"},
		{"import_a", "flag", "boolean", int64(-1), false, "{column_name=flag}"},
		{"import_b", "key", "bigint", int64(-1), notNull, "{column_name=key}"},
		{"import_b", "note", "text", int64(-1), false, "{column_name=note}"},
	}))
	run(fixture.Select("SELECT c.relname,f.ftoptions::text AS options FROM pg_foreign_table f JOIN pg_class c ON c.oid=f.ftrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' ORDER BY c.relname", []string{"relname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"import_a", "{schema_name=fdw_source,table_name=import_a}"}, {"import_b", "{schema_name=fdw_source,table_name=import_b}"}}))
}

func fdwImportValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		a := fixture.Select("SELECT id,label,amount,flag FROM fdw_import.import_a ORDER BY id", []string{"id", "label", "amount", "flag"}, []string{"INT4", "VARCHAR", "NUMERIC", "BOOL"}, [][]any{{int64(1), "alpha", "12.34", true}, {int64(2), nil, nil, false}})
		a.Prepared = prepared
		run(a)
		bb := fixture.Select("SELECT key,note FROM fdw_import.import_b ORDER BY key", []string{"key", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(9), "kept"}})
		bb.Prepared = prepared
		run(bb)
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT id,label,amount,flag FROM fdw_import.import_a WHERE false")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 4 {
		t.Fatalf("ColumnTypes=%v", types)
	}
	for i, want := range []string{"INT4", "VARCHAR", "NUMERIC", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			t.Fatalf("column %d type=%q want=%q", i, types[i].DatabaseTypeName(), want)
		}
	}
	if n, ok := types[1].Length(); ok && n != 7 {
		t.Fatalf("varchar length=%d", n)
	}
	if precision, scale, ok := types[2].DecimalSize(); ok && (precision != 6 || scale != 2) {
		t.Fatalf("numeric size=(%d,%d)", precision, scale)
	}
	if nullable, ok := types[0].Nullable(); ok && nullable {
		t.Fatal("NOT NULL id exposed as nullable")
	}
}

func TestReferenceFDWImportDefinitions(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,missing) FROM SERVER fdw_live INTO fdw_import", 0))
		fdwImportBag(run, "import_a")
		run(fixture.Exec("DROP FOREIGN TABLE fdw_import.import_a", 0))
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source EXCEPT(import_b,data) FROM SERVER fdw_live INTO fdw_import", 0))
		fdwImportBag(run, "import_a")
		run(fixture.Exec("DROP FOREIGN TABLE fdw_import.import_a", 0))
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", 0))
		fdwImportBag(run, "import_a", "import_b")
		fdwImportMetadata(run, true)
		fdwImportValues(t, p, b, c, run)
		run(fixture.Exec("CREATE TABLE fdw_comment_local(id integer)", 0))
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"COMMENT ON FOREIGN TABLE fdw_import.import_a IS 'actual imported table'", "COMMENT ON COLUMN fdw_import.import_a.label IS 'actual imported label'"} {
				step := fixture.Exec(q, 0)
				step.Prepared = prepared
				run(step)
			}
			step := fixture.ErrorDiagnostic("COMMENT ON FOREIGN TABLE fdw_comment_local IS 'wrong kind'", fdwDiagnostic("42809", `"fdw_comment_local" is not a foreign table`))
			step.Prepared = prepared
			run(step)
			step = fixture.ErrorDiagnostic("COMMENT ON TABLE fdw_import.import_a IS 'wrong kind'", fdwDiagnostic("42809", `"import_a" is not a table`))
			step.Prepared = prepared
			run(step)
		}
		run(fixture.Select("SELECT obj_description('fdw_import.import_a'::regclass::oid,'pg_class') AS table_comment,col_description('fdw_import.import_a'::regclass::oid,2) AS column_comment", []string{"table_comment", "column_comment"}, []string{"TEXT", "TEXT"}, [][]any{{"actual imported table", "actual imported label"}}))
		run(fixture.Exec("DROP FOREIGN TABLE fdw_import.import_a,fdw_import.import_b", 0))
		fdwSourceExec(t, p, b, r, run, `CREATE TABLE fdw_source."Import_A"(id integer)`, 0)
		run(fixture.Exec(`IMPORT FOREIGN SCHEMA fdw_source LIMIT TO("Import_A",missing) FROM SERVER fdw_live INTO fdw_import`, 0))
		fdwImportBag(run, "Import_A")
		run(fixture.Exec(`DROP FOREIGN TABLE fdw_import."Import_A"`, 0))
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(missing) FROM SERVER fdw_live INTO fdw_import", 0))
		fdwImportBag(run)
		utility := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(missing) FROM SERVER fdw_live INTO fdw_import"
		for _, prepared := range []bool{false, true} {
			step := fixture.Select(utility, []string{}, []string{}, [][]any{})
			step.Prepared = prepared
			run(step)
		}
	})
}

func TestReferenceFDWImportPrepared(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		q := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import"
		before := r.snapshot()
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("Prepare", err)
		}
		defer stmt.Close()
		fdwImportBag(run)
		if !b.PG && r.snapshot() != before {
			t.Fatal("Prepare called importer")
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
		fdwImportMetadata(run, true)
		collision := fdwDiagnostic("42P07", `relation "import_a" already exists`)
		collision.Context = `importing foreign table "import_a"`
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, collision))
		fdwImportBag(run, "import_a", "import_b")
		fdwImportValues(t, p, b, c, run)
		run(fixture.Exec("DROP FOREIGN TABLE fdw_import.import_a,fdw_import.import_b", 0))
		run(fixture.Exec("CREATE TABLE fdw_import.import_b(key bigint)", 0))
		run(fixture.Exec("INSERT INTO fdw_import.import_b VALUES(99)", 1))
		collision.Message = `relation "import_b" already exists`
		collision.Context = `importing foreign table "import_b"`
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, collision))
		fdwImportBag(run, "import_b")
		run(fixture.Select("SELECT key FROM fdw_import.import_b", []string{"key"}, []string{"INT8"}, [][]any{{int64(99)}}))
	})
}

func TestReferenceFDWImportOptions(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwImportSource(t, p, b, c, r, run)
				run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import OPTIONS(import_not_null '"+value+"')", 0))
				fdwImportMetadata(run, value == "true")
			})
		})
	}
	for _, options := range []string{"unknown 'x'", "import_not_null 'nonsense'", "import_not_null 'true',import_not_null 'false'"} {
		t.Run(options, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwImportSource(t, p, b, c, r, run)
				query := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import OPTIONS(" + options + ")"
				if options == "import_not_null 'true',import_not_null 'false'" {
					run(fixture.Exec(query, 0))
					fdwImportMetadata(run, false)
					return
				}
				if b.PG {
					want := fdwDiagnostic("HV00D", `invalid option "unknown"`)
					if options == "import_not_null 'nonsense'" {
						want = fdwDiagnostic("42601", "import_not_null requires a Boolean value")
					}
					run(fixture.ErrorDiagnostic(query, want))
				} else {
					_, err := c.ExecContext(p.Ctx, query)
					want := `SQL reader: unsupported schema import option "unknown"`
					if options == "import_not_null 'nonsense'" {
						want = `SQL reader: invalid import_not_null value "nonsense"`
					}
					fixture.TransactionOrdinary(t, err, want)
				}
				fdwImportBag(run)
			})
		})
	}
}
