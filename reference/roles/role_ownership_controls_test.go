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

type ownershipWriter struct{ calls int }

func (w *ownershipWriter) Write(p []byte) (int, error) { w.calls++; return len(p), nil }

func TestReferenceRoleOwnershipControls(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		// Exercise schema grant provenance and replay in a bounded setup schema,
		// then remove it before the independent ownership snapshot below.
		var before bytes.Buffer
		if !s.backend.PG {
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &before); err != nil {
				s.t.Fatal(err)
			}
		}
		s.exec("CREATE SCHEMA own_schema AUTHORIZATION own_source")
		s.actor("own_source")
		s.exec("CREATE TYPE own_schema.e AS ENUM('a')")
		s.actor("")
		ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','USAGE')", false)
		ownershipBool(s, "SELECT nspacl IS NULL FROM pg_namespace WHERE nspname='own_schema'", true)
		s.exec("GRANT USAGE ON SCHEMA own_schema TO own_reader")
		checkSchemaACL := func() {
			acl := fixture.Select("SELECT unnest(nspacl)::text AS acl FROM pg_namespace WHERE nspname='own_schema'", []string{"acl"}, []string{"TEXT"}, [][]any{{"own_source=UC/own_source"}, {"own_reader=U/own_source"}})
			acl.Bag = true
			s.run(acl)
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','USAGE')", true)
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','CREATE')", false)
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','USAGE WITH GRANT OPTION')", false)
		}
		checkSchemaACL()
		if !s.backend.PG {
			var granted bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &granted); err != nil {
				s.t.Fatal(err)
			}
			const repair = "UPDATE pg_catalog.pg_namespace SET nspacl=E'{own_source=UC/own_source,own_reader=U/own_source}'::pg_catalog.aclitem[] WHERE nspname=E'own_schema'::pg_catalog.name;"
			if !strings.Contains(granted.String(), repair) {
				s.t.Fatal("dump omitted exact schema ACL repair", granted.String())
			}
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(granted.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			checkSchemaACL()
		}
		s.actor("own_reader")
		ownershipSelect(s, "SELECT 'a'::own_schema.e::TEXT AS x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}})
		s.actor("")
		s.exec("REVOKE USAGE ON SCHEMA own_schema FROM own_reader")
		ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_schema','USAGE')", false)
		ownershipSelect(s, "SELECT nspacl::text AS acl FROM pg_namespace WHERE nspname='own_schema'", []string{"acl"}, []string{"TEXT"}, [][]any{{"{own_source=UC/own_source}"}})
		s.actor("own_reader")
		denied := fixture.ImplicitError("SELECT 'a'::own_schema.e::TEXT AS x", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_schema", Position: "13"})
		denied.ErrorStage = "execute"
		if s.prepared {
			denied.ErrorStage = "prepare"
		}
		s.run(denied)
		s.actor("")
		s.exec("DROP SCHEMA own_schema CASCADE")
		if !s.backend.PG {
			var after bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				s.t.Fatal("bounded schema grant setup changed logical snapshot", err)
			}
		}
		// Keep the relation/column ACL rejection boundary independent below.
		s.exec("CREATE SCHEMA own_control AUTHORIZATION own_source")
		s.actor("own_source")
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
		s.run(fixture.Exec("INSERT INTO own_control.t(payload) VALUES(7)", 1))
		s.run(fixture.Exec("INSERT INTO own_control.p VALUES(7)", 1))
		ownershipSelect(s, "SELECT setval('own_control.s',4) AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}})
		s.actor("")
		s.exec("ALTER TABLE own_control.t OWNER TO own_target")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		s.exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET TRUE")
		s.exec("GRANT " + s.bootstrap + " TO own_target WITH INHERIT FALSE, SET TRUE")
		ownershipInherited(s, "own_target", s.bootstrap, "USAGE", false)
		check := func() {
			for _, name := range []string{"own_control.t", "own_control.control_idx", "own_control.t_id_seq", "own_control.s", "own_control.v", "own_control.mv", "own_control.p", "own_control.leaf", "own_control.c"} {
				ownershipOwner(s, name, "own_target")
			}
			ownershipSelect(s, "SELECT id,payload FROM own_control.t", []string{"id", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}})
			ownershipSelect(s, "SELECT payload FROM own_control.v", []string{"payload"}, []string{"INT4"}, [][]any{{int64(7)}})
			ownershipRows(s, "own_control.mv")
			ownershipRows(s, "own_control.p")
			s.actor("own_target")
			for _, typ := range []struct{ kind, name string }{{"TYPE", "e"}, {"DOMAIN", "d"}} {
				s.exec("ALTER " + typ.kind + " own_control." + typ.name + " OWNER TO " + s.bootstrap)
				s.actor("")
				s.exec("ALTER " + typ.kind + " own_control." + typ.name + " OWNER TO own_target")
				s.actor("own_target")
			}
			s.actor("")
		}
		check()
		// PG-only supplemental importer supplies the independently expected
		// ownership snapshot to the real native Dump API, then replays its actual
		// emitted SQL. It does not substitute for PG's shared ALTER/REASSIGN above.
		if s.backend.PG {
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
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(expectedSnapshot)); err != nil {
				s.t.Fatal("expected ownership snapshot import", err)
			}
			if _, err := s.p.Native.ExecContext(s.p.Ctx, "REFRESH MATERIALIZED VIEW own_control.mv"); err != nil {
				s.t.Fatal(err)
			}
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
			s.t.Fatal(err)
		}
		if s.backend.PG {
			s.exec("DROP SCHEMA own_control CASCADE")
			if _, err := s.c.ExecContext(s.p.Ctx, dump.String()); err != nil {
				s.t.Fatal("pinned replay of emitted ownership SQL", err)
			}
			s.actor("")
			// PG does not interpret native MV snapshot comments: its emitted
			// definition is unpopulated until a real REFRESH rebuilds row 7.
			step := fixture.MaterializedViewUnpopulated("own_control.mv")
			step.Diagnostic.Message = `materialized view "mv" has not been populated`
			s.run(step)
			s.exec("REFRESH MATERIALIZED VIEW own_control.mv")
		} else {
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
				s.t.Fatal(err)
			}
			ownershipInherited(s, "own_actor", "own_target", "MEMBER", true)
			ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='t'", 0)
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
		}
		check()
		ownershipSelect(s, "SELECT nextval('own_control.s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}})
		if !s.backend.PG {
			var before bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &before); err != nil {
				s.t.Fatal(err)
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
				err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(tc.script))
				if tc.diagnostic.Code == "0A000" {
					// The admitted SQLSTATE and atomic refusal are the contract,
					// not an incidental enumeration of Restore's evolving whitelist.
					fixture.State(s.t, err, "0A000")
				} else {
					fixture.AssertDiagnostic(s.t, err, &tc.diagnostic)
				}
				check()
				var after bytes.Buffer
				if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					s.t.Fatal("failed restore changed ownership/data/sequence snapshot", err)
				}
			}
			for _, tc := range []struct{ grant, message string }{
				{"GRANT SELECT ON own_control.t TO own_reader", "stuntgres: dump cannot serialize explicit relation or column ACL"},
				{"GRANT SELECT(payload) ON own_control.t TO own_reader", "stuntgres: dump cannot serialize explicit relation or column ACL"},
			} {
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					s.t.Fatal(err)
				}
				s.exec(tc.grant)
				w := &ownershipWriter{}
				err := stuntgres.Dump(s.p.Ctx, s.p.Native, w)
				if err == nil || err.Error() != tc.message || w.calls != 0 {
					s.t.Fatal("explicit ACL dump before writer I/O", err, w.calls)
				}
				var structured interface{ SQLState() string }
				if errors.As(err, &structured) {
					s.t.Fatal("control ACL error must be ordinary Go error", err)
				}
			}
		}
	})
}

