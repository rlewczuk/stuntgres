package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const fdwSyntheticSecret = "synthetic-only O'Brien\\tail\nnext"

func fdwLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Initial literals are independently asserted below; snapshots additionally
// preserve all identity relationships/options/expressions on failed replacement.
var fdwGraphQueries = []string{
	"SELECT w.fdwname,r.rolname,w.fdwhandler::regproc::text,w.fdwvalidator::regproc::text,w.fdwoptions::text FROM pg_foreign_data_wrapper w JOIN pg_roles r ON r.oid=w.fdwowner ORDER BY w.fdwname",
	"SELECT s.srvname,w.fdwname,r.rolname,s.srvtype,s.srvversion,s.srvoptions::text FROM pg_foreign_server s JOIN pg_foreign_data_wrapper w ON w.oid=s.srvfdw JOIN pg_roles r ON r.oid=s.srvowner ORDER BY s.srvname",
	"SELECT s.srvname,m.umuser::regrole::text,m.umoptions::text FROM pg_user_mapping m JOIN pg_foreign_server s ON s.oid=m.umserver ORDER BY s.srvname,m.umuser",
	"SELECT n.nspname,c.relname,r.rolname,c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='fdw_import' JOIN pg_roles r ON r.oid=c.relowner ORDER BY c.relname",
	"SELECT c.relname,a.attname,a.atttypid::regtype::text,a.atttypmod,a.attnotnull,a.attfdwoptions::text,col_description(c.oid,a.attnum) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='fdw_import' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum",
	"SELECT c.relname,s.srvname,f.ftoptions::text,obj_description(c.oid,'pg_class') FROM pg_foreign_table f JOIN pg_class c ON c.oid=f.ftrelid JOIN pg_foreign_server s ON s.oid=f.ftserver JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' ORDER BY c.relname",
	"SELECT c.relname,a.attname,pg_get_expr(d.adbin,d.adrelid) FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum JOIN pg_class c ON c.oid=d.adrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' ORDER BY c.relname,a.attnum",
	"SELECT c.relname,k.conname,pg_get_constraintdef(k.oid) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import' ORDER BY c.relname,k.conname",
}

func fdwGraphSnapshot(t *testing.T, ctx context.Context, db fixture.Executor) [][][]any {
	t.Helper()
	var out [][][]any
	for _, q := range fdwGraphQueries {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		bag := make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			bag = append(bag, values)
		}
		err = rows.Err()
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, bag)
	}
	return out
}

func fdwAssertGraph(t *testing.T, ctx context.Context, db fixture.Executor, want [][][]any) {
	t.Helper()
	got := fdwGraphSnapshot(t, ctx, db)
	if !reflect.DeepEqual(got, want) {
		// Mapping credential bytes must never appear in test logs.
		for i := range got {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("persistent graph query %d changed (values withheld)", i)
			}
		}
	}
}

