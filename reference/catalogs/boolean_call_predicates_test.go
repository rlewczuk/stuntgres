package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var booleanCallFunctions = []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}

// Identity discovery deliberately does not use the visibility functions.
func booleanCallObjects(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) [4][4]int64 {
	t.Helper()
	run := func(q string) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0)) }
	for _, schema := range []string{"bcp_pred_a", "bcp_pred_b", "bcp_pred_off", "bcp_pred_gone"} {
		run("CREATE SCHEMA " + schema)
		run("CREATE TABLE " + schema + ".bcp_object(v int)")
		run("CREATE TYPE " + schema + ".bcp_object_enum AS ENUM ('one')")
		run("CREATE COLLATION " + schema + `.bcp_object FROM pg_catalog."C"`)
		run(conversionCreate(schema+".bcp_object", false))
	}
	var ids [4][4]int64
	for kind, identity := range []struct{ typ, suffix string }{
		{"regclass", ""},
		{"regtype", "_enum"},
		{"regcollation", ""},
		{"", ""},
	} {
		seen := make(map[int64]bool)
		for i, schema := range []string{"bcp_pred_a", "bcp_pred_b", "bcp_pred_off", "bcp_pred_gone"} {
			name := "bcp_object"
			if kind == 3 {
				q := "SELECT classid,objid,objsubid FROM pg_catalog.pg_get_object_address('conversion',ARRAY['" + schema + "','" + name + "'],ARRAY[]::text[])"
				var class, sub int64
				if err := conn.QueryRowContext(p.Ctx, q).Scan(&class, &ids[kind][i], &sub); err != nil {
					t.Fatal(q, err)
				}
				if class != 2607 || sub != 0 {
					t.Fatal("wrong conversion address", class, ids[kind][i], sub)
				}
			} else {
				q := "SELECT '" + schema + "." + name + identity.suffix + "'::" + identity.typ + "::oid"
				if err := conn.QueryRowContext(p.Ctx, q).Scan(&ids[kind][i]); err != nil {
					t.Fatal(q, err)
				}
			}
			id := ids[kind][i]
			if id <= 0 || id > 4294967295 || seen[id] {
				t.Fatal("invalid/distinct identity", kind, ids[kind])
			}
			seen[id] = true
		}
	}
	// DROP TYPE currently supports standalone composites, not enums. Dropping
	// an isolated schema uses the existing cascade path for every object class.
	run("DROP SCHEMA bcp_pred_gone CASCADE")
	run("CREATE TABLE bcp_pred_off.inputs(seq int, id oid)")
	run("SET search_path=bcp_pred_a,bcp_pred_b,pg_catalog")
	return ids
}

func booleanCallScenario(t *testing.T) *fixture.Pair {
	t.Helper()
	for _, schema := range []string{"bcp_pred_a", "bcp_pred_b", "bcp_pred_off", "bcp_pred_gone"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	return fixture.NewScenario(t)
}

func TestReferenceBooleanCallPredicates(t *testing.T) {
	p := booleanCallScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ids := booleanCallObjects(t, p, conn, b)
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			both := func(s fixture.Step) { t.Helper(); run(s); run(fixture.Prepared(s)) }
			for kind, fn := range booleanCallFunctions {
				run(fixture.Exec("TRUNCATE bcp_pred_off.inputs", 0))
				run(fixture.Exec("INSERT INTO bcp_pred_off.inputs VALUES (1,$1),(2,$1),(3,$2),(4,$3),(5,$4),(6,NULL)", 6, ids[kind][0], ids[kind][1], ids[kind][2], ids[kind][3]))
				for _, prefix := range []string{"", "pg_catalog."} {
					t.Run(prefix+fn, func(t *testing.T) {
						call := prefix + fn + "(s.id)"
						from := " FROM bcp_pred_off.inputs s"
						projected := fixture.Select("SELECT "+call+" AS visible"+from+" ORDER BY s.seq", []string{"visible"}, []string{"BOOL"}, [][]any{{true}, {true}, {false}, {false}, {nil}, {nil}})
						projected.CheckMetadata = fixture.VisibilityBoolMetadata
						both(projected)
						for _, tail := range []string{"", ";", " ORDER BY s.seq"} {
							s := fixture.Select("SELECT s.seq"+from+" WHERE "+call+tail, []string{"seq"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
							s.Bag = tail != " ORDER BY s.seq"
							both(s)
						}
						for _, tc := range []struct {
							expr string
							seq  []int64
						}{
							{"(((" + call + ")))", []int64{1, 2}},
							{"NOT (" + call + ")", []int64{3, 4}},
							{call + " AND s.seq IN (1,3)", []int64{1}},
							{"s.seq IN (1,3) AND " + call, []int64{1}},
							{call + " OR s.seq = 3", []int64{1, 2, 3}},
							{"s.seq = 3 OR " + call, []int64{1, 2, 3}},
							{call + " AND NULL", nil},
							{"NULL AND " + call, nil},
							{call + " OR NULL", []int64{1, 2}},
							{"NULL OR " + call, []int64{1, 2}},
						} {
							var rows [][]any
							for _, seq := range tc.seq {
								rows = append(rows, []any{seq})
							}
							both(fixture.Select("SELECT s.seq"+from+" WHERE "+tc.expr+" ORDER BY s.seq", []string{"seq"}, []string{"INT4"}, rows))
						}
						for _, expr := range []string{call + " AND NULL", "NULL AND " + call, call + " OR NULL", "NULL OR " + call, "NOT (" + call + ")", "NOT (" + call + ") AND FALSE OR TRUE", "NOT ((" + call + ") AND (FALSE OR TRUE))"} {
							var values []any
							switch expr {
							case call + " AND NULL", "NULL AND " + call:
								values = []any{nil, nil, false, false, nil, nil}
							case call + " OR NULL", "NULL OR " + call:
								values = []any{true, true, nil, nil, nil, nil}
							case "NOT (" + call + ") AND FALSE OR TRUE":
								values = []any{true, true, true, true, true, true}
							default:
								values = []any{false, false, true, true, nil, nil}
							}
							var rows [][]any
							for _, v := range values {
								rows = append(rows, []any{v})
							}
							s := fixture.Select("SELECT ("+expr+") AS visible"+from+" ORDER BY s.seq", []string{"visible"}, []string{"BOOL"}, rows)
							s.CheckMetadata = fixture.VisibilityBoolMetadata
							both(s)
						}
						for _, arg := range []string{fmt.Sprint(ids[kind][0]), "$1"} {
							var args []any
							if arg == "$1" {
								args = []any{ids[kind][0]}
							}
							s := fixture.Select("SELECT ((("+prefix+fn+"("+arg+")))) AS visible", []string{"visible"}, []string{"BOOL"}, [][]any{{true}}, args...)
							s.CheckMetadata = fixture.VisibilityBoolMetadata
							both(s)
							both(fixture.Select("SELECT 1 AS kept WHERE "+prefix+fn+"("+arg+")", []string{"kept"}, []string{"INT4"}, [][]any{{int64(1)}}, args...))
						}
						s := fixture.Select("SELECT "+call+" AS visible"+from+" WHERE FALSE", []string{"visible"}, []string{"BOOL"}, nil)
						s.CheckMetadata = fixture.VisibilityBoolMetadata
						both(s)
					})
				}
			}
		})
	}
}
