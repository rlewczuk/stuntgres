package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSCheckTransformations(t *testing.T) {
	for _, delta := range []int{1, -1} {
		for _, route := range []string{"values", "select", "update", "default"} {
			t.Run(fmt.Sprintf("delta=%d/%s", delta, route), func(t *testing.T) {
				rlsCheckCases(t, func(s *membershipScenario) {
					defaultValue, passing, failing := "3", "3", "4"
					if delta == -1 {
						defaultValue, passing, failing = "5", "5", "6"
					}
					s.exec("CREATE TABLE t(id INT PRIMARY KEY,v INT DEFAULT " + defaultValue + ",g INT GENERATED ALWAYS AS(v*2) STORED)")
					if route == "update" {
						s.run(fixture.Exec("INSERT INTO t(id,v) VALUES(1,1),(99,1)", 2))
					}
					rlsCheckTrigger(s, "t", fmt.Sprint(delta))
					rlsCheckPolicies(s, "g<10")
					s.exec("SET ROLE check_writer")
					statement := func(id, value string) string {
						switch route {
						case "select":
							return "INSERT INTO t(id,v) SELECT " + id + "," + value
						case "update":
							return "UPDATE t SET v=" + value + " WHERE id=" + id
						default:
							return "INSERT INTO t(id,v) VALUES(" + id + "," + value + ")"
						}
					}
					pass := statement("1", passing)
					if route == "default" {
						pass = "INSERT INTO t(id) VALUES(1)"
					}
					s.run(fixture.Exec(pass, 1))
					failID := "2"
					if route == "update" {
						failID = "1"
					}
					combinationDeny(s, statement(failID, failing), "", false)
					// A suppressed row never reaches CHECK, even with disallowed NEW.
					s.run(fixture.Exec(statement("99", failing), 0))
					s.run(fixture.Select(statement("99", failing)+" RETURNING id,v,g", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, nil))
					s.exec("RESET ROLE")
					want := [][]any{{int64(1), int64(4), int64(8)}}
					if route == "update" {
						want = append(want, []any{int64(99), int64(1), int64(2)})
					}
					rlsCheckGeneratedRows(s, want)
				})
			})
		}
	}
}

func TestReferenceRLSCheckRoutedTransformations(t *testing.T) {
	for _, multiplier := range []int{2, 3} {
		for _, route := range []string{"values", "select", "update"} {
			t.Run(fmt.Sprintf("child_multiplier=%d/%s", multiplier, route), func(t *testing.T) {
				rlsCheckCases(t, func(s *membershipScenario) {
					s.exec("CREATE TABLE t(id INT,v INT,g INT) PARTITION BY RANGE(id)")
					s.exec("CREATE TABLE check_child(g INT,id INT,v INT)")
					s.exec("ALTER TABLE t ATTACH PARTITION check_child FOR VALUES FROM(0) TO(100)")
					if route == "update" {
						s.run(fixture.Exec(fmt.Sprintf("INSERT INTO t(id,v,g) VALUES(1,1,%d)", multiplier), 1))
					}
					s.exec(fmt.Sprintf("CREATE FUNCTION check_routed_transform() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+1; NEW.g := NEW.v*%d; RETURN NEW; END$$", multiplier))
					s.exec("CREATE TRIGGER transform BEFORE INSERT OR UPDATE ON check_child FOR EACH ROW EXECUTE FUNCTION check_routed_transform()")
					rlsCheckPolicies(s, "g<10")
					s.exec("SET ROLE check_writer")
					statement := func(id string, value int) string {
						switch route {
						case "select":
							return fmt.Sprintf("INSERT INTO t(id,v) SELECT %s,%d", id, value)
						case "update":
							return fmt.Sprintf("UPDATE t SET v=%d WHERE id=%s", value, id)
						default:
							return fmt.Sprintf("INSERT INTO t(id,v) VALUES(%s,%d)", id, value)
						}
					}
					passing := 3
					if multiplier == 3 {
						passing = 2
					}
					s.run(fixture.Exec(statement("1", passing), 1))
					failID := "2"
					if route == "update" {
						failID = "1"
					}
					combinationDeny(s, statement(failID, passing+1), "", false)
					s.exec("RESET ROLE")
					want := [][]any{{int64(1), int64(passing + 1), int64((passing + 1) * multiplier)}}
					rlsCheckGeneratedRows(s, want)
					s.run(fixture.Select("SELECT id,v,g FROM check_child ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, want))
				})
			})
		}
		t.Run(fmt.Sprintf("child_multiplier=%d/inherited_update", multiplier), func(t *testing.T) {
			rlsCheckCases(t, func(s *membershipScenario) {
				s.exec("CREATE TABLE t(id INT,v INT,g INT GENERATED ALWAYS AS(v*2) STORED)")
				s.exec(fmt.Sprintf("CREATE TABLE check_child(g INT GENERATED ALWAYS AS(v*%d) STORED,id INT,v INT)", multiplier))
				s.exec("ALTER TABLE check_child INHERIT t")
				s.run(fixture.Exec("INSERT INTO check_child(id,v) VALUES(1,1)", 1))
				rlsCheckTrigger(s, "check_child", "1")
				rlsCheckPolicies(s, "g<10")
				s.exec("SET ROLE check_writer")
				passing := 3
				if multiplier == 3 {
					passing = 2
				}
				s.run(fixture.Exec(fmt.Sprintf("UPDATE t SET v=%d WHERE id=1", passing), 1))
				combinationDeny(s, fmt.Sprintf("UPDATE t SET v=%d WHERE id=1", passing+1), "", false)
				s.exec("RESET ROLE")
				want := [][]any{{int64(1), int64(passing + 1), int64((passing + 1) * multiplier)}}
				rlsCheckGeneratedRows(s, want)
				s.run(fixture.Select("SELECT id,v,g FROM check_child ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, want))
				s.run(fixture.Select("SELECT id,v,g FROM ONLY t", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, nil))
			})
		})
	}
}
