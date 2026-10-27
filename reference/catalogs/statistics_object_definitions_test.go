package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const statisticsDefinitionQuery = "SELECT stxname,stxkeys,stxkind,stxstattarget,stxexprs FROM pg_catalog.pg_statistic_ext WHERE stxrelid='stats_t'::regclass ORDER BY stxname"

var statisticsDefinitionNames = []string{"stxname", "stxkeys", "stxkind", "stxstattarget", "stxexprs"}
var statisticsDefinitionTypes = []string{"NAME", "INT2VECTOR", "_CHAR", "INT2", "PG_NODE_TREE"}

func statisticsRows(rows ...[]any) fixture.Step {
	return fixture.Select(statisticsDefinitionQuery, statisticsDefinitionNames, statisticsDefinitionTypes, rows)
}
func statisticsError(q, code, message string) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(code, message))
	s.ErrorStage = "execute"
	return s
}

func TestReferenceStatisticsObjectDefinitions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					utility := func(q string) {
						s := fixture.Exec(q, 0)
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					}
					utility("CREATE TABLE stats_t(a INT,b INT,c INT)")
					held, err := conn.PrepareContext(p.Ctx, statisticsDefinitionQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					query := func(rows ...[]any) {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, held, statisticsDefinitionQuery}, b.Recorder, statisticsRows(rows...))
					}
					query()
					utility("CREATE STATISTICS stats_s (mcv,dependencies,dependencies) ON b,a FROM stats_t")
					query([]any{"stats_s", "1 2", "{f,m}", nil, nil})
					oid := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='stats_s'")
					if oid <= 0 {
						t.Fatal("invalid statistics identity", oid)
					}
					run(fixture.Select("SELECT s.stxrelid=c.oid AS relation_link,s.stxnamespace=n.oid AS namespace_link,s.stxowner=c.relowner AS owner_link FROM pg_statistic_ext s JOIN pg_class c ON c.oid=s.stxrelid JOIN pg_namespace n ON n.nspname='public' WHERE s.stxname='stats_s'", []string{"relation_link", "namespace_link", "owner_link"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
					duplicate := statisticsError("CREATE STATISTICS stats_s ON a,c FROM stats_t", "42710", `statistics object "stats_s" already exists`)
					if prepared {
						duplicate = fixture.Prepared(duplicate)
					}
					run(duplicate)
					if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='stats_s'"); got != oid {
						t.Fatal("failed replacement changed OID", got, oid)
					}
					utility("CREATE STATISTICS ON b,a FROM stats_t")
					utility("CREATE STATISTICS ON b,a FROM stats_t")
					query([]any{"stats_s", "1 2", "{f,m}", nil, nil}, []any{"stats_t_b_a_stat", "1 2", "{d,f,m}", nil, nil}, []any{"stats_t_b_a_stat1", "1 2", "{d,f,m}", nil, nil})
					rows, err := conn.QueryContext(p.Ctx, "SELECT * FROM pg_statistic_ext WHERE oid=$1", oid)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					statisticExtMetadata(t, b.PG, columns)
					if !rows.Next() {
						t.Fatal("missing statistics row", rows.Err())
					}
					values := make([]any, 9)
					dest := make([]any, 9)
					for i := range dest {
						dest[i] = &values[i]
					}
					if err = rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					if fixture.Normalize(values[5]) != "1 2" || fixture.Normalize(values[7]) != "{f,m}" || values[6] != nil || values[8] != nil {
						t.Fatal(values)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("extra statistic row", rows.Err())
					}
					rows.Close()
					utility("DROP STATISTICS stats_s,stats_t_b_a_stat,stats_t_b_a_stat1 RESTRICT")
					query()
				})
			}
		})
	}
}

func TestReferenceStatisticsObjectDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT,c INT,d INT,e INT,f INT,g INT,h INT,i INT,w xid)", 0))
			for _, tc := range []struct{ sql, code, message string }{
				{"CREATE STATISTICS bad ON a FROM stats_t", "42P17", "extended statistics require at least 2 columns"},
				{"CREATE STATISTICS bad ON a,a FROM stats_t", "42701", "duplicate column name in statistics definition"},
				{"CREATE STATISTICS bad (bogus) ON a,b FROM stats_t", "42601", `unrecognized statistics kind "bogus"`},
				{"CREATE STATISTICS bad (bogus) ON a FROM stats_t", "42601", `unrecognized statistics kind "bogus"`},
				{"CREATE STATISTICS bad ON missing,b FROM stats_t", "42703", `column "missing" does not exist`},
				{"CREATE STATISTICS bad ON a,b,c,d,e,f,g,h,i FROM stats_t", "54011", "cannot have more than 8 columns in statistics"},
				{"CREATE STATISTICS bad ON tableoid,a FROM stats_t", "0A000", "statistics creation on system columns is not supported"},
				{"CREATE STATISTICS bad ON w,a FROM stats_t", "0A000", `column "w" cannot be used in statistics because its type xid has no default btree operator class`},
				{"DROP STATISTICS missing", "42704", `statistics object "missing" does not exist`},
			} {
				for _, prepared := range []bool{false, true} {
					s := statisticsError(tc.sql, tc.code, tc.message)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(statisticsRows())
				}
			}
			run(fixture.Exec("CREATE STATISTICS eight ON a,b,c,d,e,f,g,h FROM stats_t", 0))
			run(statisticsRows([]any{"eight", "1 2 3 4 5 6 7 8", "{d,f,m}", nil, nil}))
		})
	}
}

func TestReferenceStatisticsObjectNames(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "stats_other")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"CREATE SCHEMA stats_other", "CREATE TABLE stats_other.stats_t(a INT,b INT)", "CREATE STATISTICS ON b,a FROM stats_other.stats_t", "CREATE STATISTICS stats_other.stats_t ON a,b FROM stats_other.stats_t", "CREATE STATISTICS public.stats_t ON a,b FROM stats_other.stats_t", `CREATE STATISTICS stats_other."Quoted S" ON a,b FROM stats_other.stats_t`} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SELECT n.nspname,s.stxname,s.stxkeys FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace WHERE s.stxrelid='stats_other.stats_t'::regclass ORDER BY n.nspname,s.stxname", []string{"nspname", "stxname", "stxkeys"}, []string{"NAME", "NAME", "INT2VECTOR"}, [][]any{{"public", "stats_t", "1 2"}, {"stats_other", "Quoted S", "1 2"}, {"stats_other", "stats_t", "1 2"}, {"stats_other", "stats_t_b_a_stat", "1 2"}}))
			long := strings.Repeat("é", 32)
			run(fixture.Exec(`CREATE STATISTICS "`+long+`" ON a,b FROM stats_other.stats_t`, 0))
			run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname=$1", []string{"stxname"}, []string{"NAME"}, [][]any{{strings.Repeat("é", 31)}}, strings.Repeat("é", 31)))
		})
	}
}
