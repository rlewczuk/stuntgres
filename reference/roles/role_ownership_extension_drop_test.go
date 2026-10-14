package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipExtensionDrop(t *testing.T) {
	ownershipProviderCases(t, func(s *membershipScenario, database string) {
		ownershipProviderSetup(s, database, false)
		ownershipProviderOwners(s, "own_source", s.bootstrap)
		ids := ownershipProviderIDs(s)
		s.actor("own_other")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() {
			s.exec("CREATE TABLE own_ext_consumer(id INT,v TEXT DEFAULT own_provider.unaccent('Hôtel'),CONSTRAINT own_ext_check CHECK(own_provider.unaccent(v)<>''))")
		})
		s.run(fixture.Exec("INSERT INTO own_ext_consumer(id) VALUES(7)", 1))
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() {
			s.exec("CREATE VIEW own_ext_view AS SELECT own_provider.unaccent(v) AS v FROM own_ext_consumer")
			s.exec("CREATE VIEW own_ext_chain AS SELECT v FROM own_ext_view")
		})
		s.exec("CREATE POLICY own_ext_policy ON own_ext_consumer TO own_reader USING(own_provider.unaccent(v)<>'')")
		s.actor("")
		ownershipSeed(s, "own_ext_sentinel", "own_other")
		detail := "default value for column v of table own_ext_consumer depends on function own_provider.unaccent(text)\nconstraint own_ext_check on table own_ext_consumer depends on function own_provider.unaccent(text)\nview own_ext_view depends on function own_provider.unaccent(text)\nview own_ext_chain depends on view own_ext_view\npolicy own_ext_policy on table own_ext_consumer depends on function own_provider.unaccent(text)"
		ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop extension unaccent because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		ownershipProviderOwners(s, "own_source", s.bootstrap)
		ownershipProviderStable(s, ids)
		ownershipHotel(s, "SELECT v FROM own_ext_chain")
		ownershipRows(s, "own_ext_sentinel")
		if s.backend.PG {
			ownershipBool(s, "SELECT has_database_privilege('own_source',current_database(),'CREATE')", true)
		}
		s.clearNotices()
		s.exec("DROP OWNED BY own_source CASCADE")
		ownershipNotices(s, ownershipCascadeNotice("drop cascades to 5 other objects", "drop cascades to default value for column v of table own_ext_consumer\ndrop cascades to constraint own_ext_check on table own_ext_consumer\ndrop cascades to view own_ext_view\ndrop cascades to view own_ext_chain\ndrop cascades to policy own_ext_policy on table own_ext_consumer"))
		ownershipProviderGone(s)
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_ext_view' OR relname='own_ext_chain'", 0)
		if s.backend.PG {
			ownershipCount(s, "SELECT count(*) FROM pg_policies WHERE tablename='own_ext_consumer'", 0)
		}
		ownershipFailure(s, "ALTER POLICY own_ext_policy ON own_ext_consumer", fixture.Diagnostic{Code: "42704", Message: `policy "own_ext_policy" for table "own_ext_consumer" does not exist`})
		ownershipOwner(s, "own_ext_consumer", "own_other")
		ownershipRows(s, "own_ext_sentinel")
		ownershipSelect(s, "SELECT id,v FROM own_ext_consumer", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}})
		s.actor("own_other")
		s.run(fixture.Exec("INSERT INTO own_ext_consumer(id,v) VALUES(9,'')", 1))
		s.run(fixture.Exec("INSERT INTO own_ext_consumer(id) VALUES(10)", 1))
		s.actor("")
		ownershipSelect(s, "SELECT id,v FROM own_ext_consumer ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}, {int64(9), ""}, {int64(10), nil}})
		// Install a fresh unit without replacing the surviving table, then make
		// source own only its members while target independently owns the extension.
		s.exec("ALTER ROLE own_source SUPERUSER")
		s.actor("own_source")
		s.exec("CREATE EXTENSION unaccent SCHEMA own_provider")
		s.actor("")
		s.exec("ALTER ROLE own_source NOSUPERUSER")
		ownershipProviderOwners(s, "own_source", "own_source")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipProviderOwners(s, "own_target", "own_target")
		// Isolate each independent member root. The shared-dependency index has
		// no class/object tie order, so an all-member first-refusal golden is not
		// portable even though every member is protected by the same e-edge.
		for _, member := range ownershipProviderMembers {
			s.t.Run(member.kind+"_"+member.sqlIdentity, func(t *testing.T) {
				child := *s
				child.t = t
				c := &child
				ownershipProviderMemberOwners(c, "own_source", member)
				defer ownershipProviderMemberOwners(c, "own_target", member)
				members := ownershipProviderIDs(c)
				checkOwners := func() {
					ownershipSelect(c, "SELECT r.rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='unaccent'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_target"}})
					sourceProcedures, targetProcedures := int64(1), int64(3)
					dictionaryOwner := "own_target"
					if member.kind == "dictionary" {
						sourceProcedures, targetProcedures = 0, 4
						dictionaryOwner = "own_source"
					}
					for _, expected := range []struct {
						role  string
						count int64
					}{{"own_source", sourceProcedures}, {"own_target", targetProcedures}} {
						ownershipCount(c, "SELECT count(*) FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE (p.proname='unaccent' OR p.proname='unaccent_init' OR p.proname='unaccent_lexize') AND r.rolname='"+expected.role+"'", expected.count)
					}
					ownershipSelect(c, "SELECT r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname='unaccent'", []string{"rolname"}, []string{"NAME"}, [][]any{{dictionaryOwner}})
					if c.backend.PG && member.kind == "procedure" {
						ownershipSelect(c, "SELECT r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.proname='"+member.name+"' AND p.proargtypes::TEXT='"+strings.ReplaceAll(member.signature, ",", " ")+"'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_source"}})
					}
				}
				checkOwners()
				noun := "function own_provider." + member.sqlIdentity
				if member.kind == "dictionary" {
					noun = "text search dictionary own_provider.unaccent"
				}
				refusal := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop " + noun + " because extension unaccent requires it", Hint: "You can drop extension unaccent instead."}
				for _, mode := range []string{"RESTRICT", "CASCADE"} {
					c.clearNotices()
					ownershipFailure(c, "DROP OWNED BY own_source "+mode, refusal)
					ownershipNotices(c)
					checkOwners()
					ownershipProviderStable(c, members)
					ownershipHotel(c, "SELECT own_provider.unaccent('Hôtel') AS v")
					ownershipRows(c, "own_ext_sentinel")
					ownershipSelect(c, "SELECT id,v FROM own_ext_consumer ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}, {int64(9), ""}, {int64(10), nil}})
				}
			})
		}
		ownershipProviderOwners(s, "own_target", "own_target")
		ownershipProviderMemberOwners(s, "own_source")
		ownershipProviderOwners(s, "own_target", "own_source")
		s.clearNotices()
		s.exec("DROP OWNED BY own_source,own_target,own_source RESTRICT")
		ownershipNotices(s)
		ownershipProviderGone(s)
		ownershipRows(s, "own_ext_sentinel")
		ownershipSelect(s, "SELECT id,v FROM own_ext_consumer ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}, {int64(9), ""}, {int64(10), nil}})
	})
}