func fdwImportPersistenceGraph(t *testing.T) {
	fdwConfiguredGraphScenario(t, true, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", 0))
		fdwImportMetadata(run, true)
		fdwImportValues(t, p, b, c, run)
		for _, q := range []string{
			"CREATE ROLE fdw_owner SUPERUSER",
			"CREATE ROLE fdw_other",
			"CREATE ROLE fdw_reader",
			"ALTER FOREIGN TABLE fdw_import.import_a ALTER COLUMN id SET DEFAULT 7",
			"ALTER FOREIGN TABLE fdw_import.import_a ADD CONSTRAINT import_positive CHECK(id>0)",
			"COMMENT ON FOREIGN TABLE fdw_import.import_a IS 'imported table'",
			"COMMENT ON COLUMN fdw_import.import_a.label IS 'imported label'",
			"CREATE DOMAIN fdw_import.key_type AS integer",
			"CREATE FOREIGN TABLE fdw_import.typed(id fdw_import.key_type OPTIONS(column_name 'id')) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'import_a')",
			"CREATE VIEW fdw_import.typed_view AS SELECT id FROM fdw_import.typed",
			"ALTER SCHEMA fdw_import OWNER TO fdw_owner",
			"ALTER SERVER fdw_live OWNER TO fdw_other",
			"ALTER FOREIGN TABLE fdw_import.import_a OWNER TO fdw_reader",
			"ALTER FOREIGN TABLE fdw_import.import_b OWNER TO fdw_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		options := "user 'postgres'"
		if b.PG {
			options = "user 'stuntgres',password 'stuntgres',password_required 'false'"
		}
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS("+options+")", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_live OPTIONS("+options+")", 0))
		bootstrap := "postgres"
		if b.PG {
			bootstrap = "stuntgres"
		}
		run(fixture.Select("SELECT c.relname,r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='fdw_import' AND c.relkind='f' ORDER BY c.relname", []string{"relname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"import_a", "fdw_reader"}, {"import_b", "fdw_reader"}, {"typed", bootstrap}}))
		schemaDenied := fdwDiagnostic("42501", "permission denied for schema fdw_import")
		schemaDenied.Position = "16"
		denied := fixture.ErrorDiagnostic("SELECT id FROM fdw_import.import_a", schemaDenied)
		denied.SelectRows = true
		serverDenied := fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a) FROM SERVER fdw_live INTO public", fdwDiagnostic("42501", "permission denied for foreign server fdw_live"))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(denied)
		run(serverDenied)
		run(fixture.Exec("RESET ROLE", 0))
		if b.PG {
			return
		} // Native control API has no PostgreSQL equivalent.
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native VALIDATOR fdw_graph_validator OPTIONS(tag 'wrapper-first')", 0))
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native OWNER TO fdw_owner", 0))
		run(fixture.Exec("ALTER SERVER fdw_live VERSION '' OPTIONS(tag 'server-first')", 0))
		run(fixture.Exec("CREATE SERVER fdw_second TYPE '' VERSION 'v2' FOREIGN DATA WRAPPER fdw_native OPTIONS(tag 'second')", 0))
		run(fixture.Exec("ALTER USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS(password "+fdwLiteral(fdwSyntheticSecret)+",empty '')", 0))
		run(fixture.Exec("ALTER USER MAPPING FOR PUBLIC SERVER fdw_live OPTIONS(password 'synthetic-public',empty '')", 0))
		// Dropping/readding note puts its options at attnum 3, not slice slot 2.
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.import_b DROP COLUMN note", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.import_b ADD COLUMN note text OPTIONS(column_name 'note')", 0))
		run(fixture.Select("SELECT a.attnum,a.attname,a.attfdwoptions::text AS options FROM pg_attribute a WHERE a.attrelid='fdw_import.import_b'::regclass::oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum", []string{"attnum", "attname", "options"}, []string{"INT2", "NAME", "TEXT"}, [][]any{{int64(1), "key", "{column_name=key}"}, {int64(3), "note", "{column_name=note}"}}))
		run(fixture.Select("SELECT s.srvname,s.srvtype,s.srvversion,s.srvoptions::text AS options,r.rolname FROM pg_foreign_server s JOIN pg_roles r ON r.oid=s.srvowner ORDER BY s.srvname", []string{"srvname", "srvtype", "srvversion", "options", "rolname"}, []string{"NAME", "TEXT", "TEXT", "TEXT", "NAME"}, [][]any{{"fdw_live", nil, "", "{tag=server-first}", "fdw_other"}, {"fdw_second", "", "v2", "{tag=second}", "postgres"}}))
		var rawSecret string
		if err := c.QueryRowContext(p.Ctx, "SELECT umoptions[2] FROM pg_user_mapping WHERE umuser='fdw_reader'::regrole::oid AND umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_live')").Scan(&rawSecret); err != nil {
			t.Fatal(err)
		}
		if rawSecret != "password="+fdwSyntheticSecret {
			t.Fatal("synthetic mapping bytes changed (withheld)")
		}
		beforeGraph := fdwGraphSnapshot(t, p.Ctx, c)
		old := fdwHeld(t, p, b, c, "SELECT id,label FROM fdw_import.import_a ORDER BY id")
		forbidden := errors.New("control invoked import or scan callback")
		guard := func(ctx context.Context, method string) error {
			if method != "Validate" {
				return forbidden
			}
			return ctx.Err()
		}
		r.setHook(guard)
		before := r.snapshot()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if r.snapshot() != before {
			t.Fatal("Dump called a provider")
		}
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.import_a SET label='changed' WHERE id=1", 1)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fdwAssertGraph(t, p.Ctx, c, beforeGraph)
		fdwAssertNoControlScans(t, r, before)
		r.setHook(nil)
		defer r.setHook(nil)
		run(fixture.Select("SELECT id,label,amount,flag FROM fdw_import.import_a ORDER BY id", []string{"id", "label", "amount", "flag"}, []string{"INT4", "VARCHAR", "NUMERIC", "BOOL"}, [][]any{{int64(1), "changed", "12.34", true}, {int64(2), nil, nil, false}}))
		fixture.RunStep(t, p.Ctx, old, old.recorder, fixture.Select(old.query, []string{"id", "label"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(1), "alpha"}, {int64(2), nil}}))
		run(fixture.Select("SELECT key,note FROM fdw_import.import_b", []string{"key", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(9), "kept"}}))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(denied)
		run(serverDenied)
		run(fixture.Exec("RESET ROLE", 0))
		fdwPersistenceFailures(t, p, b, c, r, run, dump.Bytes(), beforeGraph)
		// A successful empty-TEMP replay exposes the candidate's actual addresses.
		// The second destination retains colliding TEMP providers at those OIDs.
		var sourceWrapper, sourceServer uint64
		if err := c.QueryRowContext(p.Ctx, "SELECT w.oid,s.oid FROM pg_foreign_data_wrapper w JOIN pg_foreign_server s ON s.srvfdw=w.oid WHERE s.srvname='fdw_live'").Scan(&sourceWrapper, &sourceServer); err != nil {
			t.Fatal(err)
		}
		fdwPersistenceTargets(t, p, r, dump.Bytes(), sourceWrapper, sourceServer)
	})
}

