package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Shared facts are independently sourced from the release catalogs. Restrict
// them by the selected operator family and geometric formals, not native data.
func geometryRelationCatalogFacts(t *testing.T) [][]any {
	t.Helper()
	geometric := func(id int64) bool {
		switch id {
		case 600, 601, 602, 603, 604, 628, 718:
			return true
		}
		return false
	}
	var facts [][]any
	for _, row := range pgOperatorFacts(t) {
		if !geometric(row[7].(int64)) || !geometric(row[8].(int64)) {
			continue
		}
		switch row[1].(string) {
		case "#", "##", "<->", "@>", "<@", "&&", "?#":
			facts = append(facts, row)
		}
	}
	return facts
}

var geometryRelationProcedureNames = map[int64]string{
	0:    "-",
	125:  "box_overlap",
	136:  "on_pb",
	137:  "on_ppath",
	139:  "areasel",
	140:  "areajoinsel",
	187:  "box_contain",
	192:  "box_contained",
	193:  "box_contain_pt",
	239:  "line_distance",
	277:  "inter_sl",
	278:  "inter_lb",
	340:  "poly_contain",
	345:  "poly_contained",
	346:  "poly_overlap",
	357:  "dist_bp",
	361:  "lseg_distance",
	362:  "lseg_interpt",
	363:  "dist_ps",
	364:  "dist_pb",
	365:  "dist_sb",
	366:  "close_ps",
	367:  "close_pb",
	368:  "close_sb",
	369:  "on_ps",
	370:  "path_distance",
	371:  "dist_ppath",
	372:  "on_sb",
	373:  "inter_sb",
	380:  "dist_sp",
	381:  "dist_bs",
	421:  "dist_pathp",
	702:  "dist_lp",
	704:  "dist_ls",
	725:  "dist_pl",
	727:  "dist_sl",
	728:  "dist_cpoly",
	729:  "poly_distance",
	785:  "dist_polyc",
	959:  "on_pl",
	960:  "on_sl",
	961:  "close_pl",
	973:  "path_inter",
	978:  "box_distance",
	980:  "box_intersect",
	991:  "point_distance",
	994:  "lseg_intersect",
	1302: "contsel",
	1303: "contjoinsel",
	1426: "path_contain_pt",
	1428: "poly_contain_pt",
	1429: "pt_contained_poly",
	1453: "circle_contain",
	1458: "circle_contained",
	1459: "circle_overlap",
	1471: "circle_distance",
	1476: "dist_pc",
	1477: "circle_contain_pt",
	1478: "pt_contained_circle",
	1488: "close_ls",
	1489: "close_lseg",
	1494: "line_interpt",
	1495: "line_intersect",
	3275: "dist_ppoly",
	3290: "dist_cpoint",
	3292: "dist_polyp",
}