func TestReferenceRoleOwnershipFeatureSmoke(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		ownershipSeed(s, "own_smoke_t", "own_source")
		s.actor("own_source")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_source", func() {
			s.exec("CREATE INDEX own_smoke_idx ON own_smoke_t(id)")
			s.exec("CREATE SEQUENCE own_smoke_seq")
		})
		s.exec("GRANT SELECT ON own_smoke_t TO own_reader")
		s.actor("")
		s.exec("GRANT own_target TO own_source WITH INHERIT FALSE, SET TRUE")
		s.actor("own_source")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_target", func() { s.exec("ALTER TABLE own_smoke_t OWNER TO own_target") })
		s.actor("")
		ownershipOwner(s, "own_smoke_t", "own_target")
		ownershipOwner(s, "own_smoke_idx", "own_target")
		s.actor("own_reader")
		ownershipRows(s, "own_smoke_t")
		s.actor("")
		ownershipSeed(s, "own_smoke_u", "own_source")
		s.exec("CREATE VIEW own_smoke_dep AS SELECT id FROM own_smoke_u")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipOwner(s, "own_smoke_u", "own_target")
		ownershipOwner(s, "own_smoke_seq", "own_target")
		ownershipRoleDependency(s, "own_reader", "privileges for table own_smoke_t")
		ownershipFailure(s, "DROP OWNED BY own_target RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "view own_smoke_dep depends on table own_smoke_u", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		ownershipRows(s, "own_smoke_u")
		s.clearNotices()
		s.exec("DROP OWNED BY own_target CASCADE")
		ownershipNotices(s, ownershipCascadeNotice("drop cascades to view own_smoke_dep", ""))
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_smoke_t' OR relname='own_smoke_idx' OR relname='own_smoke_u' OR relname='own_smoke_seq' OR relname='own_smoke_dep'", 0)
		s.exec("DROP OWNED BY own_reader")
		s.exec("DROP ROLE own_reader")
		s.exec("DROP ROLE own_source")
		s.exec("DROP ROLE own_target")
	})
}

