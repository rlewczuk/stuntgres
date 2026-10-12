package reference_test

import (
	"bytes"
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
		// ACL-free: controls intentionally do not expand ACL serialization.
		s.exec("CREATE SCHEMA own_control AUTHORIZATION own_source")
		// Namespace ACL SQL is a real PostgreSQL supplemental provider, not
		// native admission. Its native serializer guard is a descriptor witness.
		const namespaceGrant = "GRANT USAGE ON SCHEMA own_control TO own_reader"
		if s.backend.PG {
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_control','USAGE')", false)
			s.exec(namespaceGrant)
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_control','USAGE')", true)
			s.exec("REVOKE USAGE ON SCHEMA own_control FROM own_reader")
			ownershipBool(s, "SELECT has_schema_privilege('own_reader','own_control','USAGE')", false)
		} else {
			var before, after bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &before); err != nil {
				s.t.Fatal(err)
			}
			predefinedFailure(s, namespaceGrant, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "own_control"`, Position: "23"}, false, "prepare")
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				s.t.Fatal("rejected schema GRANT changed snapshot", err)
			}
		}
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
		for _, text := range []string{`CREATE SCHEMA "own_control" AUTHORIZATION "own_target";`, `SET SESSION AUTHORIZATION 'own_target';`, `CREATE INDEX "control_idx"`} {
			if !strings.Contains(dump.String(), text) {
				s.t.Fatalf("dump missing %q: %s", text, dump.String())
			}
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
		s.exec("CREATE INDEX own_smoke_idx ON own_smoke_t(id)")
		s.exec("CREATE SEQUENCE own_smoke_seq")
		s.exec("GRANT SELECT ON own_smoke_t TO own_reader")
		s.actor("")
		s.exec("GRANT own_target TO own_source WITH INHERIT FALSE, SET TRUE")
		s.actor("own_source")
		s.exec("ALTER TABLE own_smoke_t OWNER TO own_target")
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
