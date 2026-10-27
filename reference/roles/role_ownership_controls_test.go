package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipControls(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		// Exercise schema grant provenance and replay in a bounded setup schema,
		// then remove it before the independent ownership snapshot below.
		var before bytes.Buffer
		if !s.Backend.PG {
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &before); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Exec("CREATE SCHEMA own_schema AUTHORIZATION own_source")
		s.Actor("own_source")
		s.Exec("CREATE TYPE own_schema.e AS ENUM('a')")
		s.Actor("")
		ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','USAGE')", false)
		ownershipBool(s, "SELECT nspacl IS NULL FROM pg_namespace WHERE nspname='own_schema'", true)
		s.Exec("GRANT USAGE ON SCHEMA own_schema TO own_reader")
		checkSchemaACL := func() {
			acl := fixture.Select("SELECT unnest(nspacl)::text AS acl FROM pg_namespace WHERE nspname='own_schema'", []string{"acl"}, []string{"TEXT"}, [][]any{{"own_source=UC/own_source"}, {"own_reader=U/own_source"}})
			acl.Bag = true
			s.Run(acl)
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','USAGE')", true)
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','CREATE')", false)
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','USAGE WITH GRANT OPTION')", false)
		}
		checkSchemaACL()
		if !s.Backend.PG {
			var granted bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &granted); err != nil {
				s.T.Fatal(err)
			}
			const repair = "UPDATE pg_catalog.pg_namespace SET nspacl=E'{own_source=UC/own_source,own_reader=U/own_source}'::pg_catalog.aclitem[] WHERE nspname=E'own_schema'::pg_catalog.name;"
			if !strings.Contains(granted.String(), repair) {
				s.T.Fatal("dump omitted exact schema ACL repair", granted.String())
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(granted.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			checkSchemaACL()
		}
		s.Actor("own_reader")
		ownershipSelect(s, "SELECT 'a'::own_schema.e::TEXT AS x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}})
		s.Actor("")
		s.Exec("REVOKE USAGE ON SCHEMA own_schema FROM own_reader")
		ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','USAGE')", false)
		ownershipSelect(s, "SELECT nspacl::text AS acl FROM pg_namespace WHERE nspname='own_schema'", []string{"acl"}, []string{"TEXT"}, [][]any{{"{own_source=UC/own_source}"}})
		s.Actor("own_reader")
		denied := fixture.ImplicitError("SELECT 'a'::own_schema.e::TEXT AS x", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_schema", Position: "13"})
		denied.ErrorStage = "execute"
		if s.Prepared {
			denied.ErrorStage = "prepare"
		}
		s.Run(denied)
		s.Actor("")
		s.Exec("DROP SCHEMA own_schema CASCADE")
		if !s.Backend.PG {
			var after bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				s.T.Fatal("bounded schema grant setup changed logical snapshot", err)
			}
		}
		// Independently roundtrip ownership, data and relation/column ACLs below.
		s.Exec("CREATE SCHEMA own_control AUTHORIZATION own_source")
		s.Actor("own_source")
		for _, q := range []string{
			"CREATE TABLE own_control.t(id SERIAL,payload INT DEFAULT 9)",
			"CREATE INDEX control_idx ON own_control.t(payload)",
			"CREATE VIEW own_control.v AS SELECT payload FROM own_control.t",
			"CREATE MATERIALIZED VIEW own_control.mv AS SELECT 7::INT AS id",
			"CREATE SEQUENCE own_control.s",
			"CREATE TYPE own_control.e AS ENUM('a')",
			"CREATE DOMAIN own_control.d AS INT",
			"CREATE TYPE own_control.c AS(id INT)",
			"CREATE COLLATION own_control.coll FROM \"C\"",
			"CREATE TABLE own_control.p(id INT) PARTITION BY RANGE(id)",
			"CREATE TABLE own_control.leaf PARTITION OF own_control.p FOR VALUES FROM(0) TO(10)",
		} {
			ownershipDefinition(s, q)
		}
		s.Run(fixture.Exec("INSERT INTO own_control.t(payload) VALUES(7)", 1))
		s.Run(fixture.Exec("INSERT INTO own_control.p VALUES(7)", 1))
		ownershipSelect(s, "SELECT setval('own_control.s',4) AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}})
		s.Actor("")
		s.Exec("ALTER TABLE own_control.t OWNER TO own_target")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		s.Exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET TRUE")
		s.Exec("GRANT " + s.Bootstrap + " TO own_target WITH INHERIT FALSE, SET TRUE")
		ownershipInherited(s, "own_target", s.Bootstrap, "USAGE", false)
		check := func() {
			for _, name := range []string{"own_control.t", "own_control.control_idx", "own_control.t_id_seq", "own_control.s", "own_control.v", "own_control.mv", "own_control.p", "own_control.leaf", "own_control.c"} {
				ownershipOwner(s, name, "own_target")
			}
			ownershipSelect(s, "SELECT id,payload FROM own_control.t", []string{"id", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}})
			ownershipSelect(s, "SELECT payload FROM own_control.v", []string{"payload"}, []string{"INT4"}, [][]any{{int64(7)}})
			ownershipRows(s, "own_control.mv")
			ownershipRows(s, "own_control.p")
			s.Actor("own_target")
			for _, typ := range []struct{ kind, name string }{{"TYPE", "e"}, {"DOMAIN", "d"}} {
				s.Exec("ALTER " + typ.kind + " own_control." + typ.name + " OWNER TO " + s.Bootstrap)
				s.Actor("")
				s.Exec("ALTER " + typ.kind + " own_control." + typ.name + " OWNER TO own_target")
				s.Actor("own_target")
			}
			s.Actor("")
		}
		check()
		// PG-only supplemental importer supplies the independently expected
		// ownership snapshot to the real native Dump API, then replays its actual
		// emitted SQL. It does not substitute for PG's shared ALTER/REASSIGN above.
		if s.Backend.PG {
			const expectedSnapshot = `CREATE SCHEMA own_control AUTHORIZATION own_target;
SET SESSION AUTHORIZATION 'own_target';
CREATE TABLE own_control.t(id SERIAL,payload INT DEFAULT 9);
CREATE INDEX control_idx ON own_control.t(payload);
INSERT INTO own_control.t(payload) VALUES(7);
CREATE VIEW own_control.v AS SELECT payload FROM own_control.t;
CREATE MATERIALIZED VIEW own_control.mv AS SELECT 7::INT AS id WITH NO DATA;
CREATE SEQUENCE own_control.s;
SELECT setval('own_control.s',4,true);
CREATE TYPE own_control.e AS ENUM('a');
CREATE DOMAIN own_control.d AS INT;
CREATE TYPE own_control.c AS(id INT);
CREATE COLLATION own_control.coll FROM "C";
CREATE TABLE own_control.p(id INT) PARTITION BY RANGE(id);
CREATE TABLE own_control.leaf PARTITION OF own_control.p FOR VALUES FROM(0) TO(10);
INSERT INTO own_control.p VALUES(7);`
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(expectedSnapshot)); err != nil {
				s.T.Fatal("expected ownership snapshot import", err)
			}
			if _, err := s.P.Native.ExecContext(s.P.Ctx, "REFRESH MATERIALIZED VIEW own_control.mv"); err != nil {
				s.T.Fatal(err)
			}
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
			s.T.Fatal(err)
		}
		if s.Backend.PG {
			s.Exec("DROP SCHEMA own_control CASCADE")
			if _, err := s.C.ExecContext(s.P.Ctx, dump.String()); err != nil {
				s.T.Fatal("pinned replay of emitted ownership SQL", err)
			}
			s.Actor("")
			// PG does not interpret native MV snapshot comments: its emitted
			// definition is unpopulated until a real REFRESH rebuilds row 7.
			step := fixture.MaterializedViewUnpopulated("own_control.mv")
			step.Diagnostic.Message = `materialized view "mv" has not been populated`
			s.Run(step)
			s.Exec("REFRESH MATERIALIZED VIEW own_control.mv")
		} else {
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			ownershipInherited(s, "own_actor", "own_target", "MEMBER", true)
			ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='t'", 0)
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
		}
		check()
		ownershipSelect(s, "SELECT nextval('own_control.s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}})
		if !s.Backend.PG {
			var before bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &before); err != nil {
				s.T.Fatal(err)
			}
			lateScript := "CREATE TABLE replacement(id INT); INSERT INTO absent VALUES(1);"
			for _, tc := range []struct {
				script     string
				diagnostic fixture.Diagnostic
			}{
				{"SET SESSION AUTHORIZATION 'own_absent'; CREATE TABLE bad(id INT);", fixture.ControlsReferenceDiagnostic("22023", `role "own_absent" does not exist`)},
				{lateScript, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "absent" does not exist`, Position: strconv.Itoa(strings.Index(lateScript, "absent") + 1)}},
				{"REASSIGN OWNED BY own_target TO own_source;", fixture.Diagnostic{Code: "0A000"}},
				{"DROP OWNED BY own_target CASCADE;", fixture.Diagnostic{Code: "0A000"}},
			} {
				err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(tc.script))
				if tc.diagnostic.Code == "0A000" {
					// The admitted SQLSTATE and atomic refusal are the contract,
					// not an incidental enumeration of Restore's evolving whitelist.
					fixture.State(s.T, err, "0A000")
				} else {
					fixture.AssertDiagnostic(s.T, err, &tc.diagnostic)
				}
				check()
				var after bytes.Buffer
				if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					s.T.Fatal("failed restore changed ownership/data/sequence snapshot", err)
				}
			}
			for _, column := range []bool{false, true} {
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
					s.T.Fatal(err)
				}
				s.Exec("GRANT USAGE ON SCHEMA own_control TO own_reader")
				grant := "GRANT SELECT ON own_control.t TO own_reader"
				if column {
					grant = "GRANT SELECT(payload) ON own_control.t TO own_reader"
				}
				s.Exec(grant)
				checkACL := func() {
					ownershipBool(s, "SELECT has_table_privilege('own_reader','own_control.t','SELECT')", !column)
					ownershipBool(s, "SELECT has_column_privilege('own_reader','own_control.t','payload','SELECT')", true)
					ownershipBool(s, "SELECT has_column_privilege('own_reader','own_control.t','id','SELECT')", !column)
					relationACL, columnACL := any("{own_target=arwdDxtm/own_target,own_reader=r/own_target}"), any(nil)
					if column {
						relationACL, columnACL = nil, "{own_reader=r/own_target}"
					}
					ownershipSelect(s, "SELECT relacl::text AS acl FROM pg_class WHERE oid='own_control.t'::regclass", []string{"acl"}, []string{"TEXT"}, [][]any{{relationACL}})
					ownershipSelect(s, "SELECT attacl::text AS acl FROM pg_attribute WHERE attrelid='own_control.t'::regclass AND attname='payload'", []string{"acl"}, []string{"TEXT"}, [][]any{{columnACL}})
					s.Actor("own_reader")
					ownershipSelect(s, "SELECT payload FROM own_control.t", []string{"payload"}, []string{"INT4"}, [][]any{{int64(7)}})
					if column {
						fixture.RoleGrantPermissionDenied(s.Run, "SELECT id FROM own_control.t", "table", "t")
					} else {
						ownershipSelect(s, "SELECT id FROM own_control.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
					}
					s.Actor("")
				}
				checkACL()
				var granted bytes.Buffer
				if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &granted); err != nil {
					s.T.Fatal("explicit ACL Dump", err)
				}
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(granted.Bytes())); err != nil {
					s.T.Fatal("explicit ACL Restore", err)
				}
				check()
				checkACL()
			}
		}
	})
}

func TestReferenceRoleOwnershipFeatureSmoke(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		ownershipSeed(s, "own_smoke_t", "own_source")
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() {
			s.Exec("CREATE INDEX own_smoke_idx ON own_smoke_t(id)")
			s.Exec("CREATE SEQUENCE own_smoke_seq")
		})
		s.Exec("GRANT SELECT ON own_smoke_t TO own_reader")
		s.Actor("")
		s.Exec("GRANT own_target TO own_source WITH INHERIT FALSE, SET TRUE")
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_target", func() { s.Exec("ALTER TABLE own_smoke_t OWNER TO own_target") })
		s.Actor("")
		ownershipOwner(s, "own_smoke_t", "own_target")
		ownershipOwner(s, "own_smoke_idx", "own_target")
		s.Actor("own_reader")
		ownershipRows(s, "own_smoke_t")
		s.Actor("")
		ownershipSeed(s, "own_smoke_u", "own_source")
		s.Exec("CREATE VIEW own_smoke_dep AS SELECT id FROM own_smoke_u")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipOwner(s, "own_smoke_u", "own_target")
		ownershipOwner(s, "own_smoke_seq", "own_target")
		ownershipRoleDependency(s, "own_reader", "privileges for table own_smoke_t")
		ownershipFailure(s, "DROP OWNED BY own_target RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "view own_smoke_dep depends on table own_smoke_u", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		ownershipRows(s, "own_smoke_u")
		s.ClearNotices()
		s.Exec("DROP OWNED BY own_target CASCADE")
		ownershipNotices(s, ownershipCascadeNotice("drop cascades to view own_smoke_dep", ""))
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_smoke_t' OR relname='own_smoke_idx' OR relname='own_smoke_u' OR relname='own_smoke_seq' OR relname='own_smoke_dep'", 0)
		s.Exec("DROP OWNED BY own_reader")
		s.Exec("DROP ROLE own_reader")
		s.Exec("DROP ROLE own_source")
		s.Exec("DROP ROLE own_target")
	})
}

// Migrated public behavior from the former internal role controls SQL test.
// Dump spelling is not a contract: ownership, data, retained handles, session
// identity and atomic failure are checked through their public consumers.
func TestReferenceRoleControlsOwnersAndCancellation(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() { ownershipDefinition(s, "CREATE TABLE role_control_secret(id INT)") })
		s.Run(fixture.Exec("INSERT INTO role_control_secret VALUES(7)", 1))
		s.Actor("")
		ownershipOwner(s, "role_control_secret", "own_source")
		ownershipRoleDependency(s, "own_source", "owner of table role_control_secret")
		ownershipSelect(s, "SELECT id FROM role_control_secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		if !s.Backend.PG {
			old, err := s.C.QueryContext(s.P.Ctx, "SELECT id FROM role_control_secret")
			if err != nil {
				s.T.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			ownershipSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.Bootstrap}})
			ownershipSelect(s, "SELECT rolname FROM pg_roles WHERE rolname='own_source'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_source"}})
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			ownershipOwner(s, "role_control_secret", "own_source")
			ownershipRoleDependency(s, "own_source", "owner of table role_control_secret")
			fixture.AssertDiagnostic(s.T, stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader("SET SESSION AUTHORIZATION 'own_absent'; CREATE TABLE bad(id INT);")), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `role "own_absent" does not exist`})
			ownershipSelect(s, "SELECT id FROM role_control_secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			var after bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				s.T.Fatal("failed owner Restore changed live definitions or rows", err)
			}
			if !old.Next() {
				s.T.Fatal("replacement lost retained owner cursor")
			}
			var id int64
			if err := old.Scan(&id); err != nil || id != 7 {
				s.T.Fatalf("retained owner row %d: %v", id, err)
			}
			if old.Next() || old.Err() != nil {
				s.T.Fatal("retained owner cursor changed", old.Err())
			}
			ctx, cancel := context.WithCancel(s.P.Ctx)
			cancel()
			if err := stuntgres.Reset(ctx, s.P.Native); !errors.Is(err, context.Canceled) {
				s.T.Fatal("precanceled Reset", err)
			}
			after.Reset()
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				s.T.Fatal("canceled Reset changed live catalog", err)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader("CREATE TABLE role_control_legacy(id INT); INSERT INTO role_control_legacy VALUES(8);")); err != nil {
				s.T.Fatal(err)
			}
		} else {
			s.Exec("DROP TABLE role_control_secret")
			ownershipDefinition(s, "CREATE TABLE role_control_legacy(id INT)")
			s.Run(fixture.Exec("INSERT INTO role_control_legacy VALUES(8)", 1))
		}
		ownershipOwner(s, "role_control_legacy", s.Bootstrap)
		ownershipSelect(s, "SELECT id FROM role_control_legacy", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}})
		ownershipSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.Bootstrap}})
	})
}