func TestReferenceGeometryRelationOperatorCatalog(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			facts := geometryRelationCatalogFacts(t)
			predicate := pgOperatorPredicate(facts)
			types := append([]string(nil), pgOperatorTypes...)
			for i := 12; i < 15; i++ {
				types[i] = "OID"
			}
			q := "SELECT oid,oprname,oprnamespace,oprowner,oprkind,oprcanmerge,oprcanhash,oprleft,oprright,oprresult,oprcom,oprnegate,oprcode::oid AS oprcode,oprrest::oid AS oprrest,oprjoin::oid AS oprjoin FROM pg_catalog.pg_operator WHERE " + predicate + " ORDER BY oid"
			s := fixture.Select(q, pgOperatorColumns, types, facts)
			fixture.PgDescriptionRows(t, p, b, s)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
			var links [][]any
			for _, row := range facts {
				links = append(links, []any{row[0], geometryRelationProcedureNames[row[12].(int64)], geometryRelationProcedureNames[row[13].(int64)], geometryRelationProcedureNames[row[14].(int64)]})
			}
			s = fixture.Select("SELECT oid,oprcode,oprrest,oprjoin FROM pg_catalog.pg_operator WHERE "+predicate+" ORDER BY oid", []string{"oid", "oprcode", "oprrest", "oprjoin"}, []string{"OID", "REGPROC", "REGPROC", "REGPROC"}, links)
			fixture.PgDescriptionRows(t, p, b, s)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
			run(fixture.Exec("CREATE TABLE geom_relation_catalog(p point,s lseg,h path,b box,a polygon,l line,c circle)", 0))
			run(fixture.Exec("INSERT INTO geom_relation_catalog VALUES('(0,0)','[(0,0),(2,0)]','[(0,0),(2,0)]','(2,2),(0,0)','((0,0),(2,0),(2,2),(0,2))','{0,1,0}','<(0,0),1>')", 1))
			columns := map[int64]string{600: "p", 601: "s", 602: "h", 603: "b", 604: "a", 628: "l", 718: "c"}
			for _, row := range facts {
				id, op, left, right, result := row[0].(int64), row[1].(string), row[7].(int64), row[8].(int64), row[9].(int64)
				t.Run(fmt.Sprint(id), func(t *testing.T) {
					typ := "BOOL"
					var want any = true
					switch result {
					case 701:
						typ, want = "FLOAT8", float64(0)
					case 603:
						typ, want = "BOX", "(2,2),(0,0)"
					case 600:
						typ, want = "POINT", "(0,0)"
						if op == "#" || id == 1577 || id == 1578 {
							want = nil
						}
						if id == 1567 {
							want = "(1,0)"
						}
					}
					if op == "?#" && left == right && (left == 601 || left == 628 || left == 602) {
						want = false
					}
					// Collinear segment/line has no unique intersection,
					// as observed on the pinned 18.6 oracle.
					if id == 1537 {
						want = false
					}
					// Required catalog row and actual typed-column execution
					// are coupled: absent publication erases the expected row.
					query := "SELECT o.oid::bigint AS oid,v." + columns[left] + " " + op + " v." + columns[right] + " AS value FROM geom_relation_catalog v CROSS JOIN pg_catalog.pg_operator o WHERE o.oid=$1::oid"
					s := fixture.Select(query, []string{"oid", "value"}, []string{"INT8", typ}, [][]any{{id, want}}, id)
					if typ == "BOX" || typ == "POINT" {
						s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
							if column != 1 {
								return false
							}
							if value != nil {
								rawType := reflect.TypeFor[string]()
								if b.PG {
									rawType = reflect.TypeFor[[]byte]()
								}
								if reflect.TypeOf(value) != rawType {
									t.Fatalf("raw %s cell %d: %T want %v", typ, row, value, rawType)
								}
								if v, ok := value.([]byte); ok && v == nil {
									t.Fatal("non-NULL geometry is typed nil bytes")
								}
							}
							return true
						}
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							cols, err := rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							if len(cols) != 2 {
								t.Fatalf("metadata width %d want 2", len(cols))
							}
							c := cols[1]
							scanType := reflect.TypeFor[string]()
							if b.PG {
								scanType = reflect.TypeFor[any]()
							}
							if c.DatabaseTypeName() != typ || c.ScanType() != scanType {
								t.Fatalf("geometry metadata %s/%v want %s/%v", c.DatabaseTypeName(), c.ScanType(), typ, scanType)
							}
							if n, ok := c.Length(); n != 0 || ok {
								t.Fatalf("geometry length %d/%t", n, ok)
							}
							if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
								t.Fatalf("geometry precision %d,%d/%t", p, s, ok)
							}
						}
					}
					// PgDescriptionRows installs its own CheckScan that treats
					// every non-OID cell as a catalog scalar. Use the ordinary
					// runner here so geometric raw callbacks remain authoritative.
					run(s)
					run(fixture.Prepared(s))
					stmt, err := b.DB.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
					for range 2 {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				})
			}
		})
	}
}
