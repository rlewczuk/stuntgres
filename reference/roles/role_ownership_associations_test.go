package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipAssociations(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		s.actor("own_source")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_source", func() {
			for _, q := range []string{
				"CREATE TABLE own_parent(id SERIAL,identity_id INT GENERATED ALWAYS AS IDENTITY,payload INT DEFAULT 9)",
				"CREATE INDEX own_parent_index ON own_parent(payload)",
				"COMMENT ON TABLE own_parent IS 'ownership sentinel'",
				"CREATE POLICY own_parent_policy ON own_parent TO own_reader USING(payload>0)",
				"CREATE SEQUENCE own_nearby",
				"CREATE TABLE own_child(extra INT) INHERITS(own_parent)",
				"CREATE TYPE own_e AS ENUM('a','b')",
				"CREATE DOMAIN own_d AS INT",
				"CREATE TYPE own_c AS(id INT)",
			} {
				s.exec(q)
			}
		})
		s.run(fixture.Exec("INSERT INTO own_parent(payload) VALUES(7)", 1))
		s.exec("GRANT SELECT ON own_parent TO own_reader")
		s.exec("GRANT SELECT(payload) ON own_parent TO own_reader WITH GRANT OPTION")
		ownershipSelect(s, "SELECT currval('own_parent_id_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		// Capture opaque associated relation addresses once; transfer never remints.
		rows, err := s.c.QueryContext(s.p.Ctx, "SELECT oid,relname FROM pg_class WHERE relname='own_parent' OR relname='own_parent_index' OR relname='own_parent_id_seq' OR relname='own_parent_identity_id_seq' ORDER BY relname")
		if err != nil {
			s.t.Fatal(err)
		}
		ids := map[string]int64{}
		for rows.Next() {
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				s.t.Fatal(err)
			}
			if id <= 0 {
				s.t.Fatal("invalid identity", id)
			}
			ids[name] = id
		}
		if err := rows.Err(); err != nil {
			s.t.Fatal(err)
		}
		rows.Close()
		if len(ids) != 4 {
			s.t.Fatal("missing associated identities", ids)
		}
		s.exec("ALTER SEQUENCE own_parent_id_seq OWNER TO own_source")
		ownershipFailure(s, "ALTER SEQUENCE own_parent_id_seq OWNER TO own_target", fixture.Diagnostic{Code: "0A000", Message: `cannot change owner of sequence "own_parent_id_seq"`, Detail: `Sequence "own_parent_id_seq" is linked to table "own_parent".`})
		s.exec("ALTER TABLE own_parent ENABLE ROW LEVEL SECURITY")
		s.actor("own_reader")
		ownershipPolicyRows(s, "SELECT payload FROM own_parent ORDER BY payload", []string{"payload"}, [][]any{{int64(7)}})
		s.actor("")
		s.exec("ALTER TABLE own_parent OWNER TO own_target")
		for name, id := range ids {
			ownershipOwner(s, name, "own_target")
			ownershipBool(s, "SELECT oid="+fixture.Position(int(id))+" FROM pg_class WHERE relname='"+name+"'", true)
		}
		ownershipOwner(s, "own_nearby", "own_source")
		ownershipOwner(s, "own_child", "own_source")
		s.actor("")
		ownershipSelect(s, "SELECT currval('own_parent_id_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		s.actor("own_target")
		s.run(fixture.Exec("INSERT INTO own_parent DEFAULT VALUES", 1))
		ownershipSelect(s, "SELECT id,identity_id,payload FROM ONLY own_parent ORDER BY id", []string{"id", "identity_id", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(7)}, {int64(2), int64(2), int64(9)}})
		s.actor("own_reader")
		ownershipPolicyRows(s, "SELECT payload FROM own_parent ORDER BY payload", []string{"payload"}, [][]any{{int64(7)}, {int64(9)}})
		s.actor("")
		ownershipSelect(s, "SELECT description FROM pg_description WHERE classoid=1259 AND objoid='own_parent'::regclass::oid AND objsubid=0", []string{"description"}, []string{"TEXT"}, [][]any{{"ownership sentinel"}})
		if s.backend.PG {
			ownershipSelect(s, "SELECT policyname FROM pg_policies WHERE tablename='own_parent'", []string{"policyname"}, []string{"NAME"}, [][]any{{"own_parent_policy"}})
		}
		s.actor("own_target")
		s.exec("ALTER TABLE own_parent FORCE ROW LEVEL SECURITY")
		ownershipSelect(s, "SELECT payload FROM own_parent", []string{"payload"}, []string{"INT4"}, nil)
		s.actor("own_reader")
		ownershipPolicyRows(s, "SELECT payload FROM own_parent ORDER BY payload", []string{"payload"}, [][]any{{int64(7)}, {int64(9)}})
		s.actor("own_target")
		s.exec("ALTER TABLE own_parent DISABLE ROW LEVEL SECURITY")
		s.actor("")
		s.exec("GRANT " + s.bootstrap + " TO own_target WITH INHERIT FALSE, SET TRUE")
		ownershipInherited(s, "own_target", s.bootstrap, "USAGE", false)
		for _, typ := range []struct{ name, kind string }{{"own_e", "TYPE"}, {"own_d", "DOMAIN"}, {"own_c", "TYPE"}} {
			s.exec("ALTER " + typ.kind + " " + typ.name + " OWNER TO own_target")
			s.actor("own_target")
			s.exec("ALTER " + typ.kind + " " + typ.name + " OWNER TO " + s.bootstrap)
			s.actor("")
			s.exec("ALTER " + typ.kind + " " + typ.name + " OWNER TO own_target")
		}
		// Supplemental PG catalogs are not a request for absent native providers.
		if s.backend.PG {
			ownershipCount(s, "SELECT count(*) FROM pg_type t JOIN pg_type a ON a.oid=t.typarray JOIN pg_roles r ON r.oid=t.typowner WHERE t.typname IN('own_e','own_d','own_c') AND a.typelem=t.oid AND a.typowner=t.typowner AND r.rolname='own_target'", 3)
			ownershipBool(s, "SELECT t.typowner=c.relowner AND a.typowner=c.relowner FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='own_parent'::regclass", true)
		}
		ownershipOwner(s, "own_c", "own_target")
		// Explicit ordinary privileges remain revoked, even from the successor;
		// ownership's implicit grant options still permit it to grant to a reader.
		ownershipSeed(s, "own_empty", "own_source")
		s.actor("own_source")
		s.exec("REVOKE ALL ON own_empty FROM own_source")
		s.actor("")
		s.exec("ALTER TABLE own_empty OWNER TO own_target")
		s.actor("own_target")
		step := fixture.RoleReferenceError("SELECT id FROM own_empty", "42501", "permission denied for table own_empty", "", s.prepared, "execute")
		step.SelectRows = true
		s.run(step)
		s.exec("GRANT SELECT ON own_empty TO own_reader")
		s.actor("own_reader")
		ownershipRows(s, "own_empty")
		s.actor("")
		// OLD owner as grantor and grantee, existing successor entries, PUBLIC,
		// unrelated edges and column options survive the aclnewowner collision.
		ownershipSeed(s, "own_collision", "own_source")
		s.actor("own_source")
		s.exec("GRANT SELECT ON own_collision TO own_target")
		s.exec("GRANT UPDATE(id) ON own_collision TO own_target WITH GRANT OPTION")
		s.exec("GRANT SELECT ON own_collision TO PUBLIC")
		s.exec("GRANT INSERT ON own_collision TO own_other")
		s.exec("REVOKE SELECT ON own_collision FROM own_source")
		s.actor("")
		s.exec("ALTER TABLE own_collision OWNER TO own_target")
		s.actor("own_target")
		ownershipRows(s, "own_collision")
		s.exec("GRANT UPDATE(id) ON own_collision TO own_reader")
		s.actor("own_reader")
		s.run(fixture.Exec("UPDATE own_collision SET id=7", 1))
		s.actor("own_other")
		s.run(fixture.Exec("INSERT INTO own_collision VALUES(9)", 1))
		s.actor("")
		if s.backend.PG {
			ownershipCount(s, "SELECT count(*) FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantor WHERE c.relname='own_collision' AND r.rolname='own_source'", 0)
			ownershipCount(s, "SELECT count(*) FROM pg_attribute c CROSS JOIN LATERAL aclexplode(c.attacl) a JOIN pg_roles r ON r.oid=a.grantor WHERE c.attrelid='own_collision'::regclass AND r.rolname='own_source'", 0)
		}
	})
}
