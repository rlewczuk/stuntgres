package reference_test

import (
	"database/sql"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

// Active expectations follow REL_18_6 contrib/sepgsql/label.c: all shared
// databases precede current-database local objects, with ordinary superuser
// admission before opening the labeling handle. The ordinary phase never loads
// a fake provider or declares a substitute restorecon function.
func TestReferenceSepgsqlSimulationRestoreconSharedDatabases(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "denied_peer_atomicity"}[denied], func(t *testing.T) {
			simpleName := regexp.MustCompile(`^[a-z_][a-z0-9_$]*$`)
			var databases []string
			fixture.DatabaseReferenceCleanup(t, &databases)
			const peerName = "sepgsql.restorecon_peer"
			configure := func(config *stuntgres.SepgsqlSimulationConfig) {
				// The exact quoted peer entry must win. These contrary entries
				// expose using an unquoted or current-database-prefixed peer key.
				config.Policy.NamedSpecs["shared"] = "db_database \"" + peerName + "\" " + sepgsqlP + "\n" +
					"db_database *.\"" + peerName + "\" " + sepgsqlS + "\n" +
					"db_database " + peerName + " " + sepgsqlS + "\n" +
					"db_database *." + peerName + " " + sepgsqlS + "\n" +
					"db_database * " + sepgsqlP + "\n" +
					"db_schema * " + sepgsqlP + "\n" +
					"db_table * " + sepgsqlP + "\n" +
					"db_column * " + sepgsqlP + "\n" +
					"db_sequence * " + sepgsqlP + "\n" +
					"db_view * " + sepgsqlP + "\n" +
					"db_procedure * " + sepgsqlP + "\n"
				if denied {
					config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlA, Target: sepgsqlU, Class: "db_database", Deny: []string{"setattr", "relabelfrom"}})
				}
			}
			setup := func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun, active bool) {
				if backend.PG {
					databases = append(databases, peerName)
				}
				var currentName string
				var currentOID, peerOID int64
				if err := c.QueryRowContext(p.Ctx, "SELECT current_database()::text,oid::bigint FROM pg_database WHERE datname=current_database()").Scan(&currentName, &currentOID); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("CREATE DATABASE "+pq.QuoteIdentifier(peerName), 0))
				peerPool, peerNative, recorder := fixture.DatabaseScenarioPool(t, p, backend.PG, peerName, "")
				peer, err := peerPool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				peerRun := func(step fixture.Step) {
					t.Helper()
					fixture.RunStep(t, p.Ctx, peer, recorder, step)
				}
				if err := peer.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_database WHERE datname=current_database()").Scan(&peerOID); err != nil {
					t.Fatal(err)
				}
				if currentOID <= 0 || peerOID <= 0 || currentOID == peerOID || currentName == peerName {
					t.Fatal("distinct real database identities", currentName, currentOID, peerOID)
				}
				peerRun(sepgsqlText("SELECT current_database()::text AS name", "name", peerName))
				for _, execute := range []sepgsqlRun{run, peerRun} {
					for _, database := range []struct {
						name string
						oid  int64
					}{{currentName, currentOID}, {peerName, peerOID}} {
						step := fixture.Select("SELECT datname::text AS name,oid::bigint AS oid FROM pg_database WHERE datname="+pq.QuoteLiteral(database.name), []string{"name", "oid"}, []string{"TEXT", "INT8"}, [][]any{{database.name, database.oid}})
						execute(step)
						execute(fixture.Prepared(step))
					}
				}
				run(fixture.Exec("CREATE TABLE restorecon_local(v text)", 0))
				run(fixture.Exec("INSERT INTO restorecon_local VALUES('current kept')", 1))
				peerRun(fixture.Exec("CREATE TABLE restorecon_local(v text)", 0))
				peerRun(fixture.Exec("INSERT INTO restorecon_local VALUES('peer kept')", 1))
				assertData := func() {
					for _, prepared := range []bool{false, true} {
						step := sepgsqlText("SELECT v FROM restorecon_local", "v", "current kept")
						step.Prepared = prepared
						run(step)
						step.Rows = [][]any{{"peer kept"}}
						peerRun(step)
					}
				}
				assertShared := func(current, other string) {
					for _, execute := range []sepgsqlRun{run, peerRun} {
						for _, database := range []struct {
							name  string
							oid   int64
							label string
						}{{currentName, currentOID, current}, {peerName, peerOID, other}} {
							predicate := "classoid='pg_database'::regclass AND objoid=" + strconv.FormatInt(database.oid, 10) + " AND provider='selinux'"
							rows := [][]any{}
							presentation := [][]any{}
							if database.label != "" {
								rows = [][]any{{"selinux", database.label}}
								display := database.name
								// Fixture roots are either ordinary non-keyword names or
								// opaque generated names; the dotted peer must be quoted.
								if !simpleName.MatchString(display) {
									display = pq.QuoteIdentifier(display)
								}
								presentation = [][]any{{"database", nil, display, "selinux", database.label}}
							}
							for _, prepared := range []bool{false, true} {
								step := fixture.Select("SELECT provider,label FROM pg_shseclabel WHERE "+predicate, []string{"provider", "label"}, []string{"TEXT", "TEXT"}, rows)
								step.Prepared = prepared
								execute(step)
								step = fixture.Select("SELECT objtype,objnamespace,objname,provider,label FROM pg_seclabels WHERE "+predicate, []string{"objtype", "objnamespace", "objname", "provider", "label"}, []string{"TEXT", "OID", "TEXT", "TEXT", "TEXT"}, presentation)
								step.Prepared = prepared
								execute(step)
								step = sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE "+predicate, 0)
								step.Prepared = prepared
								execute(step)
							}
						}
					}
				}
				if !active {
					for _, prepared := range []bool{false, true} {
						for _, name := range []string{currentName, peerName} {
							step := fixture.ImplicitError("SECURITY LABEL FOR selinux ON DATABASE "+pq.QuoteIdentifier(name)+" IS '"+sepgsqlS+"'", fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"})
							step.Prepared = prepared
							run(step)
						}
					}
					assertShared("", "")
					assertData()
					return
				}
				peerRun(fixture.Exec("SECURITY LABEL ON TABLE restorecon_local IS '"+sepgsqlS+"'", 0))
				for _, prepared := range []bool{false, true} {
					current, other := sepgsqlS, sepgsqlU
					if prepared && !denied {
						current, other = sepgsqlU, sepgsqlS
					}
					run(fixture.Exec("SECURITY LABEL ON DATABASE "+pq.QuoteIdentifier(currentName)+" IS '"+current+"'", 0))
					// In the denied fixture the unchanged peer already has U on
					// the second attempt: do not require forbidden setup authority.
					if !prepared || !denied {
						run(fixture.Exec("SECURITY LABEL ON DATABASE "+pq.QuoteIdentifier(peerName)+" IS '"+other+"'", 0))
					}
					run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_local IS '"+sepgsqlS+"'", 0))
					assertShared(current, other)
					var labels, peerLabels [][]string
					if denied {
						labels = sepgsqlRestoreconLabels(t, p, c)
						peerLabels = sepgsqlRestoreconLabels(t, p, peer)
					}
					step := sepgsqlBool("SELECT sepgsql_restorecon($1) AS restored", "restored", true)
					step.Args = []any{"shared"}
					if denied {
						step = fixture.ImplicitError("SELECT sepgsql_restorecon($1) AS restored", fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation"}, "shared")
					}
					step.Prepared = prepared
					run(step)
					local := sepgsqlP
					if denied {
						local = sepgsqlS
						assertShared(current, other)
						if !reflect.DeepEqual(sepgsqlRestoreconLabels(t, p, c), labels) || !reflect.DeepEqual(sepgsqlRestoreconLabels(t, p, peer), peerLabels) {
							t.Fatal("denied peer relabel changed actual local/shared label associations")
						}
					} else {
						assertShared(sepgsqlP, sepgsqlP)
					}
					for _, preparedRead := range []bool{false, true} {
						step = sepgsqlText("SELECT label FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='restorecon_local'::regclass AND objsubid=0 AND provider='selinux'", "label", local)
						step.Prepared = preparedRead
						run(step)
						step.Rows = [][]any{{sepgsqlS}}
						peerRun(step)
					}
					assertData()
				}
				// Capture the actual post-peer image. Only the selected database
				// and its durable local labels/data belong to this Dump.
				run(fixture.Exec("SECURITY LABEL ON DATABASE "+pq.QuoteIdentifier(currentName)+" IS '"+sepgsqlS+"'", 0))
				if !denied {
					peerRun(fixture.Exec("SECURITY LABEL ON DATABASE "+pq.QuoteIdentifier(peerName)+" IS '"+sepgsqlU+"'", 0))
				}
				run(fixture.Exec("CREATE TABLE restorecon_control_roundtrip(v text)", 0))
				run(fixture.Exec("INSERT INTO restorecon_control_roundtrip VALUES('serialized current')", 1))
				for _, target := range []string{"TABLE restorecon_local", "COLUMN restorecon_local.v", "COLUMN restorecon_local.ctid"} {
					run(fixture.Exec("SECURITY LABEL ON "+target+" IS '"+sepgsqlS+"'", 0))
				}
				controlDump := fixture.ExtensionDump(t, p.Ctx, p.Native)
				currentSQL := "SECURITY LABEL FOR selinux ON DATABASE " + pq.QuoteIdentifier(currentName) + " IS E'" + sepgsqlS + "';"
				peerSQL := "SECURITY LABEL FOR selinux ON DATABASE " + pq.QuoteIdentifier(peerName) + " IS "
				// Native Dump uses explicit E literals for portable label text;
				// expected names still come from genuine public database identity.
				for _, statement := range []string{
					currentSQL,
					"SECURITY LABEL FOR selinux ON TABLE \"public\".\"restorecon_local\" IS E'" + sepgsqlS + "';",
					"SECURITY LABEL FOR selinux ON COLUMN \"public\".\"restorecon_local\".\"v\" IS E'" + sepgsqlS + "';",
					"SECURITY LABEL FOR selinux ON COLUMN \"public\".\"restorecon_local\".\"ctid\" IS E'" + sepgsqlS + "';",
				} {
					if strings.Count(controlDump, statement) != 1 {
						t.Fatal("post-peer dump omitted or duplicated selected label", statement)
					}
				}
				if !strings.Contains(controlDump, "stuntgres-sepgsql-simulation-v1") ||
					strings.Contains(controlDump, peerSQL) || strings.Contains(controlDump, "peer kept") {
					t.Fatal("selected dump lost registration or included peer state")
				}
				peerDump := fixture.ExtensionDump(t, p.Ctx, peerNative)
				if strings.Count(peerDump, peerSQL+"E'"+sepgsqlU+"';") != 1 ||
					strings.Contains(peerDump, "SECURITY LABEL FOR selinux ON DATABASE "+pq.QuoteIdentifier(currentName)+" IS ") ||
					!strings.Contains(peerDump, "SECURITY LABEL FOR selinux ON TABLE \"public\".\"restorecon_local\" IS E'"+sepgsqlS+"';") {
					t.Fatal("peer-as-selected dump lost its own durable labels or included another database")
				}
				assertShared(sepgsqlS, sepgsqlU)
				assertData()
				assertPeerLocal := func() {
					for _, prepared := range []bool{false, true} {
						step := sepgsqlText("SELECT v FROM restorecon_local", "v", "peer kept")
						step.Prepared = prepared
						peerRun(step)
						step = sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='restorecon_local'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlS)
						step.Prepared = prepared
						peerRun(step)
					}
				}
				assertPeerLocal()
				destinationLabels := []string{sepgsqlU}
				if !denied {
					destinationLabels = append(destinationLabels, sepgsqlR, "")
				}
				for _, destinationLabel := range destinationLabels {
					// Reuse exactly the same source image against genuinely
					// different destination peer state, including label absence.
					if destinationLabel != sepgsqlU {
						value := "NULL"
						if destinationLabel != "" {
							value = pq.QuoteLiteral(destinationLabel)
						}
						peerRun(fixture.Exec("SECURITY LABEL ON DATABASE "+pq.QuoteIdentifier(peerName)+" IS "+value, 0))
					}
					run(fixture.Exec("CREATE TABLE restorecon_reset_only(v integer)", 0))
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal("selected reset with retained destination peer", destinationLabel, err)
					}
					assertShared(sepgsqlP, destinationLabel)
					assertPeerLocal()
					for _, prepared := range []bool{false, true} {
						step := sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('restorecon_reset_only','restorecon_control_roundtrip','restorecon_local')", 0)
						step.Prepared = prepared
						run(step)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(controlDump)); err != nil {
						t.Fatal("post-peer selected restore with retained destination peer", destinationLabel, err)
					}
					assertShared(sepgsqlS, destinationLabel)
					assertData()
					assertPeerLocal()
					for _, prepared := range []bool{false, true} {
						step := sepgsqlText("SELECT v FROM restorecon_control_roundtrip", "v", "serialized current")
						step.Prepared = prepared
						run(step)
						step = sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='restorecon_reset_only'", 0)
						step.Prepared = prepared
						run(step)
						step = fixture.Select("SELECT objsubid,label FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='restorecon_local'::regclass AND provider='selinux' AND objsubid IN (-1,0,1) ORDER BY objsubid",
							[]string{"objsubid", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), sepgsqlS}, {int64(0), sepgsqlS}, {int64(1), sepgsqlS}})
						step.Prepared = prepared
						run(step)
					}
				}
			}
			sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				setup(t, p, backend, c, run, false)
			}, configure, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				setup(t, p, backend, c, run, true)
			})
		})
	}
}

