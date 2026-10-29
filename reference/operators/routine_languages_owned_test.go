package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesReassignOwned(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rl_bulk_owner", "rl_bulk_successor", "rl_bulk_reader"})
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_bulk CASCADE")()
				userOperatorExec(t, p, b, c, "CREATE ROLE rl_bulk_owner", "CREATE ROLE rl_bulk_successor", "CREATE ROLE rl_bulk_reader",
					"CREATE TRUSTED LANGUAGE rl_bulk"+routineLanguageFull,
					"REVOKE USAGE ON LANGUAGE rl_bulk FROM PUBLIC",
					"GRANT USAGE ON LANGUAGE rl_bulk TO rl_bulk_reader WITH GRANT OPTION",
					"ALTER LANGUAGE rl_bulk OWNER TO rl_bulk_owner",
					"CREATE FUNCTION rl_bulk_fn(x integer) RETURNS integer LANGUAGE rl_bulk AS $$BEGIN RETURN x+1; END$$")
				oid := routineLanguagesOID(t, p, c, "rl_bulk")
				held, closeHeld := routineLanguagesHeld(t, p, b, c, "rl_bulk_fn")
				defer closeHeld()
				check := func(owner string) {
					plpgsqlValue(t, p, b, c, "SELECT lanowner=(SELECT oid FROM pg_roles WHERE rolname='"+owner+"') AND lanacl IS NOT NULL AS v FROM pg_language WHERE lanname='rl_bulk'", "BOOL", true)
					acl := fixture.Select("SELECT unnest(lanacl)::text AS acl FROM pg_language WHERE lanname='rl_bulk'", []string{"acl"}, []string{"TEXT"}, [][]any{{owner + "=U/" + owner}, {"rl_bulk_reader=U*/" + owner}})
					acl.Bag = true
					fixture.RunStep(t, p.Ctx, c, b.Recorder, acl)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(acl))
					plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('public','rl_bulk','USAGE') AS v", "BOOL", false)
					plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('rl_bulk_reader','rl_bulk','USAGE WITH GRANT OPTION') AS v", "BOOL", true)
					plpgsqlValue(t, p, b, c, "SELECT p.prolang=l.oid AND l.lanplcallfoid=h.oid AND l.laninline=i.oid AND l.lanvalidator=v.oid AS v FROM pg_proc p JOIN pg_language l ON l.lanname='rl_bulk' JOIN pg_proc h ON h.proname='plpgsql_call_handler' AND h.pronamespace='pg_catalog'::regnamespace JOIN pg_proc i ON i.proname='plpgsql_inline_handler' AND i.pronamespace='pg_catalog'::regnamespace JOIN pg_proc v ON v.proname='plpgsql_validator' AND v.pronamespace='pg_catalog'::regnamespace WHERE p.proname='rl_bulk_fn'", "BOOL", true)
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_proc p ON d.classid='pg_proc'::regclass AND d.objid=p.oid JOIN pg_language l ON d.refclassid='pg_language'::regclass AND d.refobjid=l.oid WHERE p.proname='rl_bulk_fn' AND l.lanname='rl_bulk' AND d.deptype='n'", "INT8", int64(1))
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid WHERE l.lanname='rl_bulk' AND d.refclassid='pg_proc'::regclass AND d.deptype='n'", "INT8", int64(3))
					parametersValue(t, p, b, c, "SELECT rl_bulk_fn($1::integer) AS v", "v", "INT4", int64(5), 4)
					held(9, 10)
					if got := routineLanguagesOID(t, p, c, "rl_bulk"); got != oid {
						t.Fatalf("ownership changed language identity: %d -> %d", oid, got)
					}
				}
				check("rl_bulk_owner")
				s := fixture.Exec("REASSIGN OWNED BY rl_bulk_owner TO rl_bulk_successor", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				check("rl_bulk_successor")
				userOperatorExec(t, p, b, c, "DROP ROLE rl_bulk_owner")
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_roles WHERE rolname='rl_bulk_owner'", "INT8", int64(0))
				check("rl_bulk_successor")
			})
		})
	}
}

