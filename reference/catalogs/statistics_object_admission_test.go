package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectRelationKinds(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			// These database-wide objects outlive public-schema reset.
			defer func() {
				for _, q := range []string{"DROP SERVER IF EXISTS statistics_kind_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS statistics_kind_fdw CASCADE"} {
					if _, err := b.DB.ExecContext(context.Background(), q); err != nil {
						t.Error(err)
					}
				}
			}()
			for _, q := range []string{
				"CREATE TABLE statistics_kind_heap(a INT,b INT)",
				"CREATE TABLE statistics_kind_partition(a INT,b INT) PARTITION BY RANGE(a)",
				"CREATE MATERIALIZED VIEW statistics_kind_mat AS SELECT a,b FROM statistics_kind_heap WITH NO DATA",
				"CREATE FOREIGN DATA WRAPPER statistics_kind_fdw NO HANDLER NO VALIDATOR",
				"CREATE SERVER statistics_kind_server FOREIGN DATA WRAPPER statistics_kind_fdw",
				"CREATE FOREIGN TABLE statistics_kind_foreign(a INT,b INT) SERVER statistics_kind_server",
				"CREATE VIEW statistics_kind_view AS SELECT a,b FROM statistics_kind_heap",
				"CREATE INDEX statistics_kind_index ON statistics_kind_heap(a)",
				"CREATE SEQUENCE statistics_kind_sequence",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				for _, kind := range []string{"partition", "mat", "foreign"} {
					name := fmt.Sprintf("statistics_kind_%s_%t", kind, prepared)
					s := fixture.Exec("CREATE STATISTICS "+name+" (mcv,dependencies) ON b,a FROM statistics_kind_"+kind, 0)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT s.stxkeys,s.stxkind,s.stxrelid=c.oid AS relation_link,s.stxstattarget,s.stxexprs FROM pg_statistic_ext s JOIN pg_class c ON c.oid=s.stxrelid WHERE s.stxname=$1", []string{"stxkeys", "stxkind", "relation_link", "stxstattarget", "stxexprs"}, []string{"INT2VECTOR", "_CHAR", "BOOL", "INT2", "PG_NODE_TREE"}, [][]any{{"1 2", "{f,m}", true, nil, nil}}, name))
				}
				for _, tc := range []struct{ name, plural string }{{"view", "views"}, {"index", "indexes"}, {"sequence", "sequences"}} {
					name := "statistics_kind_" + tc.name
					want := fixture.ControlsReferenceDiagnostic("42809", `cannot define statistics for relation "`+name+`"`)
					want.Detail = "This operation is not supported for " + tc.plural + "."
					s := fixture.ErrorDiagnostic("CREATE STATISTICS statistics_kind_bad ON a,b FROM "+name, want)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='statistics_kind_bad'", []string{"stxname"}, []string{"NAME"}, nil))
				}
			}
		})
	}
}

func TestReferenceStatisticsObjectVirtualColumn(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT,v INT GENERATED ALWAYS AS (a+b) VIRTUAL)", 0))
			run(fixture.Exec("INSERT INTO stats_t(a,b) VALUES(2,3)", 1))
			run(fixture.Exec("CREATE STATISTICS sentinel_s ON a,b FROM stats_t", 0))
			for _, prepared := range []bool{false, true} {
				s := statisticsError("CREATE STATISTICS rejected_s ON a,v FROM stats_t", "0A000", "statistics creation on virtual generated columns is not supported")
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(statisticsRows([]any{"sentinel_s", "1 2", "{d,f,m}", nil, nil}))
				run(fixture.Select("SELECT a,b,v FROM stats_t", []string{"a", "b", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(3), int64(5)}}))
			}
		})
	}
}
