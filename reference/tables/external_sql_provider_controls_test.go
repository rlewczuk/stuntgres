package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExternalSQLProviderControls(t *testing.T) {
	p, provider, _ := externalSQLPhysicalScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("UPDATE external_sql_source.data SET remote_label='x' WHERE remote_id=1", 1))
			c := externalSQLProviderConnection(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			externalSQLProviderInstallPhysical(t, b, run)
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE EXTENSION dblink", 0),
				fixture.Exec("CREATE TABLE external_sql_key(id integer PRIMARY KEY,label text)", 0),
				fixture.Exec("INSERT INTO external_sql_key VALUES(7,'kept')", 1),
				fixture.Exec("CREATE TABLE external_sql_local_marker(v integer)", 0),
				fixture.Exec("INSERT INTO external_sql_local_marker VALUES(73)", 1),
				fixture.Exec("CREATE VIEW external_sql_saved AS SELECT id,label,flag,bytes FROM external_sql_data", 0),
				fixture.Exec("CREATE VIEW external_sql_saved_key AS SELECT * FROM dblink_get_pkey('external_sql_key')", 0),
				fixture.Exec("CREATE VIEW external_sql_saved_constructor AS SELECT dblink_build_sql_delete('external_sql_key','1'::int2vector,1,'{7}'::text[]) AS sql", 0),
			} {
				run(s)
			}
			check := func(db fixture.Executor, recorder *fixture.Recorder, label string) {
				for _, s := range []fixture.Step{
					fixture.Select("SELECT v FROM external_sql_local_marker", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}),
					fixture.Select("SELECT position,colname FROM external_sql_saved_key ORDER BY position", []string{"position", "colname"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "id"}}),
					fixture.Select("SELECT sql FROM external_sql_saved_constructor", []string{"sql"}, []string{"TEXT"}, [][]any{{"DELETE FROM external_sql_key WHERE id = '7'"}}),
					fixture.Select("SELECT e.extversion,p.prorettype=t.oid AS nominal_result,c.reltype=t.oid AS backing,a.typelem=t.oid AS array_element FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid AND p.proname='dblink_get_pkey' JOIN pg_type t ON t.typnamespace=n.oid AND t.typname='dblink_pkey_results' JOIN pg_class c ON c.oid=t.typrelid JOIN pg_type a ON a.oid=t.typarray JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.refclassid=3079 AND d.refobjid=e.oid AND d.deptype='e' WHERE e.extname='dblink'", []string{"extversion", "nominal_result", "backing", "array_element"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.2", true, true, true}}),
					fixture.Select("SELECT id,label,flag,bytes FROM external_sql_saved WHERE id=1", []string{"id", "label", "flag", "bytes"}, []string{"INT4", "TEXT", "BOOL", "BYTEA"}, [][]any{{int64(1), label, true, "\x00\xff"}}),
				} {
					fixture.RunStep(t, p.Ctx, db, recorder, s)
					fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
				}
			}
			check(c, b.Recorder, "x")
			if b.PG {
				return
			}
			old, err := c.QueryContext(p.Ctx, "SELECT id,label,flag,bytes FROM external_sql_data ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			before := len(provider.Snapshot())
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if len(provider.Snapshot()) != before {
				t.Fatal("Dump executed remote SQL")
			}
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DECLARE external_sql_obsolete NO SCROLL CURSOR FOR SELECT id,label,flag,bytes FROM external_sql_data ORDER BY id", 0))
			run(externalSQLProviderRows(false, "FETCH 1 FROM external_sql_obsolete", [][]any{{int64(1), "x", true, "\x00\xff"}}))
			wires := provider.Snapshot()
			obsoletePID := wires[len(wires)-1].Info().BackendPID
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			stale, err := c.QueryContext(p.Ctx, "FETCH ALL FROM external_sql_obsolete")
			if stale != nil {
				stale.Close()
				t.Fatal("stale cursor published rows")
			}
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			run(fixture.Exec("ROLLBACK", 0))
			externalSQLBackendGone(t, p.Ctx, obsoletePID)
			before = len(provider.Snapshot())
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			if len(provider.Snapshot()) != before {
				t.Fatal("Restore opened remote resources")
			}
			externalSQLOldRows(t, old)
			check(c, b.Recorder, "x")
			if _, err := p.Postgres.ExecContext(p.Ctx, "UPDATE external_sql_source.data SET remote_label='after-dump' WHERE remote_id=1"); err != nil {
				t.Fatal(err)
			}

			for _, binding := range []string{"absent", "unbound", "wrong-version", "matching"} {
				t.Run(binding, func(t *testing.T) {
					destinationProvider := &fixture.ExternalSQLProvider{}
					d := stuntgres.NewDriver()
					if binding == "unbound" {
						if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "external_sql_fixture_handler"}, Wrapper: &externalSQLHandler{}}); err != nil {
							t.Fatal(err)
						}
					} else if binding != "absent" {
						version := fixture.ExternalSQLFixtureVersion
						if binding == "wrong-version" {
							version = "incompatible-version"
						}
						if err := externalSQLConfigure(d, destinationProvider, &externalSQLHandler{}, version); err != nil {
							t.Fatal(err)
						}
					}
					connector, err := d.OpenConnector("external-sql-destination/" + t.Name())
					if err != nil {
						t.Fatal(err)
					}
					native := sql.OpenDB(connector)
					db, recorder := fixture.RecordedNativePool(t, connector)
					t.Cleanup(func() { db.Close(); native.Close() })
					fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("CREATE TABLE external_sql_destination_keep(v integer)", 0))
					fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("INSERT INTO external_sql_destination_keep VALUES(82)", 1))
					err = stuntgres.Restore(p.Ctx, native, strings.NewReader(dump.String()))
					if binding != "matching" {
						if err == nil {
							t.Fatal("incompatible destination registration accepted")
						}
						fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT v FROM external_sql_destination_keep", []string{"v"}, []string{"INT4"}, [][]any{{int64(82)}}))
						if len(destinationProvider.Snapshot()) != 0 {
							t.Fatal("failed Restore opened remote connection")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(destinationProvider.Snapshot()) != 0 {
						t.Fatal("successful Restore serialized/opened physical resources")
					}
					check(db, recorder, "after-dump")
					if len(destinationProvider.Snapshot()) == 0 {
						t.Fatal("destination used source callback instead of its own actual provider")
					}
					for _, wire := range destinationProvider.Snapshot() {
						externalSQLBackendGone(t, p.Ctx, wire.Info().BackendPID)
					}
					header := ""
					for _, line := range strings.Split(dump.String(), "\n") {
						if strings.HasPrefix(line, "-- stuntgres-external-sql-v1 ") {
							header = line + "\n"
							break
						}
					}
					if header == "" {
						t.Fatal("bound provider dump omitted its prerequisite")
					}
					for _, malformed := range []string{header + dump.String(), strings.Replace(dump.String(), header, "", 1), strings.Replace(dump.String(), header, "-- stuntgres-external-sql-v1 malformed\n", 1), strings.Replace(dump.String(), "stuntgres-external-sql-v1 ", "stuntgres-external-sql-v99 ", 1)} {
						if err := stuntgres.Restore(p.Ctx, native, strings.NewReader(malformed)); err == nil {
							t.Fatal("invalid/missing/duplicate provider prerequisite accepted")
						}
						check(db, recorder, "after-dump")
					}
				})
			}
		})
	}
}

func externalSQLOldRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id", "label", "flag", "bytes"}) {
		t.Fatal("old columns changed", columns, err)
	}
	var values [][]any
	for rows.Next() {
		var id, label, flag, data any
		if err := rows.Scan(&id, &label, &flag, &data); err != nil {
			t.Fatal(err)
		}
		values = append(values, []any{fixture.Normalize(id), fixture.Normalize(label), fixture.Normalize(flag), fixture.Normalize(data)})
	}
	if rows.Err() != nil || !reflect.DeepEqual(values, externalSQLProviderExpectedRows()) {
		t.Fatal("old finalized rows changed", values, rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