func TestReferenceRoutineLanguagesDropOwned(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_owned_owner"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = recorder
		defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_owned CASCADE")()
		userOperatorExec(t, p, b, c, "CREATE ROLE rl_owned_owner", "CREATE TRUSTED LANGUAGE rl_owned"+routineLanguageFull,
			"ALTER LANGUAGE rl_owned OWNER TO rl_owned_owner",
			"CREATE FUNCTION rl_owned_fn(x integer) RETURNS integer LANGUAGE rl_owned AS $$BEGIN RETURN x+1; END$$",
			"CREATE VIEW rl_owned_view AS SELECT rl_owned_fn(4) AS v",
			"CREATE VIEW rl_owned_chain AS SELECT v FROM rl_owned_view",
			"CREATE TABLE rl_owned_data(id integer,v integer DEFAULT rl_owned_fn(4),CONSTRAINT rl_owned_check CHECK(rl_owned_fn(v)>0))",
			"CREATE TABLE rl_owned_keep(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_owned_data(id) VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_owned_keep VALUES(7)", 1))
		held, closeHeld := routineLanguagesHeld(t, p, b, c, "rl_owned_fn")
		defer closeHeld()
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop language rl_owned because other objects depend on it")
		d.Detail = "function rl_owned_fn(integer) depends on language rl_owned\nview rl_owned_view depends on function rl_owned_fn(integer)\nview rl_owned_chain depends on view rl_owned_view\ndefault value for column v of table rl_owned_data depends on function rl_owned_fn(integer)\nconstraint rl_owned_check on table rl_owned_data depends on function rl_owned_fn(integer)"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		routineLanguagesError(t, p, b, c, "DROP OWNED BY rl_owned_owner RESTRICT", d, false, "execute")
		routineLanguagesCount(t, p, b, c, "rl_owned", 1)
		plpgsqlValue(t, p, b, c, "SELECT lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_owned_owner') AS v FROM pg_language WHERE lanname='rl_owned'", "BOOL", true)
		held(9, 10)
		plpgsqlValue(t, p, b, c, "SELECT v FROM rl_owned_chain", "INT4", int64(5))
		plpgsqlValue(t, p, b, c, "SELECT v FROM rl_owned_data WHERE id=1", "INT4", int64(5))
		if len(notices) != 0 {
			t.Fatalf("failed retirement emitted notices: %#v", notices)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("DROP OWNED BY rl_owned_owner CASCADE", 0)))
		wantNotices := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 5 other objects", Detail: "drop cascades to function rl_owned_fn(integer)\ndrop cascades to view rl_owned_view\ndrop cascades to view rl_owned_chain\ndrop cascades to default value for column v of table rl_owned_data\ndrop cascades to constraint rl_owned_check on table rl_owned_data"}}
		if !reflect.DeepEqual(notices, wantNotices) {
			t.Fatalf("owned cascade notices: %#v want %#v", notices, wantNotices)
		}
		routineLanguagesCount(t, p, b, c, "rl_owned", 0)
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_owned_fn'", "INT8", int64(0))
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname IN ('rl_owned_view','rl_owned_chain')", "INT8", int64(0))
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_attrdef WHERE adrelid='rl_owned_data'::regclass", "INT8", int64(0))
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_constraint WHERE conrelid='rl_owned_data'::regclass", "INT8", int64(0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_owned_data(id) VALUES(2)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM rl_owned_data ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(5)}, {int64(2), nil}}))
		plpgsqlValue(t, p, b, c, "SELECT v FROM rl_owned_keep", "INT4", int64(7))
		// The same root rule must retire a language without any cascade.
		userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_owned"+routineLanguageCall,
			"ALTER LANGUAGE rl_owned OWNER TO rl_owned_owner", "DROP OWNED BY rl_owned_owner RESTRICT")
		routineLanguagesCount(t, p, b, c, "rl_owned", 0)
		userOperatorExec(t, p, b, c, "DROP ROLE rl_owned_owner")
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_roles WHERE rolname='rl_owned_owner'", "INT8", int64(0))
	})
}