func TestReferenceSepgsqlSimulationRestoreconAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"restorecon_reader"})
	setup := func(run sepgsqlRun) {
		run(fixture.Exec("CREATE ROLE restorecon_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO restorecon_reader", 0))
		run(fixture.Exec("CREATE TABLE restorecon_authorization(v text)", 0))
		run(fixture.Exec("INSERT INTO restorecon_authorization VALUES('unchanged')", 1))
		run(fixture.Exec("GRANT SELECT ON restorecon_authorization TO restorecon_reader", 0))
	}
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		run(fixture.Exec("SET ROLE restorecon_reader", 0))
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("SECURITY LABEL FOR selinux ON TABLE restorecon_authorization IS '"+sepgsqlS+"'", fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"})
			step.Prepared = prepared
			run(step)
		}
		run(sepgsqlText("SELECT v FROM restorecon_authorization", "v", "unchanged"))
		run(fixture.Exec("RESET ROLE", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname='sepgsql_restorecon'", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='restorecon_authorization'::regclass AND provider='selinux'", 0))
	}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		// SQL EXECUTE and the A subject's explicit wildcard MAC grant both
		// admit initialization/body entry. Only ordinary superuser status fails.
		run(fixture.Exec("REVOKE ALL ON FUNCTION sepgsql_restorecon(text) FROM PUBLIC", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION sepgsql_restorecon(text) TO restorecon_reader", 0))
		run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_authorization IS '"+sepgsqlS+"'", 0))
		initialLabels := sepgsqlRestoreconLabels(t, p, c)
		held, err := c.PrepareContext(p.Ctx, "SELECT sepgsql_restorecon($1) AS restored")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		denied := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: must be superuser to restore initial contexts"}
		missing := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "SELinux: failed to initialize labeling handle: No such file or directory"}
		assertUnchanged := func() {
			run(sepgsqlText("SELECT label FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='restorecon_authorization'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlS))
			run(sepgsqlText("SELECT v FROM restorecon_authorization", "v", "unchanged"))
			if !reflect.DeepEqual(sepgsqlRestoreconLabels(t, p, c), initialLabels) {
				t.Fatal("failed restorecon changed local/shared labels")
			}
		}
		for _, role := range []string{"SET ROLE restorecon_reader", "RESET ROLE", "SET ROLE restorecon_reader"} {
			run(fixture.Exec(role, 0))
			if role == "RESET ROLE" {
				var value bool
				fixture.AssertDiagnostic(t, held.QueryRowContext(p.Ctx, "not-registered-profile").Scan(&value), &missing)
				assertUnchanged()
				continue
			}
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
			for _, profile := range []any{nil, "public", "not-registered-profile"} {
				for _, prepared := range []bool{false, true} {
					step := fixture.ErrorDiagnostic("SELECT sepgsql_restorecon($1)", denied, profile)
					step.Prepared = prepared
					run(step)
					assertUnchanged()
				}
				var value bool
				fixture.AssertDiagnostic(t, held.QueryRowContext(p.Ctx, profile).Scan(&value), &denied)
				assertUnchanged()
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		startingLabels := []string{sepgsqlS, sepgsqlU, sepgsqlA, sepgsqlR}
		nextLabel := 0
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_authorization IS '"+sepgsqlS+"'", 0))
			step := fixture.ErrorDiagnostic("SELECT sepgsql_restorecon($1)", missing, "not-registered-profile")
			step.Prepared = prepared
			run(step)
			assertUnchanged()
			for _, profile := range []any{nil, "public"} {
				run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_authorization IS '"+startingLabels[nextLabel]+"'", 0))
				nextLabel++
				step = sepgsqlBool("SELECT sepgsql_restorecon($1) AS restored", "restored", true)
				step.Args, step.Prepared = []any{profile}, prepared
				run(step)
				run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='restorecon_authorization'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlP))
			}
		}
		// The very same handle prepared as admin and rejected after downgrade
		// now succeeds after actual SQL role restoration, with BOOL metadata.
		run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_authorization IS '"+sepgsqlT+"'", 0))
		rows, err := held.QueryContext(p.Ctx, "public")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 || columns[0].Name() != "restored" || columns[0].DatabaseTypeName() != "BOOL" {
			t.Fatal("held restorecon BOOL metadata", columns, err)
		}
		var restored bool
		if !rows.Next() {
			t.Fatal("held restorecon missing row", rows.Err())
		}
		if err := rows.Scan(&restored); err != nil || !restored {
			t.Fatal("held restorecon result", restored, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("held restorecon extra row/error", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='restorecon_authorization'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlP))
	})
}

