package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGFileSettingsLifecycle(t *testing.T) {
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		return d.RegisterSetting(stuntgres.SettingDefinition{Name: "filesettings.mode", Type: stuntgres.SettingString, Default: "registered"})
	})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			exec := func(q string) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0)) }
			value := func(e fixture.Executor, name, want string) {
				fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT current_setting('"+name+"') AS value", []string{"value"}, []string{"TEXT"}, [][]any{{want}}))
			}
			var baseline string
			if err := peer.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&baseline); err != nil {
				t.Fatal(err)
			}
			pgFileSettingsSource(t, p, b, conn, false)
			exec("SET application_name='file_settings_not_a_file'")
			value(conn, "application_name", "file_settings_not_a_file")
			value(peer, "application_name", baseline)
			pgFileSettingsSource(t, p, b, conn, true)
			exec("RESET application_name")
			value(conn, "application_name", baseline)
			if b.PG {
				pgFileSettingsSource(t, p, b, conn, false)
				return
			}
			value(conn, "filesettings.mode", "registered")
			exec("SET filesettings.mode='session'")
			value(conn, "filesettings.mode", "session")
			pgFileSettingsSource(t, p, b, conn, false)
			exec("RESET filesettings.mode")
			value(conn, "filesettings.mode", "registered")
			var simulated string
			if err := conn.QueryRowContext(p.Ctx, "SHOW config_file").Scan(&simulated); err != nil || simulated == "" {
				t.Fatal(simulated, err)
			}
			pgFileSettingsSource(t, p, b, peer, false)
			exec("CREATE TABLE pgfilesettings_sentinel(n integer)")
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO pgfilesettings_sentinel VALUES(42)", 1))
			query := pgFileSettingsSelect + " WHERE FALSE"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
			authorityStatement, err := conn.PrepareContext(p.Ctx, pgFileSettingsPrivilegeQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer authorityStatement.Close()
			authorityHeld := fixture.HeldStatement{Executor: conn, Statement: authorityStatement, SQL: pgFileSettingsPrivilegeQuery}
			var retained []*sql.Rows
			var saved [][]*sql.ColumnType
			defer func() {
				for _, rows := range retained {
					rows.Close()
				}
			}()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = b.DB.QueryContext(p.Ctx, query)
				}
				if err != nil {
					t.Fatal(err)
				}
				retained = append(retained, rows)
				pgFileSettingsMetadata(t, rows, false)
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				saved = append(saved, types)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "pg_file_settings") || strings.Contains(dump.String(), "pg_show_all_file_settings") {
				t.Fatal("reconstructed file settings defaults added dump noise", dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			pgFileSettingsRead(t, p, b, held)
			pgFileSettingsInitialACL(t, p, b, conn, true)
			pgFileSettingsRoutineMetadata(t, p, b, conn)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT relname::text FROM pg_class WHERE relname='pgfilesettings_sentinel'", []string{"relname"}, []string{"TEXT"}, nil))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			pgFileSettingsSentinel(t, p, b, conn)
			pgFileSettingsRead(t, p, b, held)
			pgFileSettingsSource(t, p, b, conn, false)
			for _, rows := range retained {
				pgFileSettingsMetadata(t, rows, false)
				if rows.Next() || rows.Err() != nil {
					t.Fatal("retained empty snapshot", rows.Err())
				}
			}
			for _, types := range saved {
				fixture.ScalarAssertMetadata(t, false, types)
				for i, typ := range types {
					if typ.Name() != pgFileSettingsColumns[i] || typ.DatabaseTypeName() != pgFileSettingsTypes[i] {
						t.Fatal("saved descriptors changed", i, typ)
					}
					if nullable, known := typ.Nullable(); !nullable || !known {
						t.Fatal("saved nullability changed", i, nullable, known)
					}
				}
			}
			for _, q := range []string{"GRANT SELECT ON pg_catalog.pg_file_settings TO pg_signal_backend", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() TO pg_signal_backend"} {
				exec(q)
			}
			dump.Reset()
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, identity := range []string{"pg_file_settings", "pg_show_all_file_settings"} {
				if !strings.Contains(dump.String(), identity) {
					t.Fatal("custom catalog ACL was omitted from symbolic dump", identity, dump.String())
				}
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			pgFileSettingsInitialACL(t, p, b, conn, true)
			exec("SET ROLE pg_signal_backend")
			pgFileSettingsDenied(t, p, b, held, query, false)
			exec("RESET ROLE")
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_file_settings'::regclass", []string{"relacl"}, []string{"TEXT"}, [][]any{{"{postgres=arwdDxtm/postgres,pg_signal_backend=r/postgres}"}}))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=3329", []string{"proacl"}, []string{"TEXT"}, [][]any{{"{postgres=X/postgres,pg_signal_backend=X/postgres}"}}))
			exec("SET ROLE pg_signal_backend")
			pgFileSettingsRead(t, p, b, held)
			// This held statement predates both replacements. Unlike the retained
			// shape query, its backing function cannot be pruned by WHERE FALSE.
			readAuthority := func() {
				pgFileSettingsPrivilegeRead(t, p, b, conn)
				s := fixture.Select(pgFileSettingsPrivilegeQuery, pgFileSettingsColumns, pgFileSettingsTypes, nil)
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(s))
				pgFileSettingsPrivilegeRead(t, p, b, authorityHeld)
			}
			denyAuthority := func(function bool) {
				for _, executor := range []fixture.Executor{conn, authorityHeld} {
					pgFileSettingsDenied(t, p, b, executor, pgFileSettingsPrivilegeQuery, function)
				}
			}
			adminExec := func(q string) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(q, 0)) }
			readAuthority()
			adminExec("REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() FROM pg_signal_backend")
			denyAuthority(true)
			adminExec("GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() TO pg_signal_backend")
			readAuthority()
			adminExec("REVOKE SELECT ON pg_catalog.pg_file_settings FROM pg_signal_backend")
			denyAuthority(false)
			adminExec("GRANT SELECT ON pg_catalog.pg_file_settings TO pg_signal_backend")
			readAuthority()
			exec("RESET ROLE")
			pgFileSettingsInitialACL(t, p, b, conn, false)
			pgFileSettingsColumnACLControls(t, p, b, conn, peer)
			for range 2 {
				pool, recorder := fixture.RecordedNativePool(t, p.NativeConnector)
				fresh := fixture.Backend{Name: "reopened", DB: pool, Recorder: recorder}
				pgFileSettingsSource(t, p, fresh, pool, false)
				if err := pool.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// Native controls must replay actual column authority, not merely classify a
// changed ACL as nondefault. Whole-view SELECT is deliberately absent throughout.
func pgFileSettingsColumnACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend, actor, admin *sql.Conn) {
	t.Helper()
	exec := func(e fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0)) }
	exec(admin, "REVOKE SELECT ON pg_catalog.pg_file_settings FROM pg_signal_backend")
	exec(admin, "GRANT SELECT(sourcefile) ON pg_catalog.pg_file_settings TO pg_signal_backend")
	exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() TO pg_signal_backend")
	query := "SELECT sourcefile FROM pg_catalog.pg_file_settings"
	statement, err := actor.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	held := fixture.HeldStatement{Executor: actor, Statement: statement, SQL: query}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "UPDATE pg_catalog.pg_attribute SET attacl=") || !strings.Contains(dump.String(), "'sourcefile'::pg_catalog.name") {
		t.Fatal("column ACL did not enter symbolic attribute replay", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	pgFileSettingsInitialACL(t, p, b, admin, true)
	fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT attacl::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_file_settings'::regclass AND attname='sourcefile'", []string{"attacl"}, []string{"TEXT"}, [][]any{{nil}}))
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_file_settings'::regclass", []string{"relacl"}, []string{"TEXT"}, [][]any{{"{postgres=arwdDxtm/postgres}"}}))
	fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT attname::text,attacl::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_file_settings'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attacl"}, []string{"TEXT", "TEXT"}, [][]any{
		{"sourcefile", "{pg_signal_backend=r/postgres}"},
		{"sourceline", nil},
		{"seqno", nil},
		{"name", nil},
		{"setting", nil},
		{"applied", nil},
		{"error", nil},
	}))
	fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=3329", []string{"proacl"}, []string{"TEXT"}, [][]any{{"{postgres=X/postgres,pg_signal_backend=X/postgres}"}}))
	history := func() {
		pgFileSettingsInitialACL(t, p, b, admin, false)
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT count(*) FROM pg_init_privs WHERE classoid='pg_catalog.pg_class'::regclass AND objoid='pg_catalog.pg_file_settings'::regclass AND objsubid>0", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
	history()
	exec(actor, "SET ROLE pg_signal_backend")
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(query, []string{"sourcefile"}, []string{"TEXT"}, nil)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"sourcefile"}, []string{"TEXT"}, nil))
	for _, deniedQuery := range []string{
		"SELECT * FROM pg_catalog.pg_file_settings",
		"SELECT name FROM pg_catalog.pg_file_settings",
		"SELECT sourcefile FROM pg_catalog.pg_file_settings WHERE name='stuntgres_missing_file_parameter'",
		"SELECT sourcefile FROM pg_catalog.pg_file_settings ORDER BY seqno",
	} {
		pgFileSettingsDenied(t, p, b, actor, deniedQuery, false)
	}
	exec(admin, "REVOKE SELECT(sourcefile) ON pg_catalog.pg_file_settings FROM pg_signal_backend")
	pgFileSettingsDenied(t, p, b, actor, query, false)
	pgFileSettingsDenied(t, p, b, held, query, false)
	history()
	exec(actor, "RESET ROLE")
	exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() FROM pg_signal_backend")
	pgFileSettingsInitialACL(t, p, b, admin, true)
	pgFileSettingsSentinel(t, p, b, admin)
}
