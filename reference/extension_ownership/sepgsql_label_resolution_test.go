package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 seclabel.c ExecSecLabelStmt and objectaddress.c establish provider,
// generic support, actual lookup/lock, ownership, then provider-hook ordering.
// Only the ordinary phase runs on PostgreSQL: no fake active oracle provider.
func TestReferenceSepgsqlSimulationGenericLabelResolution(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sepgsql_resolution_owner", "sepgsql_resolution_reader", "sepgsql_resolution_member"})
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	fixture.SubscriptionReferenceCleanup(t, []string{"sepgsql_resolution_sub"})
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlLabelResolution(t, p, b, c, run, false, &databases)
	}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		sepgsqlLabelResolution(t, p, b, c, run, true, &databases)
	})
}

func sepgsqlLabelResolution(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun, active bool, databases *[]string) {
	t.Helper()
	const subscription = "sepgsql_resolution_sub"
	const peerName = "sepgsql_resolution_peer"
	const lo = "921841"
	const missingLO = "921842"
	// All declarations are disconnected; neither label resolution nor its
	// ordinary ownership witnesses open publisher transport or create slots.
	run(fixture.Exec("CREATE DATABASE "+peerName, 0))
	if b.PG {
		*databases = append(*databases, peerName)
	}
	pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, peerName, "")
	peer, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peerRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, recorder, s) }
	defer func() {
		for _, q := range []string{"ROLLBACK", "RESET ROLE", "DROP SUBSCRIPTION IF EXISTS " + subscription, "DROP PUBLICATION IF EXISTS sepgsql_resolution_pub", "DROP EVENT TRIGGER IF EXISTS sepgsql_resolution_evt", "SELECT lo_unlink(oid) FROM pg_largeobject_metadata WHERE oid=" + lo + "::oid"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error("local resolution cleanup", q, err)
			}
		}
		if _, err := peer.ExecContext(p.Ctx, "DROP SUBSCRIPTION IF EXISTS "+subscription); err != nil {
			t.Error("peer resolution cleanup", err)
		}
	}()
	peerRun(fixture.Exec("CREATE SUBSCRIPTION "+subscription+" CONNECTION 'application_name=sepgsql_resolution_peer' PUBLICATION resolution_peer WITH (connect=false,slot_name=NONE)", 0))
	both := func(s fixture.Step) {
		t.Helper()
		run(s)
		run(fixture.Prepared(s))
	}
	failure := func(q, code, message string) {
		t.Helper()
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
		s.ErrorStage = "execute"
		both(s)
	}
	providerMessage := `security label provider "selinux" is not loaded`
	unsupported := "SELinux: security labels are not supported for this type of object"
	// A genuine same-name peer subscription must not satisfy current-db lookup.
	if active {
		failure("SECURITY LABEL FOR selinux ON SUBSCRIPTION "+subscription+" IS NULL", "42704", `subscription "`+subscription+`" does not exist`)
	} else {
		failure("SECURITY LABEL FOR selinux ON SUBSCRIPTION "+subscription+" IS NULL", "22023", providerMessage)
		failure("ALTER SUBSCRIPTION "+subscription+" RENAME TO sepgsql_resolution_unused", "42704", `subscription "`+subscription+`" does not exist`)
	}
	for _, q := range []string{
		"CREATE ROLE sepgsql_resolution_owner SUPERUSER",
		"CREATE ROLE sepgsql_resolution_reader",
		"CREATE ROLE sepgsql_resolution_member INHERIT",
		"GRANT sepgsql_resolution_owner TO sepgsql_resolution_member",
		"GRANT USAGE ON SCHEMA public TO sepgsql_resolution_owner,sepgsql_resolution_reader,sepgsql_resolution_member",
		"CREATE TABLE sepgsql_resolution_data(v integer)",
		"CREATE PUBLICATION sepgsql_resolution_pub FOR TABLE sepgsql_resolution_data",
		"CREATE FUNCTION sepgsql_resolution_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RETURN; END$$",
		"CREATE EVENT TRIGGER sepgsql_resolution_evt ON ddl_command_end WHEN TAG IN ('ALTER TABLE') EXECUTE FUNCTION sepgsql_resolution_fn()",
		"ALTER EVENT TRIGGER sepgsql_resolution_evt DISABLE",
		"CREATE SUBSCRIPTION " + subscription + " CONNECTION 'application_name=sepgsql_resolution_local' PUBLICATION resolution_local WITH (connect=false,slot_name=NONE)",
		"CREATE TYPE sepgsql_resolution_type AS ENUM ('v')",
		"CREATE DOMAIN sepgsql_resolution_domain AS integer",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO sepgsql_resolution_data VALUES(41)", 1))
	run(fixture.Select("SELECT lo_from_bytea("+lo+"::oid,decode('00abff','hex'))::bigint AS object", []string{"object"}, []string{"INT8"}, [][]any{{int64(921841)}}))
	families := []struct {
		kind, name, missing string
	}{
		{"PUBLICATION", "sepgsql_resolution_pub", "sepgsql_resolution_absent_pub"},
		{"EVENT TRIGGER", "sepgsql_resolution_evt", "sepgsql_resolution_absent_evt"},
		{"SUBSCRIPTION", subscription, "sepgsql_resolution_absent_sub"},
		{"LARGE OBJECT", lo, missingLO},
		{"TYPE", "sepgsql_resolution_type", "sepgsql_resolution_absent_type"},
		{"DOMAIN", "sepgsql_resolution_domain", "sepgsql_resolution_absent_domain"},
	}
	missingMessage := func(kind, name string) string {
		switch kind {
		case "PUBLICATION":
			return fmt.Sprintf(`publication "%s" does not exist`, name)
		case "EVENT TRIGGER":
			return fmt.Sprintf(`event trigger "%s" does not exist`, name)
		case "SUBSCRIPTION":
			return fmt.Sprintf(`subscription "%s" does not exist`, name)
		case "TYPE", "DOMAIN":
			return fmt.Sprintf(`type "%s" does not exist`, name)
		default:
			return "large object " + name + " does not exist"
		}
	}
	ownerMessage := func(kind, name string) string {
		switch kind {
		case "PUBLICATION":
			return "must be owner of publication " + name
		case "EVENT TRIGGER":
			return "must be owner of event trigger " + name
		case "SUBSCRIPTION":
			return "must be owner of subscription " + name
		case "TYPE", "DOMAIN":
			return "must be owner of type " + name
		default:
			return "must be owner of large object " + name
		}
	}
	// Missing and existing admin-owned objects, including invalid label text:
	// ordinary/inactive always fails provider selection before either lookup.
	for _, f := range families {
		for _, name := range []string{f.missing, f.name} {
			failure("SECURITY LABEL FOR absent_provider ON "+f.kind+" "+name+" IS 'invalid'", "22023", `security label provider "absent_provider" is not loaded`)
		}
		if active {
			failure("SECURITY LABEL FOR selinux ON "+f.kind+" "+f.missing+" IS NULL", "42704", missingMessage(f.kind, f.missing))
		} else {
			failure("SECURITY LABEL FOR selinux ON "+f.kind+" "+f.missing+" IS NULL", "22023", providerMessage)
		}
		for _, text := range []string{"NULL", "''", "'invalid'", "'" + sepgsqlP + "'"} {
			code, message := "22023", providerMessage
			if active {
				code, message = "0A000", unsupported
			}
			failure("SECURITY LABEL FOR selinux ON "+f.kind+" "+f.name+" IS "+text, code, message)
		}
	}
	// Tablespace metadata is genuinely supported without physical CREATE.
	// Do not rename/reown the pinned oracle's builtins: current administrative
	// ownership and a real unrelated role establish both admission branches.
	for _, name := range []string{"pg_default", "pg_global"} {
		const missing = "sepgsql_resolution_absent_space"
		failure("SECURITY LABEL FOR absent_provider ON TABLESPACE "+name+" IS 'invalid'", "22023", `security label provider "absent_provider" is not loaded`)
		failure("SECURITY LABEL FOR absent_provider ON TABLESPACE "+missing+" IS 'invalid'", "22023", `security label provider "absent_provider" is not loaded`)
		code, message := "22023", providerMessage
		missingCode, missingText := "22023", providerMessage
		if active {
			code, message = "0A000", unsupported
			missingCode, missingText = "42704", `tablespace "`+missing+`" does not exist`
		}
		failure("SECURITY LABEL FOR selinux ON TABLESPACE "+missing+" IS 'invalid'", missingCode, missingText)
		q := "SECURITY LABEL FOR selinux ON TABLESPACE " + name + " IS 'invalid'"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"", "sepgsql_resolution_reader", ""} {
			if role != "" {
				run(fixture.Exec("SET ROLE "+role, 0))
			}
			roleCode, roleMessage := code, message
			if active && role != "" {
				roleCode, roleMessage = "42501", "must be owner of tablespace "+name
			}
			for _, text := range []string{"NULL", "''", "'invalid'", "'" + sepgsqlP + "'"} {
				failure("SECURITY LABEL FOR selinux ON TABLESPACE "+name+" IS "+text, roleCode, roleMessage)
			}
			failure("SECURITY LABEL FOR selinux ON TABLESPACE "+missing+" IS NULL", missingCode, missingText)
			failure("SECURITY LABEL FOR absent_provider ON TABLESPACE "+name+" IS 'invalid'", "22023", `security label provider "absent_provider" is not loaded`)
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: roleCode, Message: roleMessage})
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, step)
			if !active && role == "sepgsql_resolution_reader" {
				failure("ALTER TABLESPACE "+name+" RENAME TO sepgsql_resolution_unused_space", "42501", "permission denied for tablespace "+name)
			}
			run(fixture.Exec("RESET ROLE", 0))
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		both(sepgsqlCount("SELECT count(*) FROM pg_tablespace WHERE oid="+map[string]string{"pg_default": "1663", "pg_global": "1664"}[name]+"::oid AND spcname='"+name+"' AND spcowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)", 1))
	}
	if !active {
		failure("ALTER TABLESPACE sepgsql_resolution_absent_space RENAME TO sepgsql_resolution_unused_space", "42704", `tablespace "sepgsql_resolution_absent_space" does not exist`)
	}
	for _, f := range families {
		run(fixture.Exec("ALTER "+f.kind+" "+f.name+" OWNER TO sepgsql_resolution_owner", 0))
	}
	run(fixture.Exec("ALTER ROLE sepgsql_resolution_owner NOSUPERUSER", 0))
	// Capture only allocator-dependent OIDs. Every other expected field below
	// comes from the independent declarations, not from a native golden dump.
	var publicationOID, eventOID, localOID, peerOID, ownerOID, typeOID, domainOID int64
	for _, capture := range []struct {
		q   string
		out *int64
	}{
		{"SELECT oid::bigint FROM pg_publication WHERE pubname='sepgsql_resolution_pub'", &publicationOID},
		{"SELECT oid::bigint FROM pg_event_trigger WHERE evtname='sepgsql_resolution_evt'", &eventOID},
		{"SELECT oid::bigint FROM pg_subscription WHERE subname='" + subscription + "' AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())", &localOID},
		{"SELECT oid::bigint FROM pg_subscription WHERE subname='" + subscription + "' AND subdbid<>(SELECT oid FROM pg_database WHERE datname=current_database())", &peerOID},
		{"SELECT oid::bigint FROM pg_roles WHERE rolname='sepgsql_resolution_owner'", &ownerOID},
		{"SELECT oid::bigint FROM pg_type WHERE typname='sepgsql_resolution_type' AND typnamespace='public'::regnamespace", &typeOID},
		{"SELECT oid::bigint FROM pg_type WHERE typname='sepgsql_resolution_domain' AND typnamespace='public'::regnamespace", &domainOID},
	} {
		if err := c.QueryRowContext(p.Ctx, capture.q).Scan(capture.out); err != nil || *capture.out <= 0 {
			t.Fatal("actual resolution identity", capture.q, *capture.out, err)
		}
	}
	if localOID == peerOID {
		t.Fatal("same-name subscriptions did not retain distinct real identities")
	}
	unchanged := func() {
		t.Helper()
		both(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE classoid IN ('pg_publication'::regclass,'pg_event_trigger'::regclass,'pg_largeobject'::regclass,'pg_type'::regclass)", 0))
		both(sepgsqlCount("SELECT count(*) FROM pg_shseclabel WHERE classoid IN ('pg_subscription'::regclass,'pg_tablespace'::regclass)", 0))
		both(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE objtype IN ('publication','event trigger','large object','subscription','type','domain','tablespace')", 0))
		both(sepgsqlCount(fmt.Sprintf("SELECT count(*) FROM pg_publication WHERE oid=%d::oid AND pubname='sepgsql_resolution_pub' AND pubowner=%d::oid AND NOT puballtables AND pubinsert AND pubupdate AND pubdelete AND pubtruncate", publicationOID, ownerOID), 1))
		both(sepgsqlCount("SELECT count(*) FROM pg_publication_tables WHERE pubname='sepgsql_resolution_pub' AND schemaname='public' AND tablename='sepgsql_resolution_data'", 1))
		both(sepgsqlCount(fmt.Sprintf("SELECT count(*) FROM pg_event_trigger WHERE oid=%d::oid AND evtname='sepgsql_resolution_evt' AND evtowner=%d::oid AND evtenabled='D' AND evtevent='ddl_command_end' AND evtfoid='sepgsql_resolution_fn()'::regprocedure AND evttags=ARRAY['ALTER TABLE']::text[]", eventOID, ownerOID), 1))
		both(sepgsqlCount(fmt.Sprintf("SELECT count(*) FROM pg_subscription WHERE oid=%d::oid AND subowner=%d::oid AND subname='%s' AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND subconninfo='application_name=sepgsql_resolution_local' AND NOT subenabled AND subslotname IS NULL AND subpublications=ARRAY['resolution_local']::text[]", localOID, ownerOID, subscription), 1))
		both(sepgsqlCount(fmt.Sprintf("SELECT count(*) FROM pg_subscription WHERE oid=%d::oid AND subname='%s' AND subconninfo='application_name=sepgsql_resolution_peer' AND NOT subenabled AND subslotname IS NULL", peerOID, subscription), 1))
		both(sepgsqlCount("SELECT count(*) FROM pg_largeobject_metadata WHERE oid="+lo+"::oid AND lomowner="+fmt.Sprint(ownerOID)+"::oid", 1))
		both(sepgsqlText("SELECT encode(lo_get("+lo+"::oid),'hex') AS bytes", "bytes", "00abff"))
		both(sepgsqlCount(fmt.Sprintf("SELECT count(*) FROM pg_type WHERE oid=%d::oid AND typname='sepgsql_resolution_type' AND typtype='e' AND typowner=%d::oid AND typnamespace='public'::regnamespace", typeOID, ownerOID), 1))
		both(sepgsqlCount(fmt.Sprintf("SELECT count(*) FROM pg_type WHERE oid=%d::oid AND typname='sepgsql_resolution_domain' AND typtype='d' AND typbasetype='integer'::regtype AND typowner=%d::oid AND typnamespace='public'::regnamespace", domainOID, ownerOID), 1))
		both(sepgsqlCount("SELECT count(*) FROM pg_tablespace WHERE (oid=1663::oid AND spcname='pg_default' OR oid=1664::oid AND spcname='pg_global') AND spcowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)", 2))
		both(fixture.Select("SELECT v FROM sepgsql_resolution_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
		if active {
			both(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='sepgsql_resolution_data'::regclass AND provider='selinux' AND label='"+sepgsqlP+"'", 8))
			both(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE classoid='pg_proc'::regclass AND objoid='sepgsql_resolution_fn()'::regprocedure AND provider='selinux' AND label='"+sepgsqlP+"'", 1))
		}
	}
	unchanged()
	// Prepare under administrative authority, execute repeatedly under genuinely
	// changed current roles. Neither Prepare nor an earlier rejection freezes it.
	for _, f := range families {
		q := "SECURITY LABEL FOR selinux ON " + f.kind + " " + f.name + " IS 'invalid'"
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"sepgsql_resolution_reader", "sepgsql_resolution_owner", "sepgsql_resolution_member"} {
			run(fixture.Exec("SET ROLE "+role, 0))
			both(sepgsqlText("SELECT current_user::text AS actor", "actor", role))
			if active {
				// The physical A subject grants independent MAC prerequisites;
				// changing SQL role must not substitute a different MAC subject.
				both(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
			}
			code, message := "22023", providerMessage
			if active {
				code, message = "0A000", unsupported
				if role == "sepgsql_resolution_reader" {
					code, message = "42501", ownerMessage(f.kind, f.name)
				}
			}
			for _, text := range []string{"NULL", "''", "'invalid'", "'" + sepgsqlP + "'"} {
				failure("SECURITY LABEL FOR selinux ON "+f.kind+" "+f.name+" IS "+text, code, message)
			}
			failure("SECURITY LABEL FOR absent_provider ON "+f.kind+" "+f.name+" IS NULL", "22023", `security label provider "absent_provider" is not loaded`)
			missingCode, missingText := "22023", providerMessage
			if active {
				missingCode, missingText = "42704", missingMessage(f.kind, f.missing)
			}
			failure("SECURITY LABEL FOR selinux ON "+f.kind+" "+f.missing+" IS 'invalid'", missingCode, missingText)
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, b.Recorder, step)
			run(fixture.Exec("RESET ROLE", 0))
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Existing LO compatibility affects ONLY ownership, never existence or
	// provider support; its effective value is read again at Execute.
	for _, setting := range []string{"off", "on"} {
		run(fixture.Exec("SET lo_compat_privileges="+setting, 0))
		run(fixture.Exec("SET ROLE sepgsql_resolution_reader", 0))
		code, message := "22023", providerMessage
		if active {
			code, message = "42501", "must be owner of large object "+lo
			if setting == "on" {
				code, message = "0A000", unsupported
			}
		}
		for _, text := range []string{"NULL", "''", "'invalid'"} {
			failure("SECURITY LABEL FOR selinux ON LARGE OBJECT "+lo+" IS "+text, code, message)
		}
		if active {
			failure("SECURITY LABEL FOR selinux ON LARGE OBJECT "+missingLO+" IS NULL", "42704", "large object "+missingLO+" does not exist")
		} else {
			failure("SECURITY LABEL FOR selinux ON LARGE OBJECT "+missingLO+" IS NULL", "22023", providerMessage)
		}
		// The same setting cannot bypass a publication's ordinary ownership.
		code, message = "22023", providerMessage
		if active {
			code, message = "42501", "must be owner of publication sepgsql_resolution_pub"
		}
		failure("SECURITY LABEL FOR selinux ON PUBLICATION sepgsql_resolution_pub IS NULL", code, message)
		run(fixture.Exec("RESET ROLE", 0))
	}
	run(fixture.Exec("SET lo_compat_privileges=off", 0))
	if !active {
		// Independent released ordinary owners establish the same lookup and
		// ownership diagnostics without pretending an active provider is loaded.
		// REL_18_6 alter.c AlterObjectRename_internal requires database
		// CREATE after ordinary publication/subscription ownership. Give only
		// the intended owner that real prerequisite; the unrelated reader
		// must still fail the unchanged object-owner diagnostic first.
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		databaseName := fixture.ExtensionTempIdentifier(database)
		run(fixture.Exec("GRANT CREATE ON DATABASE "+databaseName+" TO sepgsql_resolution_owner", 0))
		// Independent cleanup also covers failure inside the explicit rename
		// transaction, before the outer role/declaration cleanup can run.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"ROLLBACK", "RESET ROLE", "REVOKE CREATE ON DATABASE " + databaseName + " FROM sepgsql_resolution_owner"} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("ordinary owner prerequisite cleanup", q, err)
				}
			}
		}()
		for _, f := range families {
			q := "ALTER " + f.kind + " " + f.missing + " RENAME TO sepgsql_resolution_unused"
			if f.kind == "LARGE OBJECT" {
				q = "SELECT lo_unlink(" + f.missing + "::oid)"
			}
			failure(q, "42704", missingMessage(f.kind, f.missing))
			run(fixture.Exec("SET ROLE sepgsql_resolution_reader", 0))
			q = "ALTER " + f.kind + " " + f.name + " RENAME TO sepgsql_resolution_unused"
			if f.kind == "LARGE OBJECT" {
				q = "SELECT lo_unlink(" + f.name + "::oid)"
			}
			failure(q, "42501", ownerMessage(f.kind, f.name))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE sepgsql_resolution_owner", 0))
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT ordinary_owner", 0))
				step := fixture.Exec(q, 0)
				if f.kind == "LARGE OBJECT" {
					step = fixture.Select(q, []string{"lo_unlink"}, []string{"INT4"}, [][]any{{int64(1)}})
				}
				step.Prepared = prepared
				run(step)
				run(fixture.Exec("ROLLBACK TO ordinary_owner", 0))
				run(fixture.Exec("COMMIT", 0))
			}
			run(fixture.Exec("RESET ROLE", 0))
		}
		run(fixture.Exec("REVOKE CREATE ON DATABASE "+databaseName+" FROM sepgsql_resolution_owner", 0))
	}
	unchanged()
	peerRun(sepgsqlCount(fmt.Sprintf("SELECT count(*) FROM pg_subscription WHERE oid=%d::oid AND subname='%s' AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND subconninfo='application_name=sepgsql_resolution_peer' AND NOT subenabled AND subslotname IS NULL", peerOID, subscription), 1))
	peerRun(sepgsqlCount("SELECT count(*) FROM pg_shseclabel WHERE classoid='pg_subscription'::regclass", 0))
}