func fdwAssertNoControlScans(t *testing.T, r *fdwSQLReader, before fdwReaderState) {
	t.Helper()
	after := r.snapshot()
	if after.begins != before.begins || after.queries != before.queries || after.iterations != before.iterations || after.openPools != before.openPools {
		t.Fatal("control scanned/imported remote data", before, after)
	}
}

func fdwPersistenceFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step), script []byte, graph [][][]any) {
	t.Helper()
	run(fixture.Exec("CREATE TABLE fdw_sentinel(v integer)", 0))
	run(fixture.Exec("INSERT INTO fdw_sentinel VALUES(99)", 1))
	sentinel := fdwHeld(t, p, b, c, "SELECT v FROM fdw_sentinel")
	preserve := func() {
		t.Helper()
		fdwAssertGraph(t, p.Ctx, c, graph)
		run(fixture.Select("SELECT v FROM fdw_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
	}
	failure := errors.New("later actual validator rejected restore")
	for _, kind := range []string{"reader", "cancel", "late-sql", "validator", "import", "alter-options", "drop", "foreign-write"} {
		t.Run(kind, func(t *testing.T) {
			input := io.Reader(bytes.NewReader(script))
			ctx := p.Ctx
			r.setHook(func(ctx context.Context, method string) error {
				if method != "Validate" {
					return errors.New("restore invoked remote callback")
				}
				return ctx.Err()
			})
			defer r.setHook(nil)
			switch kind {
			case "reader":
				input = &fixture.ReferenceRangeErrorReader{Err: failure}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(p.Ctx)
				cancel()
			case "late-sql":
				input = strings.NewReader(string(script) + "CREATE TABLE malformed(")
			case "validator":
				r.mu.Lock()
				r.validateHook = func(ctx context.Context, k stuntgres.ForeignObjectKind, options []stuntgres.ForeignOption) error {
					if k == stuntgres.ForeignTable && fdwOption(options, "table_name") == "import_b" {
						return failure
					}
					return ctx.Err()
				}
				r.mu.Unlock()
				defer func() { r.mu.Lock(); r.validateHook = nil; r.mu.Unlock() }()
			case "import":
				input = strings.NewReader(string(script) + "IMPORT FOREIGN SCHEMA fdw_source FROM SERVER fdw_live INTO fdw_import;")
			case "alter-options":
				input = strings.NewReader(string(script) + "ALTER SERVER fdw_live OPTIONS(SET tag 'forbidden');")
			case "drop":
				input = strings.NewReader(string(script) + "DROP SERVER fdw_live CASCADE;")
			case "foreign-write":
				input = strings.NewReader(string(script) + "INSERT INTO fdw_import.import_a(id) VALUES(8);")
			}
			err := stuntgres.Restore(ctx, p.Native, input)
			if err == nil {
				t.Fatal("invalid restore succeeded")
			}
			if (kind == "reader" || kind == "validator") && !errors.Is(err, failure) {
				t.Fatal("ordinary restore error identity lost", err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if kind == "foreign-write" {
				want := fdwDiagnostic("0A000", "restore cannot write foreign tables")
				fixture.AssertDiagnostic(t, err, &want)
			}
			preserve()
		})
	}
	fixture.RunStep(t, p.Ctx, sentinel, sentinel.recorder, fixture.Select(sentinel.query, []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script)); err != nil {
		t.Fatal("recovery", err)
	}
	fdwAssertGraph(t, p.Ctx, c, graph)
}

func fdwPersistenceTargets(t *testing.T, p *fixture.Pair, r *fdwSQLReader, script []byte, sourceWrapper, sourceServer uint64) {
	t.Helper()
	for _, registration := range []string{"none", "handler-only", "configured"} {
		d := stuntgres.NewDriver()
		if registration == "configured" {
			if err := r.configure(d); err != nil {
				t.Fatal(err)
			}
			if err := d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_graph_validator"}, Validate: r.validateGraph}); err != nil {
				t.Fatal(err)
			}
		} else if registration == "handler-only" {
			if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_sql_handler"}, Wrapper: r}); err != nil {
				t.Fatal(err)
			}
		}
		connector, err := d.OpenConnector("fdw-persistence-destination")
		if err != nil {
			t.Fatal(err)
		}
		control := sql.OpenDB(connector)
		defer control.Close()
		db := fdwRecordedPool(t, connector)
		defer db.Close()
		recorder := &fixture.Recorder{RawBackend: fixture.RawNative}
		exec := func(q string) { fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec(q, 0)) }
		// A different allocation history plus a retained TEMP relation forces the
		// replacement to keep real session-local identities, not just reset empty.
		exec("CREATE TEMP TABLE retained_temp(v integer)")
		if registration == "configured" {
			retained := make(map[uint64]bool)
			rows, err := db.QueryContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname LIKE 'pg_%temp_%'")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var oid uint64
				if err := rows.Scan(&oid); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				retained[oid] = true
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			var tableOID uint64
			if err := db.QueryRowContext(p.Ctx, "SELECT 'retained_temp'::regclass::oid").Scan(&tableOID); err != nil {
				t.Fatal(err)
			}
			retained[tableOID] = true
			if !retained[sourceWrapper] || !retained[sourceServer] {
				t.Fatal("fixture did not produce actual retained-TEMP wrapper/server candidate address collisions")
			}
		}
		exec("CREATE TABLE allocation_history(v integer)")
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("INSERT INTO retained_temp VALUES(41)", 1))
		exec("CREATE TABLE fdw_sentinel(v integer)")
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("INSERT INTO fdw_sentinel VALUES(99)", 1))
		for _, q := range []string{"CREATE ROLE fdw_owner SUPERUSER", "CREATE ROLE fdw_other", "CREATE ROLE fdw_reader"} {
			exec(q)
		}
		if registration != "configured" {
			err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(script))
			want := fdwDiagnostic("42883", "function public.fdw_sql_handler() does not exist")
			if registration == "handler-only" {
				want = fdwDiagnostic("42883", "function public.fdw_graph_validator(text[], oid) does not exist")
			}
			fixture.AssertDiagnostic(t, err, &want)
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT v FROM fdw_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
			continue
		}
		exec("DROP ROLE fdw_reader")
		if err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(script)); err == nil {
			t.Fatal("restore accepted absent mapping/table role")
		}
		exec("CREATE ROLE fdw_reader")
		// Target role identities are captured before external reader I/O.
		gated := &fixture.ReferenceRangeGateReader{Reader: bytes.NewReader(script), Entered: make(chan struct{}), Release: make(chan struct{})}
		done := make(chan error, 1)
		go func() { done <- stuntgres.Restore(p.Ctx, control, gated) }()
		joined, released := false, false
		func() {
			defer func() {
				if !released {
					close(gated.Release)
				}
				if !joined {
					<-done
				}
			}()
			select {
			case <-gated.Entered:
			case err := <-done:
				joined = true
				t.Fatalf("restore reader gate not entered: %v", err)
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			exec("DROP ROLE fdw_reader")
			exec("CREATE ROLE fdw_reader")
			close(gated.Release)
			released = true
			if err := <-done; err == nil {
				joined = true
				t.Fatal("recreated role adopted captured authority")
			}
			joined = true
		}()
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT v FROM fdw_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		if err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT v FROM retained_temp", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT id FROM fdw_import.typed_view ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		// Selecting exact and PUBLIC mappings after remapping checks server/key
		// references rather than comparing generated OIDs between databases.
		exec("GRANT USAGE ON SCHEMA fdw_import TO fdw_reader")
		exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_reader")
		exec("SET ROLE fdw_reader")
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT key,note FROM fdw_import.import_b", []string{"key", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(9), "kept"}}))
		exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_live")
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT key,note FROM fdw_import.import_b", []string{"key", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(9), "kept"}}))
		exec("RESET ROLE")
	}
}