func TestReferenceRoutineLanguagesOwnedProtection(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_protected_owner", "rl_protected_successor"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_protected_plain CASCADE", "DROP LANGUAGE IF EXISTS rl_protected CASCADE")()
		userOperatorExec(t, p, b, c, "CREATE ROLE rl_protected_owner", "CREATE ROLE rl_protected_successor")
		// Genuine installed provenance is not an intrinsic database-system pin.
		// ROLLBACK restores the entire shared stock definition even on failure.
		userOperatorExec(t, p, b, c, "BEGIN", "ALTER LANGUAGE plpgsql OWNER TO rl_protected_owner", "REASSIGN OWNED BY rl_protected_owner TO rl_protected_successor")
		plpgsqlValue(t, p, b, c, "SELECT lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_protected_successor') AS v FROM pg_language WHERE lanname='plpgsql'", "BOOL", true)
		userOperatorExec(t, p, b, c, "ROLLBACK")
		stockOwner := "postgres"
		if b.PG {
			stockOwner = "stuntgres"
		}
		plpgsqlValue(t, p, b, c, "SELECT l.lanowner=(SELECT oid FROM pg_roles WHERE rolname='"+stockOwner+"') AND l.lanacl IS NULL AND l.lanpltrusted AND l.lanplcallfoid=h.oid AND l.laninline=i.oid AND l.lanvalidator=v.oid AS v FROM pg_language l JOIN pg_proc h ON h.proname='plpgsql_call_handler' AND h.pronamespace='pg_catalog'::regnamespace JOIN pg_proc i ON i.proname='plpgsql_inline_handler' AND i.pronamespace='pg_catalog'::regnamespace JOIN pg_proc v ON v.proname='plpgsql_validator' AND v.pronamespace='pg_catalog'::regnamespace WHERE l.lanname='plpgsql'", "BOOL", true)
		userOperatorExec(t, p, b, c, "BEGIN", "CREATE TRUSTED LANGUAGE rl_protected_plain"+routineLanguageCall, "ROLLBACK")
		routineLanguagesCount(t, p, b, c, "rl_protected_plain", 0)
		userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_protected"+routineLanguageFull,
			"CREATE TRUSTED LANGUAGE rl_protected_plain"+routineLanguageCall,
			"ALTER LANGUAGE rl_protected OWNER TO rl_protected_owner", "ALTER LANGUAGE rl_protected_plain OWNER TO rl_protected_owner")
		plainOID := routineLanguagesOID(t, p, c, "rl_protected_plain")
		userOperatorExec(t, p, b, c, "BEGIN", "ALTER LANGUAGE rl_protected_plain RENAME TO rl_protected_tx",
			"DROP LANGUAGE rl_protected_tx", "ROLLBACK")
		if got := routineLanguagesOID(t, p, c, "rl_protected_plain"); got != plainOID {
			t.Fatalf("rolled-back rename/drop replaced language identity: %d -> %d", plainOID, got)
		}
		routineLanguagesCount(t, p, b, c, "rl_protected_tx", 0)
		// Register detachment only after ADD succeeds; outer cleanup rolls back
		// first if an assertion interrupts the installed transaction above.
		userOperatorExec(t, p, b, c, "ALTER EXTENSION plpgsql ADD LANGUAGE rl_protected")
		defer routineLanguagesCleanup(t, c, "ALTER EXTENSION plpgsql DROP LANGUAGE rl_protected")()
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop language rl_protected because extension plpgsql requires it")
		d.Hint = "You can drop extension plpgsql instead."
		for _, suffix := range []string{" RESTRICT", " CASCADE"} {
			routineLanguagesError(t, p, b, c, "DROP OWNED BY rl_protected_owner"+suffix, d, false, "execute")
			for _, language := range []string{"rl_protected", "rl_protected_plain"} {
				routineLanguagesCount(t, p, b, c, language, 1)
				plpgsqlValue(t, p, b, c, "SELECT lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_protected_owner') AS v FROM pg_language WHERE lanname='"+language+"'", "BOOL", true)
			}
		}
		pin := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop language sql because it is required by the database system")
		routineLanguagesError(t, p, b, c, "DROP LANGUAGE sql CASCADE", pin, false, "execute")
		plpgsqlValue(t, p, b, c, "SELECT oid=14 AS v FROM pg_language WHERE lanname='sql'", "BOOL", true)
		userOperatorExec(t, p, b, c, "REASSIGN OWNED BY rl_protected_owner TO rl_protected_successor")
		for _, language := range []string{"rl_protected", "rl_protected_plain"} {
			plpgsqlValue(t, p, b, c, "SELECT lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_protected_successor') AS v FROM pg_language WHERE lanname='"+language+"'", "BOOL", true)
		}
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE l.lanname='rl_protected' AND e.extname='plpgsql' AND d.deptype='e'", "INT8", int64(1))
		userOperatorExec(t, p, b, c, "DROP ROLE rl_protected_owner")
	})
}
