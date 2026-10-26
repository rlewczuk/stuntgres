package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Use the real statement admission host and physical partial coordinator. The
// caller is the bootstrap superuser; only the aggregate owner lacks the selected
// support right, so a mistaken caller-principal check cannot satisfy the proof.
func aggregateCombineStringOwnerAuthority(t *testing.T) {
	for _, support := range []struct{ name, signature string }{
		{"string_agg_transfn", "internal,text,text"},
		{"string_agg_finalfn", "internal"},
		{"string_agg_combine", "internal,internal"},
		{"string_agg_serialize", "internal"},
		{"string_agg_deserialize", "bytea,internal"},
	} {
		for _, grant := range []bool{false, true} {
			label := support.name + "/denied"
			if grant {
				label = support.name + "/owner_granted"
			}
			t.Run(label, func(t *testing.T) {
				e := engine.New()
				for _, sql := range []string{
					"CREATE ROLE ac_worker_owner",
					"GRANT CREATE ON SCHEMA pg_catalog TO ac_worker_owner",
					"ALTER FUNCTION pg_catalog.string_agg(text,text) OWNER TO ac_worker_owner",
					"CREATE TABLE t(id integer)",
					"INSERT INTO t VALUES(1),(2),(3),(4)",
					"REVOKE EXECUTE ON FUNCTION pg_catalog." + support.name + "(" + support.signature + ") FROM PUBLIC",
				} {
					enginetest.Run(t, e, sql)
				}
				if grant {
					enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION pg_catalog."+support.name+"("+support.signature+") TO ac_worker_owner")
				}
				captured := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
				source, err := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
				captured.Close()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(source.Close)
				op, done, err := enginetest.Operation(e, context.Background())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(done)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := scope.Finish(nil); err != nil {
						t.Error(err)
					}
				})
				const sql = "SELECT string_agg(id::text,','::text) AS v FROM t"
				raw := enginetest.Statement(t, sql)
				if err := scope.Gate(raw); err != nil {
					t.Fatal(err)
				}
				if err := scope.AcquireRelation(raw, true); err != nil {
					t.Fatal(err)
				}
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				var query *plan.Query
				if err == nil {
					binding, failure := core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, source.Ref, scope.Input, nil, nil)
					err = failure
					query = binding.Query
					scope.RoutineAuth, scope.RoutineDependencies = auth, binding.Dependencies
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				source.Host = core.HostOf(scope)
				node, eligible := exec.ParallelAggregatePlan(query.Root, source)
				if !eligible {
					t.Fatal("real builtin string owner lost partial eligibility")
				}
				probe := &aggregateCombineProbe{provider: node.Calls[0].Provider}
				node.Calls[0].Provider = probe
				rows, count, failure := exec.FinalizeParallelAggregate(op.Context(), query.Root, node, source, nil, 2)
				if !grant {
					var diagnostic *sqlstate.Error
					if rows != nil || count != 0 || !errors.As(failure, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for function "+support.name {
						t.Fatal(rows, count, failure)
					}
					if probe.encodes.Load() != 0 || probe.decodes.Load() != 0 || probe.combines.Load() != 0 || probe.finals.Load() != 0 {
						t.Fatal("denied support ran opaque phase", probe.encodes.Load(), probe.decodes.Load(), probe.combines.Load(), probe.finals.Load())
					}
				} else {
					if failure != nil || count != 1 {
						t.Fatal(rows, count, failure)
					}
					if text := aggregateCombineString(t, rows); text != "1,2,3,4" {
						t.Fatal(text)
					}
					if probe.encodes.Load() != 2 || probe.decodes.Load() != 2 || probe.combines.Load() != 2 || probe.finals.Load() != 1 {
						t.Fatal("real opaque stages not reached", probe.encodes.Load(), probe.decodes.Load(), probe.combines.Load(), probe.finals.Load())
					}
				}
				if source.View != nil {
					t.Fatal("support admission retained source pin")
				}
			})
		}
	}
}

func aggregateCombineStringSupportRemap(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT string_agg(id::text,','::text) AS v FROM t")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	original := &node.Calls[0]
	clones := make(map[*catalog.Procedure]*catalog.Procedure)
	mapper := &stored.ViewQueryRemapper{
		Catalog: e.CatalogRead(),
		Ref:     func(ref ids.ObjectRef) ids.ObjectRef { return ref },
		Type:    func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ },
		Procedure: func(old *catalog.Procedure) (*catalog.Procedure, error) {
			if old == nil {
				return nil, nil
			}
			if next := clones[old]; next != nil {
				return next, nil
			}
			next := *old
			clones[old] = &next
			return &next, nil
		},
	}
	mapped, err := mapper.Query(node)
	if err != nil {
		t.Fatal(err)
	}
	next := &mapped.(*plan.AggregateNode).Calls[0]
	for i, support := range original.PrimaryStringSupport {
		if support == nil || next.PrimaryStringSupport[i] != clones[support] || next.PrimaryStringSupport[i] == support {
			t.Fatal("string support was not immutably remapped", i)
		}
	}
}
