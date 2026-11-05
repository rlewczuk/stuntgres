package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func lifecyclePeer(t *testing.T, s *lifecycleCase, database string) (*lifecycleCase, func()) {
	t.Helper()
	pool, raw, rec := fixture.DatabaseScenarioPool(t, s.p, s.b.PG, database, "")
	c, err := pool.Conn(s.p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := s.b
	b.DB, b.Recorder = pool, rec
	peer := &lifecycleCase{p: s.p, b: b, c: c, notices: new([]fixture.Diagnostic)}
	return peer, func() { c.Close(); pool.Close(); raw.Close() }
}

func TestReferenceTablespaceLifecycleSharedTargets(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		// The shared PG cluster never commits a builtin rename. It does
		// independently prove cross-database isolation and rollback below.
		created := false
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if created {
				if _, err := s.c.ExecContext(ctx, "DROP DATABASE ts_lifecycle_shared_before"); err != nil {
					t.Error("shared database cleanup", err)
				}
			}
		}()
		s.exec(t, "CREATE DATABASE ts_lifecycle_shared_before TEMPLATE template1")
		created = true
		peer, closePeer := lifecyclePeer(t, s, "ts_lifecycle_shared_before")
		defer closePeer()
		peer.names(t, "pg_default", "pg_global")
		s.begin(t)
		defer func() {
			if s.inTx {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := s.c.ExecContext(ctx, "ROLLBACK"); err != nil {
					t.Error("shared rollback cleanup", err)
				}
				s.inTx = false
			}
		}()
		s.exec(t, "CREATE ROLE ts_lifecycle_shared_owner")
		s.exec(t, "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_shared_private")
		s.exec(t, "ALTER TABLESPACE ts_lifecycle_shared_private OWNER TO ts_lifecycle_shared_owner")
		s.exec(t, "GRANT CREATE ON TABLESPACE ts_lifecycle_shared_private TO PUBLIC")
		s.owner(t, "ts_lifecycle_shared_private", "ts_lifecycle_shared_owner", "{ts_lifecycle_shared_owner=C/ts_lifecycle_shared_owner,=C/ts_lifecycle_shared_owner}")
		peer.names(t, "pg_default", "pg_global")
		s.exec(t, "SAVEPOINT ts_lifecycle_shared_savepoint")
		s.exec(t, "ALTER TABLESPACE pg_global RENAME TO ts_lifecycle_global_private")
		s.exec(t, "ROLLBACK TO ts_lifecycle_shared_savepoint")
		s.exec(t, "RELEASE ts_lifecycle_shared_savepoint")
		peer.names(t, "pg_default", "pg_global")
		s.rollback(t)
		s.names(t, "pg_default", "pg_global")
		peer.names(t, "pg_default", "pg_global")
		// Native publication rejects a pre-canceled context. PostgreSQL may
		// finish the command before cancellation arrives, so never send this
		// autocommit builtin mutation to the shared oracle.
		if !s.b.PG {
			t.Run("native-canceled-publication", func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					q := "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_shared_canceled"
					ctx, cancel := context.WithCancel(s.p.Ctx)
					cancel()
					if prepared {
						held, err := s.c.PrepareContext(s.p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						_, err = held.ExecContext(ctx)
						held.Close()
						if !errors.Is(err, context.Canceled) {
							t.Fatal("shared canceled prepared rename", err)
						}
					} else {
						if _, err := s.c.ExecContext(ctx, q); !errors.Is(err, context.Canceled) {
							t.Fatal("shared canceled direct rename", err)
						}
					}
					s.names(t, "pg_default", "pg_global")
					peer.names(t, "pg_default", "pg_global")
				}
			})
		}
		closePeer()
		if !s.b.PG {
			t.Run("native-committed-existing-and-later-targets", func(t *testing.T) { lifecycleNativeCommitted(t, s, false) })
		}
	})
}

func TestReferenceTablespaceLifecycleControls(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		// The common SQL image is independently asserted in both modes;
		// native controls are an explicit additional capability, not PG parity.
		s.begin(t)
		s.utility(t, "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_control_rollback", true)
		s.names(t, "ts_lifecycle_control_rollback", "pg_global")
		s.rollback(t)
		s.names(t, "pg_default", "pg_global")
		if !s.b.PG {
			t.Run("native-dump-reset-restore", func(t *testing.T) { lifecycleNativeCommitted(t, s, true) })
		}
	})
}

