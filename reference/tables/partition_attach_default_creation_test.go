package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// CREATE's incumbent DEFAULT validation is an affected consumer of admitted
// ATTACH layouts, not a new partition-definition or comparison provider.
func TestReferencePartitionAttachDefaultCreation(t *testing.T) {
	for _, layout := range []struct {
		name           string
		nested, sparse bool
	}{
		{"regular_reordered", false, false}, {"regular_sparse", false, true},
		{"nested_reordered", true, false}, {"nested_sparse", true, true},
	} {
		for _, key := range []struct{ name, expression, bounds, healthy string }{
			{"scalar", "k", "FROM(1) TO(3)", "FROM(10) TO(12)"},
			{"expression", "(k+1)", "FROM(2) TO(4)", "FROM(11) TO(13)"},
			{"multikey", "k,j", "FROM(1,5) TO(3,0)", "FROM(10,0) TO(12,0)"},
		} {
			for _, conflict := range []bool{true, false} {
				outcome := "inverse_valid"
				if conflict {
					outcome = "logical_conflict"
				}
				for _, prepared := range []bool{false, true} {
					route := "direct"
					if prepared {
						route = "prepared"
					}
					t.Run(layout.name+"/"+key.name+"/"+outcome+"/"+route, func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, b := range p.Backends() {
							t.Run(b.Name, func(t *testing.T) {
								run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
								run(fixture.Exec("CREATE TABLE pa_dc_parent(k INT,j INT,payload INT) PARTITION BY RANGE("+key.expression+")", 0))
								prefix := ""
								if layout.sparse {
									prefix = "dead INT,"
								}
								partitioning := ""
								if layout.nested {
									partitioning = " PARTITION BY RANGE(j)"
								}
								defaultPrefix := prefix
								if layout.nested {
									defaultPrefix = ""
								} // Only the ordinary physical leaf needs a sparse layout.
								run(fixture.Exec("CREATE TABLE pa_dc_default("+defaultPrefix+"payload INT,k INT,j INT)"+partitioning, 0))
								if layout.sparse && !layout.nested {
									run(fixture.Exec("ALTER TABLE pa_dc_default DROP COLUMN dead", 0))
								}
								physical := "pa_dc_default"
								if layout.nested {
									physical = "pa_dc_leaf"
									run(fixture.Exec("CREATE TABLE pa_dc_leaf("+prefix+"j INT,payload INT,k INT)", 0))
									if layout.sparse {
										run(fixture.Exec("ALTER TABLE pa_dc_leaf DROP COLUMN dead", 0))
									}
									run(fixture.Exec("ALTER TABLE pa_dc_default ATTACH PARTITION pa_dc_leaf DEFAULT", 0))
								}
								k, j, payload := int64(99), int64(2), int64(2)
								if conflict {
									k, j, payload = 2, 10, 99
								}
								run(fixture.Exec(fmt.Sprintf("INSERT INTO %s(k,j,payload) VALUES(%d,%d,%d)", physical, k, j, payload), 1))
								idCount := int64(1)
								if layout.nested {
									idCount = 2
								}
								run(fixture.Exec("CREATE TABLE pa_dc_ids AS SELECT relname,oid FROM pg_class WHERE relname='pa_dc_default' OR relname='pa_dc_leaf'", idCount))
								run(fixture.Exec("ALTER TABLE pa_dc_parent ATTACH PARTITION pa_dc_default DEFAULT", 0))
								parentRows := func(rows [][]any) {
									run(fixture.Select("SELECT k,j,payload FROM pa_dc_parent ORDER BY k,j", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "INT4"}, rows))
								}
								seed := [][]any{{k, j, payload}}
								parentRows(seed)
								physicalRows := func() {
									rows := [][]any{{payload, k, j}}
									names := []string{"payload", "k", "j"}
									if layout.nested {
										rows = [][]any{{j, payload, k}}
										names = []string{"j", "payload", "k"}
									}
									run(fixture.Select("SELECT * FROM "+physical, names, []string{"INT4", "INT4", "INT4"}, rows))
								}
								physicalRows()
								q := "CREATE TABLE pa_dc_new PARTITION OF pa_dc_parent FOR VALUES " + key.bounds
								step := fixture.Exec(q, 0)
								if conflict {
									// CREATE's DEFAULT-root identity is independently predicted
									// from the existing pinned CREATE contract, not ATTACH's leaf error.
									step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "23514", Message: `updated partition constraint for default partition "pa_dc_default" would be violated by some row`, Schema: "public", Table: "pa_dc_default"})
								}
								if prepared {
									held, err := b.DB.PrepareContext(p.Ctx, q)
									if err != nil {
										t.Fatal(err)
									}
									defer held.Close()
									fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: q}, b.Recorder, step)
								} else {
									run(step)
								}
								parentRows(seed)
								physicalRows()
								newCount := int64(1)
								if conflict {
									newCount = 0
								}
								run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='pa_dc_new'", []string{"count"}, []string{"INT8"}, [][]any{{newCount}}))
								if conflict {
									absent := fixture.ImplicitError("SELECT NULL::pa_dc_new", fixture.Diagnostic{Code: "42704", Message: `type "pa_dc_new" does not exist`, Position: "14"})
									absent.SelectRows = true
									run(absent)
								} else {
									run(fixture.Select("SELECT NULL::pa_dc_new IS NULL AS has_row_type", []string{"has_row_type"}, []string{"BOOL"}, [][]any{{true}}))
								}
								run(fixture.Select("SELECT COUNT(*) FROM pg_class c JOIN pa_dc_ids s ON c.relname=s.relname AND c.oid=s.oid", []string{"count"}, []string{"INT8"}, [][]any{{idCount}}))
								run(fixture.Select("SELECT COUNT(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid JOIN pg_class p ON p.oid=d.refobjid WHERE c.relname='pa_dc_default' AND p.relname='pa_dc_parent' AND d.classid=1259 AND d.refclassid=1259 AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='a'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
								if conflict {
									healthy := fixture.Exec("CREATE TABLE pa_dc_healthy PARTITION OF pa_dc_parent FOR VALUES "+key.healthy, 0)
									healthy.Prepared = prepared
									run(healthy)
									run(fixture.Exec("INSERT INTO pa_dc_parent VALUES(11,7,111)", 1))
									parentRows([][]any{{k, j, payload}, {int64(11), int64(7), int64(111)}})
									run(fixture.Select("SELECT * FROM pa_dc_healthy", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(7), int64(111)}}))
								} else {
									run(fixture.Exec("INSERT INTO pa_dc_parent VALUES(2,7,222)", 1))
									parentRows([][]any{{int64(2), int64(7), int64(222)}, {k, j, payload}})
									run(fixture.Select("SELECT * FROM pa_dc_new", []string{"k", "j", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(7), int64(222)}}))
								}
								physicalRows()
								run(fixture.Select("SELECT COUNT(*) FROM pg_class c JOIN pa_dc_ids s ON c.relname=s.relname AND c.oid=s.oid", []string{"count"}, []string{"INT8"}, [][]any{{idCount}}))
							})
						}
					})
				}
			}
		}
	}
}