// Literal bracket members and first-match ordering follow libselinux's
// fnmatch(..., 0) contract. The active phase is native simulation, not an
// assertion that the ordinary PostgreSQL fixture runs an SELinux provider.
func TestReferenceSepgsqlSimulationRestoreconEscapedBrackets(t *testing.T) {
	names := []string{"a", "b", "unrelated", "z"}
	setup := func(run sepgsqlRun) {
		run(fixture.Exec("CREATE SCHEMA restorecon_brackets", 0))
		for _, name := range names {
			run(fixture.Exec("CREATE TABLE restorecon_brackets."+name+"(v text)", 0))
			run(fixture.Exec("INSERT INTO restorecon_brackets."+name+" VALUES('"+name+" kept')", 1))
		}
	}
	assertOrdinary := func(run sepgsqlRun) {
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT c.relname::text AS name FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='restorecon_brackets' AND c.relkind='r' ORDER BY c.relname",
				[]string{"name"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"unrelated"}, {"z"}})
			step.Prepared = prepared
			run(step)
			for _, name := range names {
				step = sepgsqlText("SELECT v FROM restorecon_brackets."+name, "v", name+" kept")
				step.Prepared = prepared
				run(step)
			}
		}
	}
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("SECURITY LABEL FOR selinux ON TABLE restorecon_brackets.b IS '"+sepgsqlS+"'", fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"})
			step.Prepared = prepared
			run(step)
			step = sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname='sepgsql_restorecon'", 0)
			step.Prepared = prepared
			run(step)
			step = sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE provider='selinux'", 0)
			step.Prepared = prepared
			run(step)
		}
		assertOrdinary(run)
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.NamedSpecs["escaped-brackets"] =
			"db_table *.restorecon_brackets.[a\\-z] " + sepgsqlS + "\n" +
				"db_table *.restorecon_brackets.[a-z] " + sepgsqlP + "\n" +
				"db_table *.restorecon_brackets.unrelated " + sepgsqlT + "\n" +
				"db_column *.restorecon_brackets.unrelated.v " + sepgsqlS + "\n" +
				config.Policy.DefaultSpec
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		// Seed the independently expected complete catalog, then disturb only
		// the three target tables before each direct/prepared application.
		for _, target := range []struct {
			name, label string
		}{{"a", sepgsqlS}, {"b", sepgsqlP}, {"z", sepgsqlS}, {"unrelated", sepgsqlT}} {
			run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_brackets."+target.name+" IS '"+target.label+"'", 0))
		}
		run(fixture.Exec("SECURITY LABEL ON COLUMN restorecon_brackets.unrelated.v IS '"+sepgsqlS+"'", 0))
		expectedLabels := sepgsqlRestoreconLabels(t, p, c)
		for _, prepared := range []bool{false, true} {
			for _, name := range []string{"a", "b", "z"} {
				run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_brackets."+name+" IS '"+sepgsqlU+"'", 0))
			}
			step := sepgsqlBool("SELECT sepgsql_restorecon($1) AS restored", "restored", true)
			step.Args, step.Prepared = []any{"escaped-brackets"}, prepared
			run(step)
			// Actual pg_seclabel object/class/subobject associations, not
			// names or values calculated by the pattern compiler under test.
			for _, queryPrepared := range []bool{false, true} {
				step = fixture.Select("SELECT c.relname::text AS name,l.label FROM pg_seclabel l JOIN pg_class c ON c.oid=l.objoid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE l.classoid='pg_class'::regclass AND l.objsubid=0 AND l.provider='selinux' AND n.nspname='restorecon_brackets' ORDER BY c.relname",
					[]string{"name", "label"}, []string{"TEXT", "TEXT"}, [][]any{{"a", sepgsqlS}, {"b", sepgsqlP}, {"unrelated", sepgsqlT}, {"z", sepgsqlS}})
				step.Prepared = queryPrepared
				run(step)
				step = sepgsqlText("SELECT label FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='restorecon_brackets.unrelated'::regclass AND objsubid=1 AND provider='selinux'", "label", sepgsqlS)
				step.Prepared = queryPrepared
				run(step)
			}
			if !reflect.DeepEqual(sepgsqlRestoreconLabels(t, p, c), expectedLabels) {
				t.Fatal("restorecon did not restore the expected complete local/shared label associations")
			}
			assertOrdinary(run)
		}
	})
}

// Capture the real label catalogs, not profile-derived expected rows. Failure
// checks compare every association in both shared and current-local roots.
func sepgsqlRestoreconLabels(t *testing.T, p *fixture.Pair, c *sql.Conn) [][]string {
	t.Helper()
	var labels [][]string
	for _, query := range []string{
		"SELECT classoid::text,objoid::text,objsubid::text,provider,label FROM pg_seclabel ORDER BY classoid,objoid,objsubid,provider",
		"SELECT classoid::text,objoid::text,'0'::text,provider,label FROM pg_shseclabel ORDER BY classoid,objoid,provider",
	} {
		rows, err := c.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			row := make([]string, 5)
			if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4]); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			labels = append(labels, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return labels
}
