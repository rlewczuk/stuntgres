package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRLSCombinationSourceGuards(t *testing.T) {
	combinationCases(t, func(s *membershipScenario) {
		s.exec("CREATE POLICY sel ON t FOR SELECT USING(id=1)")
		s.exec("CREATE POLICY upd ON t FOR UPDATE USING(id=1) WITH CHECK(TRUE)")
		s.exec("CREATE POLICY del ON t FOR DELETE USING(id=1)")
		s.run(fixture.Exec("UPDATE t SET v=0 WHERE id<>1", 3))
		s.exec("SET ROLE comb_reader")
		s.run(fixture.Select("SELECT x.id,t.v FROM (SELECT 1 AS id UNION ALL SELECT 2) x LEFT JOIN t ON t.id=x.id ORDER BY x.id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}}))
		s.run(fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s.run(fixture.Select("SELECT 100/v AS quotient FROM t WHERE 100/v>0", []string{"quotient"}, []string{"INT4"}, [][]any{{int64(10)}}))
		s.run(fixture.Exec("UPDATE t SET v=100/v", 1))
		s.run(fixture.Exec("DELETE FROM t WHERE 100/v>0", 1))
		s.exec("RESET ROLE")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 2, 3, 4)
	})
	// Named parent frame differs from the physical inherited child's prefix.
	combinationCases(t, func(s *membershipScenario) {
		s.exec("CREATE TABLE prefix(local TEXT)")
		s.exec("CREATE TABLE child() INHERITS(prefix,t)")
		s.run(fixture.Exec("INSERT INTO child(local,id,allowed,v) VALUES('x',5,TRUE,50),('y',6,FALSE,60)", 2))
		s.exec("CREATE POLICY p ON t USING(allowed)")
		s.exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 4, 5)
		s.run(fixture.Exec("UPDATE t SET v=51 WHERE id=5", 1))
		s.exec("RESET ROLE")
		s.run(fixture.Select("SELECT local,id,v FROM child ORDER BY id", []string{"local", "id", "v"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"x", int64(5), int64(51)}, {"y", int64(6), int64(60)}}))
	})
}
func TestReferenceRLSCombinationStoredRuntime(t *testing.T) {
	combinationCases(t, func(s *membershipScenario) {
		s.exec("CREATE TABLE helper(id INT)")
		s.run(fixture.Exec("INSERT INTO helper VALUES(1),(3)", 2))
		s.exec("CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM helper h WHERE h.id=t.id))")
		s.exec("SET ROLE comb_reader")
		combinationError(s, "SELECT id FROM t ORDER BY id", "42501", "permission denied for table helper", true)
		s.exec("RESET ROLE")
		s.exec("GRANT SELECT ON helper TO comb_reader")
		s.exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 3)
		s.exec("RESET ROLE")
		s.exec("ALTER TABLE helper ENABLE ROW LEVEL SECURITY")
		s.exec("CREATE POLICY helper_rows ON helper USING(id=1)")
		s.exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1)
		s.exec("RESET ROLE")
		s.exec("ALTER TABLE helper DISABLE ROW LEVEL SECURITY")
		s.exec("ALTER TABLE helper RENAME TO original_helper")
		s.exec("CREATE TABLE helper(id INT)")
		s.run(fixture.Exec("INSERT INTO helper VALUES(2)", 1))
		s.exec("ALTER TABLE t RENAME COLUMN id TO renamed")
		s.exec("SET ROLE comb_reader")
		s.run(fixture.Select("SELECT renamed FROM t ORDER BY renamed", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
	})
	combinationCases(t, func(s *membershipScenario) {
		s.exec("CREATE DOMAIN positive_int AS INT CHECK(VALUE>0)")
		s.exec("CREATE COLLATION policy_collation FROM \"C\"")
		s.exec("CREATE SEQUENCE policy_calls")
		s.exec("GRANT USAGE ON SEQUENCE policy_calls TO comb_reader,comb_group")
		s.exec("CREATE POLICY p ON t USING(id::positive_int>0 AND 'a' COLLATE policy_collation='a' AND CURRENT_DATE IS NOT NULL AND current_user='comb_reader' AND session_user<>current_user AND nextval('policy_calls')>id-id)")
		s.run(fixture.Select("SELECT is_called FROM policy_calls", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		h := combinationHeld(s, "SELECT id FROM t ORDER BY id")
		s.run(fixture.Select("SELECT is_called FROM policy_calls", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		s.exec("SET ROLE comb_reader")
		combinationHeldIDs(s, h, 1, 2, 3, 4)
		s.exec("RESET ROLE")
		s.run(fixture.Select("SELECT last_value FROM policy_calls", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(4)}}))
		s.exec("SET ROLE comb_group")
		combinationHeldIDs(s, h)
		s.exec("RESET ROLE")
		s.run(fixture.Select("SELECT is_called FROM policy_calls", []string{"is_called"}, []string{"BOOL"}, [][]any{{true}}))
	})
	for _, indirect := range []bool{false, true} {
		combinationCases(t, func(s *membershipScenario) {
			q := "CREATE POLICY recursive ON t USING(EXISTS(SELECT 1 FROM t x WHERE x.id=t.id))"
			if indirect {
				s.exec("CREATE TABLE helper(id INT)")
				s.run(fixture.Exec("INSERT INTO helper VALUES(1)", 1))
				s.exec("GRANT SELECT ON helper TO comb_reader")
				s.exec("ALTER TABLE helper ENABLE ROW LEVEL SECURITY")
				s.exec("CREATE POLICY back ON helper USING(EXISTS(SELECT 1 FROM t WHERE t.id=helper.id))")
				q = "CREATE POLICY recursive ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id))"
			}
			s.exec(q)
			s.exec("SET ROLE comb_reader")
			stage := "execute"
			if s.backend.PG {
				stage = "prepare"
			}
			predefinedFailure(s, "SELECT id FROM t", fixture.Diagnostic{Code: "42P17", Message: `infinite recursion detected in policy for relation "t"`}, true, stage)
		})
	}
}

// The unused dangerous projection must stay unrequested, while the correlated
// key output must retain demand through policy import and ordinary view writes.
func TestReferenceRLSCombinationViewDemand(t *testing.T) {
	for _, negative := range []bool{false, true} {
		name, predicate := "exists", "EXISTS"
		ids := []int64{1, 3}
		if negative {
			name, predicate = "not_exists", "NOT EXISTS"
			ids = []int64{2, 4}
		}
		t.Run(name, func(t *testing.T) {
			combinationCases(t, func(s *membershipScenario) {
				combinationViewDemandSource(s)
				s.exec("CREATE POLICY p ON t FOR SELECT USING(" + predicate + "(SELECT 1 FROM helper_view h WHERE h.lookup=t.id))")
				s.exec("SET ROLE comb_reader")
				combinationIDs(s, "SELECT id FROM t ORDER BY id", ids...)
				s.exec("RESET ROLE")
				// Keep both frozen attribute identity and the projected key's
				// second output slot while names now resolve to different rows.
				s.exec("ALTER TABLE helper RENAME COLUMN id TO retained_id")
				s.exec("ALTER TABLE helper RENAME TO retained_helper")
				s.exec("ALTER VIEW helper_view RENAME COLUMN lookup TO retained_key")
				s.exec("ALTER VIEW helper_view RENAME TO retained_view")
				s.exec("CREATE TABLE helper(id INT)")
				s.run(fixture.Exec("INSERT INTO helper VALUES(2),(4)", 2))
				s.exec("CREATE VIEW helper_view AS SELECT 0 AS unused,id+0 AS lookup FROM helper")
				s.exec("GRANT SELECT ON helper_view TO comb_reader")
				s.exec("ALTER TABLE t RENAME COLUMN id TO retained_id")
				s.exec("SET ROLE comb_reader")
				combinationIDs(s, "SELECT retained_id AS id FROM t ORDER BY retained_id", ids...)
				s.exec("RESET ROLE")
				// Presence-only EXISTS must not demand either projection,
				// particularly the unused division-by-zero expression.
				s.exec("ALTER POLICY p ON t USING(" + predicate + "(SELECT 1 FROM retained_view))")
				presence := []int64{1, 2, 3, 4}
				if negative {
					presence = nil
				}
				s.exec("SET ROLE comb_reader")
				combinationIDs(s, "SELECT retained_id AS id FROM t ORDER BY retained_id", presence...)
				s.exec("RESET ROLE")
				// This ordinary updatable-view predicate uses the same stored
				// query graft, without target RLS or policy-role interference.
				s.exec("CREATE TABLE write_base(id INT,v INT)")
				s.run(fixture.Exec("INSERT INTO write_base VALUES(1,10),(2,20),(3,30),(4,40)", 4))
				s.exec("CREATE VIEW write_filtered AS SELECT id,v FROM write_base WHERE EXISTS(SELECT 1 FROM retained_view h WHERE h.retained_key=write_base.id)")
				updated := fixture.Select("UPDATE write_filtered SET v=v+1 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(3), int64(31)}})
				updated.Bag = true
				s.run(updated)
				s.run(fixture.Select("SELECT id,v FROM write_base ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}, {int64(3), int64(31)}, {int64(4), int64(40)}}))
			})
		})
	}
}