// Migrated public behavior from the former internal role controls SQL test.
// Dump spelling is not a contract: ownership, data, retained handles, session
// identity and atomic failure are checked through their public consumers.
func TestReferenceRoleControlsOwnersAndCancellation(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		s.actor("own_source")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_source", func() { ownershipDefinition(s, "CREATE TABLE role_control_secret(id INT)") })
		s.run(fixture.Exec("INSERT INTO role_control_secret VALUES(7)", 1))
		s.actor("")
		ownershipOwner(s, "role_control_secret", "own_source")
		ownershipRoleDependency(s, "own_source", "owner of table role_control_secret")
		ownershipSelect(s, "SELECT id FROM role_control_secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		if !s.backend.PG {
			old, err := s.c.QueryContext(s.p.Ctx, "SELECT id FROM role_control_secret")
			if err != nil {
				s.t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
				s.t.Fatal(err)
			}
			ownershipSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.bootstrap}})
			ownershipSelect(s, "SELECT rolname FROM pg_roles WHERE rolname='own_source'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_source"}})
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			ownershipOwner(s, "role_control_secret", "own_source")
			ownershipRoleDependency(s, "own_source", "owner of table role_control_secret")
			fixture.AssertDiagnostic(s.t, stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader("SET SESSION AUTHORIZATION 'own_absent'; CREATE TABLE bad(id INT);")), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `role "own_absent" does not exist`})
			ownershipSelect(s, "SELECT id FROM role_control_secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			var after bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				s.t.Fatal("failed owner Restore changed live definitions or rows", err)
			}
			if !old.Next() {
				s.t.Fatal("replacement lost retained owner cursor")
			}
			var id int64
			if err := old.Scan(&id); err != nil || id != 7 {
				s.t.Fatalf("retained owner row %d: %v", id, err)
			}
			if old.Next() || old.Err() != nil {
				s.t.Fatal("retained owner cursor changed", old.Err())
			}
			ctx, cancel := context.WithCancel(s.p.Ctx)
			cancel()
			if err := stuntgres.Reset(ctx, s.p.Native); !errors.Is(err, context.Canceled) {
				s.t.Fatal("precanceled Reset", err)
			}
			after.Reset()
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				s.t.Fatal("canceled Reset changed live catalog", err)
			}
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader("CREATE TABLE role_control_legacy(id INT); INSERT INTO role_control_legacy VALUES(8);")); err != nil {
				s.t.Fatal(err)
			}
		} else {
			s.exec("DROP TABLE role_control_secret")
			ownershipDefinition(s, "CREATE TABLE role_control_legacy(id INT)")
			s.run(fixture.Exec("INSERT INTO role_control_legacy VALUES(8)", 1))
		}
		ownershipOwner(s, "role_control_legacy", s.bootstrap)
		ownershipSelect(s, "SELECT id FROM role_control_legacy", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}})
		ownershipSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{s.bootstrap}})
	})
}
