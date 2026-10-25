package reference_test

import (
	"fmt"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent REL_18_6 pg_operator.dat / pg_proc.dat facts. These do not
// depend on native publication or on the not-yet-expanded supported TSV.
var geometryPositionCatalogEntries = []struct {
	oid               int64
	op                string
	typeID, procedure int64
	name              string
	forward, self     bool
}{
	{485, "<<", 604, 341, "poly_left", true, false},
	{486, "&<", 604, 342, "poly_overleft", true, true},
	{487, "&>", 604, 343, "poly_overright", false, true},
	{488, ">>", 604, 344, "poly_right", false, false},
	{493, "<<", 603, 188, "box_left", true, false},
	{494, "&<", 603, 189, "box_overleft", true, true},
	{495, "&>", 603, 190, "box_overright", false, true},
	{496, ">>", 603, 191, "box_right", false, false},
	{506, ">^", 600, 131, "point_above", false, false},
	{507, "<<", 600, 132, "point_left", true, false},
	{508, ">>", 600, 133, "point_right", false, false},
	{509, "<^", 600, 134, "point_below", true, false},
	{800, ">^", 603, 115, "box_above_eq", false, false},
	{801, "<^", 603, 116, "box_below_eq", true, false},
	{1506, "<<", 718, 1454, "circle_left", true, false},
	{1507, "&<", 718, 1455, "circle_overleft", true, true},
	{1508, "&>", 718, 1456, "circle_overright", false, true},
	{1509, ">>", 718, 1457, "circle_right", false, false},
	{1514, "|>>", 718, 1461, "circle_above", false, false},
	{1515, "<<|", 718, 1460, "circle_below", true, false},
	{2570, "<<|", 603, 2562, "box_below", true, false},
	{2571, "&<|", 603, 2563, "box_overbelow", true, true},
	{2572, "|&>", 603, 2564, "box_overabove", false, true},
	{2573, "|>>", 603, 2565, "box_above", false, false},
	{2574, "<<|", 604, 2566, "poly_below", true, false},
	{2575, "&<|", 604, 2567, "poly_overbelow", true, true},
	{2576, "|&>", 604, 2568, "poly_overabove", false, true},
	{2577, "|>>", 604, 2569, "poly_above", false, false},
	{2589, "&<|", 718, 2587, "circle_overbelow", true, true},
	{2590, "|&>", 718, 2588, "circle_overabove", false, true},
	{4161, "|>>", 600, 131, "point_above", false, false},
	{4162, "<<|", 600, 134, "point_below", true, false},
}

func TestReferenceGeometryPositionOperatorCatalog(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			var facts, links [][]any
			for _, e := range geometryPositionCatalogEntries {
				facts = append(facts, []any{e.oid, e.op, int64(11), int64(10), "b", false, false, e.typeID, e.typeID, int64(16), int64(0), int64(0), e.procedure, int64(1300), int64(1301)})
				links = append(links, []any{e.oid, e.name, "positionsel", "positionjoinsel"})
			}
			sort.Slice(facts, func(i, j int) bool { return facts[i][0].(int64) < facts[j][0].(int64) })
			sort.Slice(links, func(i, j int) bool { return links[i][0].(int64) < links[j][0].(int64) })
			predicate := pgOperatorPredicate(facts)
			types := append([]string(nil), pgOperatorTypes...)
			for i := 12; i < 15; i++ {
				types[i] = "OID"
			}
			q := "SELECT oid,oprname,oprnamespace,oprowner,oprkind,oprcanmerge,oprcanhash,oprleft,oprright,oprresult,oprcom,oprnegate,oprcode::oid AS oprcode,oprrest::oid AS oprrest,oprjoin::oid AS oprjoin FROM pg_catalog.pg_operator WHERE " + predicate + " ORDER BY oid"
			s := fixture.Select(q, pgOperatorColumns, types, facts)
			fixture.PgDescriptionRows(t, p, backend, s)
			fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(s))
			s = fixture.Select("SELECT oid,oprcode,oprrest,oprjoin FROM pg_catalog.pg_operator WHERE "+predicate+" ORDER BY oid", []string{"oid", "oprcode", "oprrest", "oprjoin"}, []string{"OID", "REGPROC", "REGPROC", "REGPROC"}, links)
			fixture.PgDescriptionRows(t, p, backend, s)
			fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(s))
			run(fixture.Exec("CREATE TABLE position_catalog(pa point,pb point,ba box,bb box,ga polygon,gb polygon,ca circle,cb circle)", 0))
			run(fixture.Exec("INSERT INTO position_catalog VALUES('(0,0)','(3,3)','(1,1),(0,0)','(4,4),(3,3)','((0,0),(1,0),(1,1),(0,1))','((3,3),(4,3),(4,4),(3,4))','<(0,0),0.5>','<(3,3),0.5>')", 1))
			columns := map[int64]string{600: "p", 603: "b", 604: "g", 718: "c"}
			for _, e := range geometryPositionCatalogEntries {
				t.Run(fmt.Sprint(e.oid), func(t *testing.T) {
					col := columns[e.typeID]
					q := "SELECT o.oid::bigint AS oid,v." + col + "a " + e.op + " v." + col + "b AS forward,v." + col + "b " + e.op + " v." + col + "a AS backward,v." + col + "a " + e.op + " v." + col + "a AS same,NULL::" + map[int64]string{600: "point", 603: "box", 604: "polygon", 718: "circle"}[e.typeID] + " " + e.op + " v." + col + "b AS absent FROM position_catalog v CROSS JOIN pg_catalog.pg_operator o WHERE o.oid=$1::oid"
					s := fixture.Select(q, []string{"oid", "forward", "backward", "same", "absent"}, []string{"INT8", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{e.oid, e.forward, !e.forward, e.self, nil}}, e.oid)
					run(s)
					run(fixture.Prepared(s))
					stmt, err := backend.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
					for range 2 {
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
					}
				})
			}
		})
	}
}
