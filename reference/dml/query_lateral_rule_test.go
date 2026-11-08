package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Each backend checks relational identities independently: allocator-specific
// numeric OIDs are neither expected values nor a cross-backend comparison.
func TestReferenceQueryLateralRuleIdentity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { cteDMLRun(t, p, b, false, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE lat_rule_target(id INT,v INT,o OID,n OID,present BOOL)", 0),
				fixture.Exec("CREATE TABLE lat_rule_src(id INT,v INT)", 0),
				fixture.Exec("CREATE TABLE lat_rule_zero()", 0),
				fixture.Exec("CREATE TABLE lat_rule_audit(id INT,v INT,o OID,n OID,present BOOL)", 0),
				fixture.Exec("INSERT INTO lat_rule_src VALUES(1,101),(2,202),(9,909)", 3),
				fixture.Exec("INSERT INTO lat_rule_zero DEFAULT VALUES", 1),
				fixture.Exec("CREATE RULE lat_rule_log AS ON UPDATE TO lat_rule_target DO ALSO INSERT INTO lat_rule_audit VALUES(OLD.id,NEW.v,NEW.o,NEW.n,NEW.present)", 0),
			} {
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					for _, zeroWidth := range []bool{false, true} {
						t.Run(map[bool]string{false: "ordinary", true: "zero_width_presence"}[zeroWidth], func(t *testing.T) {
							for _, returning := range []bool{false, true} {
								t.Run(map[bool]string{false: "affected", true: "returning"}[returning], func(t *testing.T) {
									run(fixture.Exec("TRUNCATE lat_rule_target,lat_rule_audit", 0))
									run(fixture.Exec("INSERT INTO lat_rule_target(id,v) VALUES(1,10),(2,20),(3,30)", 3))
									from := "lat_rule_src s CROSS JOIN LATERAL (SELECT s.tableoid AS o,s.v AS v,s IS DISTINCT FROM NULL AS present) q"
									secondPresent := true
									if zeroWidth {
										from = "lat_rule_src s LEFT JOIN lat_rule_zero z ON s.id=1 CROSS JOIN LATERAL (SELECT s.tableoid AS o,s.v AS v,z IS DISTINCT FROM NULL AS present) q"
										secondPresent = false
									}
									// NEW's nested query and the predicate are separate consumers
									// from q's child query and its source-free capture manifest.
									query := "UPDATE lat_rule_target t SET v=q.v,o=q.o,n=(SELECT s.tableoid),present=q.present FROM " + from + " WHERE t.id=s.id AND (SELECT s.tableoid)='lat_rule_src'::regclass::oid"
									if returning {
										cteDMLRun(t, p, b, prepared, cteDMLBag(query+" RETURNING t.id,t.v,t.o='lat_rule_src'::regclass::oid AS source_identity,t.o<>t.tableoid AS not_target,t.n=t.o AS nested_identity,t.present", []string{"id", "v", "source_identity", "not_target", "nested_identity", "present"}, []string{"INT4", "INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), int64(101), true, true, true, true}, {int64(2), int64(202), true, true, true, secondPresent}}))
									} else {
										cteDMLRun(t, p, b, prepared, fixture.Exec(query, 2))
									}
									run(cteDMLBag("SELECT id,v,o='lat_rule_src'::regclass::oid AS source_identity,o<>'lat_rule_target'::regclass::oid AS not_target,n=o AS nested_identity,present FROM lat_rule_audit", []string{"id", "v", "source_identity", "not_target", "nested_identity", "present"}, []string{"INT4", "INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), int64(101), true, true, true, true}, {int64(2), int64(202), true, true, true, secondPresent}}))
									run(cteDMLBag("SELECT id,v,o='lat_rule_src'::regclass::oid AS source_identity,n=o AS nested_identity,present FROM lat_rule_target", []string{"id", "v", "source_identity", "nested_identity", "present"}, []string{"INT4", "INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), int64(101), true, true, true}, {int64(2), int64(202), true, true, secondPresent}, {int64(3), int64(30), nil, nil, nil}}))
									run(cteDMLBag("SELECT id,v FROM lat_rule_src", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(202)}, {int64(9), int64(909)}}))
								})
							}
						})
					}
				})
			}
		})
	}
}