// The same committed native witness is used by visibility, controls and smoke.
// No PostgreSQL builtin mutation escapes a transaction in any calling scenario.
func lifecycleNativeCommitted(t *testing.T, s *lifecycleCase, controls bool) {
	t.Helper()
	if s.b.PG {
		t.Fatal("native controls are not a PostgreSQL substitute")
	}
	const def = "ts lifecycle \"default"
	const global = "PG_lifecycle_global"
	s.exec(t, "CREATE ROLE ts_lifecycle_control_owner")
	s.exec(t, "CREATE ROLE ts_lifecycle_control_reader")
	// Role identity is allocated independently of the builtin tablespaces.
	// Reset must retain this exact destination principal, not bootstrap OID10.
	var ownerOID int64
	if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname='ts_lifecycle_control_owner'").Scan(&ownerOID); err != nil {
		t.Fatal(err)
	}
	s.exec(t, "CREATE DATABASE ts_lifecycle_control_before")
	peer, closePeer := lifecyclePeer(t, s, "ts_lifecycle_control_before")
	defer closePeer()
	retained, err := s.c.QueryContext(s.p.Ctx, "SELECT oid,spcname,spcowner,spcacl,spcoptions FROM pg_tablespace ORDER BY oid")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	tablespaceMetadata(t, metadata, false)
	s.exec(t, "SET temp_tablespaces='pg_global'")
	s.begin(t)
	s.error(t, "CREATE TEMP TABLE ts_lifecycle_control_cache_before(a integer)", tablespaceDiagnostic("22023", "only shared relations can be placed in pg_global tablespace"), false)
	s.step(t, fixture.Prepared(fixture.Exec("ALTER TABLESPACE pg_default RENAME TO "+lifecycleQuote(def), 0)))
	s.exec(t, "ALTER TABLESPACE pg_global RENAME TO "+lifecycleQuote(global))
	s.exec(t, "GRANT CREATE ON TABLESPACE "+lifecycleQuote(def)+" TO ts_lifecycle_control_reader")
	s.exec(t, "ALTER TABLESPACE "+lifecycleQuote(def)+" OWNER TO ts_lifecycle_control_owner")
	s.exec(t, "ALTER TABLESPACE "+lifecycleQuote(global)+" OWNER TO ts_lifecycle_control_owner")
	peer.names(t, "pg_default", "pg_global")
	s.exec(t, "COMMIT")
	s.inTx = false
	// These autocommit errors must resolve the committed live name, not seed
	// spellings. Path validation still precedes the duplicate-name check.
	s.error(t, tablespaceSQL(lifecycleQuote(def), "", tablespaceAbsent), tablespaceDiagnostic("42710", "tablespace \""+def+"\" already exists"), false)
	s.error(t, tablespaceSQL(lifecycleQuote(def), "", "relative"), tablespaceDiagnostic("42P17", "tablespace location must be an absolute path"), false)
	check := func(t *testing.T, x *lifecycleCase) {
		t.Helper()
		x.owner(t, def, "ts_lifecycle_control_owner", "{ts_lifecycle_control_owner=C/ts_lifecycle_control_owner,ts_lifecycle_control_reader=C/ts_lifecycle_control_owner}")
		x.owner(t, global, "ts_lifecycle_control_owner", "")
		x.both(t, tablespaceStep(x.b, "SELECT oid,spcname,spcowner,spcacl,spcoptions FROM pg_tablespace ORDER BY oid", [][]any{
			{int64(1663), def, ownerOID, "{ts_lifecycle_control_owner=C/ts_lifecycle_control_owner,ts_lifecycle_control_reader=C/ts_lifecycle_control_owner}", nil},
			{int64(1664), global, ownerOID, nil, nil},
		}))
		x.both(t, fixture.Select("SELECT spcname,has_tablespace_privilege('ts_lifecycle_control_owner',spcname,'CREATE') AS owner_create,has_tablespace_privilege('ts_lifecycle_control_owner',spcname,'CREATE WITH GRANT OPTION') AS owner_delegates,has_tablespace_privilege('ts_lifecycle_control_reader',spcname,'CREATE') AS reader_create,has_tablespace_privilege('ts_lifecycle_control_reader',spcname,'CREATE WITH GRANT OPTION') AS reader_delegates FROM pg_tablespace ORDER BY oid", []string{"spcname", "owner_create", "owner_delegates", "reader_create", "reader_delegates"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
			{def, true, true, true, false},
			{global, true, true, false, false},
		}))
		x.both(t, fixture.Select("SELECT oid::bigint AS oid,spcname FROM pg_tablespace ORDER BY oid", []string{"oid", "spcname"}, []string{"INT8", "NAME"}, [][]any{{int64(1663), def}, {int64(1664), global}}))
		x.both(t, fixture.Select("SELECT pg_describe_object(1213,1663,0) AS description,pg_tablespace_location(1663) AS location", []string{"description", "location"}, []string{"TEXT", "TEXT"}, [][]any{{"tablespace " + def, ""}}))
		x.both(t, fixture.Select("SELECT tablespace FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_tablespace'", []string{"tablespace"}, []string{"NAME"}, [][]any{{global}}))
	}
	check(t, s)
	check(t, peer)
	// Cached OID1664 ended with the transaction. Stale setting text is
	// retained, then re-resolved/ignored in the next transaction, not aliased.
	s.exec(t, "CREATE TEMP TABLE ts_lifecycle_control_cache_after(a integer)")
	s.exec(t, "DROP TABLE ts_lifecycle_control_cache_after")
	s.exec(t, "RESET temp_tablespaces")
	s.exec(t, "CREATE DATABASE ts_lifecycle_control_after")
	later, closeLater := lifecyclePeer(t, s, "ts_lifecycle_control_after")
	defer closeLater()
	check(t, later)
	// Exchange the two live names through a temporary spelling in one commit.
	// Every peer must vacate all old keys before installing either new binding.
	for _, swapped := range []bool{true, false} {
		from, to := def, global
		if !swapped {
			from, to = global, def
		}
		s.begin(t)
		s.exec(t, "ALTER TABLESPACE "+lifecycleQuote(from)+" RENAME TO ts_lifecycle_exchange")
		s.step(t, fixture.Prepared(fixture.Exec("ALTER TABLESPACE "+lifecycleQuote(to)+" RENAME TO "+lifecycleQuote(from), 0)))
		s.exec(t, "ALTER TABLESPACE ts_lifecycle_exchange RENAME TO "+lifecycleQuote(to))
		s.exec(t, "COMMIT")
		s.inTx = false
		names := []string{def, global}
		if swapped {
			names = []string{global, def}
		}
		for _, x := range []*lifecycleCase{s, peer, later} {
			x.both(t, fixture.Select("SELECT oid::bigint AS oid,spcname FROM pg_tablespace ORDER BY oid", []string{"oid", "spcname"}, []string{"INT8", "NAME"}, [][]any{{int64(1663), names[0]}, {int64(1664), names[1]}}))
			for i, name := range names {
				x.both(t, fixture.Select("SELECT objid::bigint AS objid FROM pg_get_object_address('tablespace',ARRAY[$1],ARRAY[]::text[])", []string{"objid"}, []string{"INT8"}, [][]any{{int64(1663 + i)}}, name))
			}
			x.count(t, "SELECT count(*) AS n FROM pg_tablespace WHERE spcname='ts_lifecycle_exchange'", 0)
		}
	}
	check(t, s)
	check(t, peer)
	check(t, later)
	// A separate public Driver must not inherit this cluster's shared image.
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("tablespace-lifecycle-isolated")
	if err != nil {
		t.Fatal(err)
	}
	isolated, isolatedRaw, isolatedRec := fixture.DatabaseConnectorScenarioPool(t, connector, false)
	defer isolated.Close()
	defer isolatedRaw.Close()
	tablespaceBuiltins(t, s.p, fixture.Backend{Recorder: isolatedRec}, isolated)
	for _, name := range []string{def, global} {
		s.exec(t, "SET ROLE ts_lifecycle_control_owner")
		s.error(t, "DROP TABLESPACE "+lifecycleQuote(name), tablespaceDiagnostic("42501", "permission denied for tablespace "+name), false)
		s.exec(t, "RESET ROLE")
	}
	check(t, s)
	check(t, peer)
	check(t, later)
	if controls {
		targets := []*lifecycleCase{s, peer, later}
		values := []int64{42, 7, 99}
		attachments := make([]fixture.Step, len(targets))
		for i, x := range targets {
			x.exec(t, "CREATE TABLE ts_lifecycle_control_data(v integer)")
			x.step(t, fixture.Exec("INSERT INTO ts_lifecycle_control_data VALUES ($1)", 1, values[i]))
			// Capture actual attachment identities before replacement. These are
			// preservation expectations, not a portable database-OID oracle.
			query := "SELECT oid::bigint AS oid,datname,datdba::bigint AS owner_oid,datistemplate,datallowconn,datconnlimit,datcollversion,datacl::text AS acl FROM pg_database WHERE datname=current_database()"
			row := make([]any, 8)
			dest := make([]any, len(row))
			for j := range row {
				dest[j] = &row[j]
			}
			if err := x.c.QueryRowContext(s.p.Ctx, query).Scan(dest...); err != nil {
				t.Fatal(err)
			}
			for j := range row {
				row[j] = fixture.Normalize(row[j])
			}
			attachments[i] = fixture.Select(query, []string{"oid", "datname", "owner_oid", "datistemplate", "datallowconn", "datconnlimit", "datcollversion", "acl"}, []string{"INT8", "NAME", "INT8", "BOOL", "BOOL", "INT4", "TEXT", "TEXT"}, [][]any{row})
		}
		var dump strings.Builder
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		script := dump.String()
		if strings.Contains(script, "CREATE TABLESPACE") || strings.Contains(script, " LOCATION ") {
			t.Fatal("dump claims physical tablespace creation")
		}
		for _, name := range []string{def, global} {
			seed := "pg_default"
			if name == global {
				seed = "pg_global"
			}
			rename := "ALTER TABLESPACE " + lifecycleQuote(seed) + " RENAME TO " + lifecycleQuote(name)
			at := strings.Index(script, rename)
			locator := "WHERE spcname=E'" + strings.ReplaceAll(name, "'", "''") + "'::pg_catalog.name"
			owner, acl := -1, -1
			for _, line := range strings.SplitAfter(script, "\n") {
				if strings.Contains(line, locator) {
					if strings.HasPrefix(line, "UPDATE pg_catalog.pg_tablespace SET spcowner=") {
						owner = strings.Index(script, line)
					}
					if strings.HasPrefix(line, "UPDATE pg_catalog.pg_tablespace SET spcacl=") {
						acl = strings.Index(script, line)
					}
				}
			}
			if at < 0 || owner < 0 || acl < 0 || at >= owner || at >= acl {
				t.Fatalf("rename must precede owner and ACL repairs: %s\n%s", rename, script)
			}
		}
		ioFailure := errors.New("tablespace lifecycle output failure")
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, fixture.CatalogControlFailingWriter{Err: ioFailure}); !errors.Is(err, ioFailure) {
			t.Fatal("Dump output error", err)
		}
		unchanged := func() {
			t.Helper()
			check(t, s)
			check(t, peer)
			check(t, later)
			for i, x := range targets {
				x.both(t, attachments[i])
				x.both(t, fixture.Select("SELECT v FROM ts_lifecycle_control_data", []string{"v"}, []string{"INT4"}, [][]any{{values[i]}}))
				x.count(t, "SELECT count(*) AS n FROM pg_class WHERE relname='ts_lifecycle_unpublished'", 0)
			}
			var after strings.Builder
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if after.String() != script {
				t.Fatal("failed control changed captured state")
			}
		}
		unchanged()
		// Replay starts from canonical seed spellings even while the destination
		// already has aliases. Its rename SQL must remain a real replay operation.
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(script)); err != nil {
			t.Fatal("restore alias-bearing dump over aliased destination", err)
		}
		unchanged()
		// Destination alias A is not an implicit stage binding. Even after
		// genuine local work and a valid shared OWNER/ACL delta, missing alias
		// setup, a different stage alias B, and the displaced canonical name
		// must reject the entire replacement through the same exact boundary.
		oldData, err := s.c.QueryContext(s.p.Ctx, "SELECT v FROM ts_lifecycle_control_data")
		if err != nil {
			t.Fatal(err)
		}
		defer oldData.Close()
		staged := "CREATE TABLE ts_lifecycle_unpublished(v integer); INSERT INTO ts_lifecycle_unpublished VALUES (123); ALTER TABLESPACE pg_default RENAME TO " + lifecycleQuote(def) + "; UPDATE pg_catalog.pg_tablespace SET spcowner='postgres'::pg_catalog.regrole WHERE spcname=E'ts lifecycle \"default'::pg_catalog.name; UPDATE pg_catalog.pg_tablespace SET spcacl=pg_catalog.acldefault('c'::pg_catalog.\"char\",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE spcname=E'ts lifecycle \"default'::pg_catalog.name; "
		for _, probe := range []struct {
			name, setup, target string
		}{
			{"destination-alias-without-setup", "", global},
			{"wrong-stage-alias", "ALTER TABLESPACE pg_global RENAME TO ts_lifecycle_stage_other; ", global},
			{"displaced-canonical-name", "ALTER TABLESPACE pg_global RENAME TO ts_lifecycle_stage_other; ", "pg_global"},
		} {
			input := staged + probe.setup + "UPDATE pg_catalog.pg_tablespace SET spcacl='{ts_lifecycle_control_reader=C*/ts_lifecycle_control_owner}'::pg_catalog.aclitem[] WHERE spcname='" + probe.target + "'::pg_catalog.name;"
			err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(input))
			if err == nil || err.Error() != "stuntgres: missing object ACL repair target" {
				t.Fatal(probe.name, err)
			}
			unchanged()
		}
		if !oldData.Next() {
			t.Fatal("lost retained selected data", oldData.Err())
		}
		var oldValue int64
		if err := oldData.Scan(&oldValue); err != nil || oldValue != 42 {
			t.Fatal("retained selected data changed", oldValue, err)
		}
		if oldData.Next() || oldData.Err() != nil {
			t.Fatal("retained selected data boundary", oldData.Err())
		}
		oldData.Close()
		err = stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(script+";ALTER TABLESPACE ts_lifecycle_failed_restore RENAME TO ts_lifecycle_failed_new;"))
		d := lifecycleMissing("ts_lifecycle_failed_restore")
		fixture.AssertDiagnostic(t, err, &d)
		unchanged()
		ctx, cancel := context.WithCancel(s.p.Ctx)
		err = stuntgres.Restore(ctx, s.p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		unchanged()
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
			t.Fatal(err)
		}
		// Reset owns canonical names, not shared authority. Assert the complete
		// retained OWNER/raw ACL/options image and effective grant options on all
		// targets; the original retained Rows below still carry bootstrap state.
		for _, x := range []*lifecycleCase{s, peer, later} {
			x.both(t, tablespaceStep(x.b, "SELECT oid,spcname,spcowner,spcacl,spcoptions FROM pg_tablespace WHERE oid=1663 OR oid=1664 ORDER BY oid", [][]any{
				{int64(1663), "pg_default", ownerOID, "{ts_lifecycle_control_owner=C/ts_lifecycle_control_owner,ts_lifecycle_control_reader=C/ts_lifecycle_control_owner}", nil},
				{int64(1664), "pg_global", ownerOID, nil, nil},
			}))
			x.owner(t, "pg_default", "ts_lifecycle_control_owner", "{ts_lifecycle_control_owner=C/ts_lifecycle_control_owner,ts_lifecycle_control_reader=C/ts_lifecycle_control_owner}")
			x.owner(t, "pg_global", "ts_lifecycle_control_owner", "")
			x.both(t, fixture.Select("SELECT spcname,has_tablespace_privilege('ts_lifecycle_control_owner',spcname,'CREATE') AS owner_create,has_tablespace_privilege('ts_lifecycle_control_owner',spcname,'CREATE WITH GRANT OPTION') AS owner_delegates,has_tablespace_privilege('ts_lifecycle_control_reader',spcname,'CREATE') AS reader_create,has_tablespace_privilege('ts_lifecycle_control_reader',spcname,'CREATE WITH GRANT OPTION') AS reader_delegates FROM pg_tablespace ORDER BY oid", []string{"spcname", "owner_create", "owner_delegates", "reader_create", "reader_delegates"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
				{"pg_default", true, true, true, false},
				{"pg_global", true, true, false, false},
			}))
			for i, name := range []string{"pg_default", "pg_global"} {
				x.both(t, fixture.Select("SELECT objid::bigint AS objid FROM pg_get_object_address('tablespace',ARRAY[$1],ARRAY[]::text[])", []string{"objid"}, []string{"INT8"}, [][]any{{int64(1663 + i)}}, name))
			}
			x.both(t, fixture.Select("SELECT tablespace FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_tablespace'", []string{"tablespace"}, []string{"NAME"}, [][]any{{"pg_global"}}))
		}
		var canonical strings.Builder
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &canonical); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(canonical.String(), "UPDATE pg_catalog.pg_tablespace SET ") {
			t.Fatal("canonical-name ordinary dump serialized shared authority", canonical.String())
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		check(t, s)
		check(t, peer)
		check(t, later)
		// Deliberate native input establishes its candidate aliases before a
		// canonical repair resolves that exact stage name to builtin OID1664.
		manual := "ALTER TABLESPACE pg_default RENAME TO " + lifecycleQuote(def) + "; ALTER TABLESPACE pg_global RENAME TO " + lifecycleQuote(global) + "; UPDATE pg_catalog.pg_tablespace SET spcacl='{ts_lifecycle_control_reader=C*/ts_lifecycle_control_owner}'::pg_catalog.aclitem[] WHERE spcname='PG_lifecycle_global'::pg_catalog.name;"
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(manual)); err != nil {
			t.Fatal("explicit alias setup and canonical repair", err)
		}
		for _, x := range []*lifecycleCase{s, peer, later} {
			x.both(t, fixture.Select("SELECT oid::bigint AS oid,spcname,spcowner::regrole::text AS owner_name,spcacl::text AS acl,has_tablespace_privilege('ts_lifecycle_control_reader','PG_lifecycle_global','CREATE WITH GRANT OPTION') AS delegates FROM pg_tablespace WHERE oid=1664", []string{"oid", "spcname", "owner_name", "acl", "delegates"}, []string{"INT8", "NAME", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1664), global, "ts_lifecycle_control_owner", "{ts_lifecycle_control_reader=C*/ts_lifecycle_control_owner}", true}}))
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(script)); err != nil {
			t.Fatal("restore captured authority after explicit repair", err)
		}
		check(t, s)
		check(t, peer)
		check(t, later)
		// No explicit delta: candidate names win, while a genuine shared grant
		// committed during reader I/O remains current in every selected target.
		const retainedAlias = "PG_lifecycle_global_retained"
		aliases := "ALTER TABLESPACE pg_default RENAME TO " + lifecycleQuote(def) + "; ALTER TABLESPACE pg_global RENAME TO " + lifecycleQuote(retainedAlias) + ";"
		reader := &databaseAlterRepairReader{source: strings.NewReader(aliases), before: func() {
			s.exec(t, "GRANT CREATE ON TABLESPACE "+lifecycleQuote(global)+" TO ts_lifecycle_control_reader WITH GRANT OPTION")
		}}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, reader); err != nil {
			t.Fatal("candidate alias with latest shared authority", err)
		}
		for _, x := range []*lifecycleCase{s, peer, later} {
			x.both(t, fixture.Select("SELECT oid::bigint AS oid,spcname,spcowner::regrole::text AS owner_name,spcacl IS NOT NULL AS explicit_acl,has_tablespace_privilege('ts_lifecycle_control_reader','PG_lifecycle_global_retained','CREATE') AS ordinary,has_tablespace_privilege('ts_lifecycle_control_reader','PG_lifecycle_global_retained','CREATE WITH GRANT OPTION') AS delegates FROM pg_tablespace WHERE oid=1664", []string{"oid", "spcname", "owner_name", "explicit_acl", "ordinary", "delegates"}, []string{"INT8", "NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1664), retainedAlias, "ts_lifecycle_control_owner", true, true, true}}))
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(script)); err != nil {
			t.Fatal("restore captured image after ordinary alias replacement", err)
		}
		check(t, s)
		check(t, peer)
		check(t, later)
		// A single alias carries only its own exact authority. The canonical
		// peer retains a later shared grant, rather than importing a snapshot.
		// Only native Reset reestablishes reserved canonical spellings;
		// ordinary ALTER cannot rename an alias back into the pg_ namespace.
		if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
			t.Fatal("canonicalize names for mixed alias image", err)
		}
		s.exec(t, "ALTER TABLESPACE pg_default RENAME TO "+lifecycleQuote(def))
		for _, x := range []*lifecycleCase{s, peer, later} {
			x.both(t, tablespaceStep(x.b, "SELECT oid,spcname,spcowner,spcacl,spcoptions FROM pg_tablespace WHERE oid=1663 OR oid=1664 ORDER BY oid", [][]any{
				{int64(1663), def, ownerOID, "{ts_lifecycle_control_owner=C/ts_lifecycle_control_owner,ts_lifecycle_control_reader=C/ts_lifecycle_control_owner}", nil},
				{int64(1664), "pg_global", ownerOID, nil, nil},
			}))
		}
		var mixed strings.Builder
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &mixed); err != nil {
			t.Fatal(err)
		}
		mixedScript := mixed.String()
		if strings.Contains(mixedScript, "WHERE spcname=E'pg_global'::pg_catalog.name") {
			t.Fatal("alias image serialized canonical peer authority", mixedScript)
		}
		s.exec(t, "GRANT CREATE ON TABLESPACE "+lifecycleQuote(def)+" TO ts_lifecycle_control_reader WITH GRANT OPTION")
		s.exec(t, "GRANT CREATE ON TABLESPACE pg_global TO ts_lifecycle_control_reader WITH GRANT OPTION")
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(mixedScript)); err != nil {
			t.Fatal("restore single-alias authority image", err)
		}
		for _, x := range []*lifecycleCase{s, peer, later} {
			x.owner(t, def, "ts_lifecycle_control_owner", "{ts_lifecycle_control_owner=C/ts_lifecycle_control_owner,ts_lifecycle_control_reader=C/ts_lifecycle_control_owner}")
			x.both(t, fixture.Select("SELECT oid::bigint AS oid,spcname,spcowner::regrole::text AS owner_name,spcacl::text AS acl,has_tablespace_privilege('ts_lifecycle_control_reader',spcname,'CREATE WITH GRANT OPTION') AS delegates FROM pg_tablespace ORDER BY oid", []string{"oid", "spcname", "owner_name", "acl", "delegates"}, []string{"INT8", "NAME", "TEXT", "TEXT", "BOOL"}, [][]any{
				{int64(1663), def, "ts_lifecycle_control_owner", "{ts_lifecycle_control_owner=C/ts_lifecycle_control_owner,ts_lifecycle_control_reader=C/ts_lifecycle_control_owner}", false},
				{int64(1664), "pg_global", "ts_lifecycle_control_owner", "{ts_lifecycle_control_owner=C/ts_lifecycle_control_owner,ts_lifecycle_control_reader=C*/ts_lifecycle_control_owner}", true},
			}))
		}
		if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(script)); err != nil {
			t.Fatal("restore full alias image after canonical peer retention", err)
		}
		check(t, s)
		check(t, peer)
		check(t, later)
	}
	for _, want := range []struct {
		oid  int64
		name string
	}{{1663, "pg_default"}, {1664, "pg_global"}} {
		if !retained.Next() {
			t.Fatal("lost retained tablespace", retained.Err())
		}
		var oid, owner int64
		var name string
		var acl, options any
		if err := retained.Scan(&oid, &name, &owner, &acl, &options); err != nil {
			t.Fatal(err)
		}
		if oid != want.oid || name != want.name || owner != 10 || acl != nil || options != nil {
			t.Fatal("retained immutable image changed", oid, name, owner, acl, options)
		}
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatal("retained cursor boundary", retained.Err())
	}
	retained.Close()
	tablespaceMetadata(t, metadata, false)
}
