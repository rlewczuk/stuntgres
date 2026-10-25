package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// The released expectation TSV is test evidence, never a provider-discovery
// registry. Exercise publication and real typed-column evaluation together.
func TestReferenceLsegOperatorCatalog(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			var facts [][]any
			for _, row := range pgOperatorFacts(t) {
				id := row[0].(int64)
				if id == 1535 || id >= 1586 && id <= 1590 {
					facts = append(facts, row)
				}
			}
			if len(facts) != 6 {
				t.Fatalf("independent LSEG release records: %d want 6", len(facts))
			}
			q := "SELECT oid,oprname,oprnamespace,oprowner,oprkind,oprcanmerge,oprcanhash,oprleft,oprright,oprresult,oprcom,oprnegate,oprcode::oid AS oprcode,oprrest::oid AS oprrest,oprjoin::oid AS oprjoin FROM pg_operator WHERE oid IN (1535,1586,1587,1588,1589,1590) ORDER BY oid"
			types := append([]string(nil), pgOperatorTypes...)
			for i := 12; i < 15; i++ {
				types[i] = "OID"
			}
			s := fixture.Select(q, pgOperatorColumns, types, facts)
			fixture.PgDescriptionRows(t, p, b, s)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
			links := fixture.Select("SELECT oid,oprcode,oprrest,oprjoin FROM pg_operator WHERE oid IN (1535,1586,1587,1588,1589,1590) ORDER BY oid", []string{"oid", "oprcode", "oprrest", "oprjoin"}, []string{"OID", "REGPROC", "REGPROC", "REGPROC"}, [][]any{
				{int64(1535), "lseg_eq", "eqsel", "eqjoinsel"},
				{int64(1586), "lseg_ne", "neqsel", "neqjoinsel"},
				{int64(1587), "lseg_lt", "-", "-"},
				{int64(1588), "lseg_le", "-", "-"},
				{int64(1589), "lseg_gt", "-", "-"},
				{int64(1590), "lseg_ge", "-", "-"},
			})
			fixture.PgDescriptionRows(t, p, b, links)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(links))
			run(fixture.Exec("CREATE TABLE lseg_operator_values(id integer NOT NULL,a lseg,b lseg)", 0))
			run(fixture.Exec("INSERT INTO lseg_operator_values VALUES(1,'0,0,3,4','0,0,3,4'),(2,'0,0,3,4','3,4,0,0'),(3,'0,0,3,4','0,0,0,6'),(4,NULL,'0,0,3,4')", 4))
			for _, tc := range []struct {
				op   string
				oid  int64
				want [3]bool
			}{
				{"=", 1535, [3]bool{true, false, false}},
				{"<>", 1586, [3]bool{false, true, true}},
				{"<", 1587, [3]bool{false, false, true}},
				{"<=", 1588, [3]bool{true, true, true}},
				{">", 1589, [3]bool{false, false, false}},
				{">=", 1590, [3]bool{true, true, false}},
			} {
				t.Run(tc.op, func(t *testing.T) {
					// An absent published row cannot silently pass this executable
					// consumer: the cross join would erase all four expected rows.
					query := "SELECT v.id,o.oid,v.a" + tc.op + "v.b AS matches FROM lseg_operator_values v CROSS JOIN pg_operator o WHERE o.oid=$1::oid ORDER BY v.id"
					rows := [][]any{{int64(1), tc.oid, tc.want[0]}, {int64(2), tc.oid, tc.want[1]}, {int64(3), tc.oid, tc.want[2]}, {int64(4), tc.oid, nil}}
					s := fixture.Select(query, []string{"id", "oid", "matches"}, []string{"INT4", "OID", "BOOL"}, rows, tc.oid)
					fixture.PgDescriptionRows(t, p, b, s)
					fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
					stmt, err := b.DB.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
					for range 2 {
						fixture.PgDescriptionExecutorRows(t, p, b, held, s)
					}
				})
			}
		})
	}
}
