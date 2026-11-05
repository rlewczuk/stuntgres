package enginetests

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPgStatStatementsResetBinding(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION pg_stat_statements")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
	procedure := view.MustProcedure(ext.Installation.Members[2])
	key := statistics.StatementKey{UserID: 10, DatabaseID: 1, QueryID: 17, TopLevel: true}
	token := e.Cluster.StatementStatistics.Begin(key, key, "owner seed", time.Unix(100, 0))
	defer e.Cluster.StatementStatistics.Release(token)
	e.Cluster.StatementStatistics.RecordExecution(token, statistics.StatementRecord{Key: key, At: time.Unix(100, 0), Milliseconds: 2, Rows: 3})
	// Snapshot owns complete entries but deliberately exposes no map order.
	// Compare every field by the full cluster key, not by slice position.
	snapshot := func() map[statistics.StatementKey]statistics.StatementEntry {
		entries := e.Cluster.StatementStatistics.Snapshot()
		keyed := make(map[statistics.StatementKey]statistics.StatementEntry, len(entries))
		for _, entry := range entries {
			if _, duplicate := keyed[entry.Key]; duplicate {
				t.Fatal("duplicate statement snapshot key", entry.Key)
			}
			keyed[entry.Key] = entry
		}
		return keyed
	}
	before, info := snapshot(), e.Cluster.StatementStatistics.Info()
	for _, tc := range []struct {
		source string
		order  [4]uint8
	}{
		{"SELECT pg_stat_statements_reset()", [4]uint8{0, 1, 2, 3}},
		{"SELECT pg_stat_statements_reset(minmax_only=>false,queryid=>17,userid=>0)", [4]uint8{3, 2, 0, 1}},
		{"SELECT pg_stat_statements_reset(0,queryid:=17,dbid:=0)", [4]uint8{0, 2, 1, 3}},
	} {
		raw := enginetest.Statement(t, tc.source).(*parser.Select).Targets[0].(parser.FunctionExpr)
		binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		bound, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		call, ok := bound.(plan.InstalledPgStatStatementsResetExpr)
		if !ok || call.Procedure != procedure || call.RoutineReference != procedure || call.Ref.Address != procedure.Address() || call.Ref.Domain() != view.Owner().Domain || call.Order != tc.order || int(call.WrittenCount) != len(raw.Args) || call.Usage.Type.ID() != sqltype.Timestamptz {
			t.Fatal("typed installed reset recipe", bound)
		}
		if call.Properties().Volatility != expr.Volatile || !call.Properties().StageDependent || call.PlanningFold() != expr.FoldNever || call.PreflightFold() != expr.PreflightChildren {
			t.Fatal("reset lost volatile/shared admission contract")
		}
		if admitted, sets := expr.SourceTraits(call); !admitted || sets {
			t.Fatal("reset lost scalar FROM admission/cardinality")
		}
		if _, opening := any(call).(expr.SourceOpening); !opening {
			t.Fatal("reset lost genuine installed scalar source opening")
		}
		for _, slot := range tc.order[len(raw.Args):] {
			if call.Args[slot].SourcePosition().Character != 0 {
				t.Fatal("omitted default acquired fictitious source position", slot)
			}
		}
		for i := range call.Labels {
			label := ""
			if i < len(raw.Args) && len(raw.ArgNames) != 0 {
				label = raw.ArgNames[i].Text
			}
			if call.Labels[i] != label {
				t.Fatal("written/default argument label changed", i, call.Labels)
			}
		}
		for i, typ := range []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int8, sqltype.Bool} {
			canonical, _ := view.LookupType(typ)
			if call.Args[i].ResultUsage().Type != canonical {
				t.Fatal("default/supplied formal not canonical typed", i)
			}
		}
		written, err := call.WrittenArguments(raw)
		if err != nil || len(written) != len(raw.Args) {
			t.Fatal("written/default occurrence mapping", written, err)
		}
		for i, arg := range written {
			if !reflect.DeepEqual(arg, call.Args[tc.order[i]]) {
				t.Fatal("capture reordered source arguments", i)
			}
		}
		saved := call
		children := append([]expr.Expr(nil), call.Children()...)
		rebuilt, err := call.Rebuild(call.Info, children)
		if err != nil || !reflect.DeepEqual(rebuilt, saved) {
			t.Fatal("rebuild changed original provenance/identity", rebuilt, err)
		}
		children[0] = nil
		if !reflect.DeepEqual(rebuilt, saved) || !reflect.DeepEqual(call, saved) {
			t.Fatal("rebuild borrowed mutable caller children")
		}
		bad := call
		bad.Order[1] = bad.Order[0]
		if _, err := bad.Rebuild(bad.Info, bad.Children()); err == nil {
			t.Fatal("invalid execution order accepted")
		}
		if _, err := call.Rebuild(call.Info, call.Children()[:3]); err == nil {
			t.Fatal("truncated reset children accepted")
		}
		for _, mutate := range []func(*plan.InstalledPgStatStatementsResetExpr){
			func(n *plan.InstalledPgStatStatementsResetExpr) { n.WrittenCount = 5 },
			func(n *plan.InstalledPgStatStatementsResetExpr) { n.Labels[0] = "unknown" },
		} {
			bad := call
			mutate(&bad)
			if _, err := bad.Rebuild(bad.Info, bad.Children()); err == nil {
				t.Fatal("malformed reset source metadata accepted")
			}
		}
	}
	after := snapshot()
	if !reflect.DeepEqual(after, before) || e.Cluster.StatementStatistics.Info() != info {
		t.Fatal("binding borrowed reset runtime or mutated statistics", after, before)
	}
	left := pgssAnalyzedJumble(t, e, "SELECT pg_stat_statements_reset(0,0,17,false)")
	right := pgssAnalyzedJumble(t, e, "SELECT pg_stat_statements_reset(0,0,18,false)")
	if left.QueryID() == 0 || left.QueryID() != right.QueryID() {
		t.Fatal("reset literals did not use normal selected-call jumble")
	}
	// Released query jumbling retains written arity and NamedArgExpr formal
	// numbers, but ignores literal values and named-notation punctuation.
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"()", "(0)", false},
		{"()", "(0,0,0,false)", false},
		{"()", "(minmax_only=>true)", false},
		{"(minmax_only=>true)", "(minmax_only=>false)", true},
		{"(userid=>0)", "(dbid=>0)", false},
		{"(userid=>0)", "(userid:=0)", true},
	} {
		a := pgssAnalyzedJumble(t, e, "SELECT pg_stat_statements_reset"+tc.left)
		b := pgssAnalyzedJumble(t, e, "SELECT pg_stat_statements_reset"+tc.right)
		if a.QueryID() == 0 || b.QueryID() == 0 || (a.QueryID() == b.QueryID()) != tc.same {
			t.Fatal("reset written/default jumble identity", tc, a.QueryID(), b.QueryID())
		}
	}
	enginetest.Run(t, e, "ALTER FUNCTION pg_stat_statements_reset(oid,oid,bigint,boolean) RENAME TO genuine_reset")
	enginetest.Run(t, e, "CREATE SCHEMA reset_binding_move")
	enginetest.Run(t, e, "ALTER FUNCTION genuine_reset(oid,oid,bigint,boolean) SET SCHEMA reset_binding_move")
	for _, source := range []string{
		"SELECT reset_binding_move.genuine_reset(userid=>4294967295::bigint)",
		"SELECT reset_binding_move.genuine_reset(minmax_only=>false)",
		"SELECT reset_binding_move.genuine_reset(0,queryid=>17)",
		"SELECT reset_binding_move.genuine_reset(minmax_only=>false,queryid=>17,dbid=>0,userid=>0)",
	} {
		raw := enginetest.Statement(t, source).(*parser.Select).Targets[0]
		binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		bound, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal("renamed/moved installed defaultable named call", source, err)
		}
		call, ok := bound.(plan.InstalledPgStatStatementsResetExpr)
		if !ok || call.Procedure.Address() != procedure.Address() {
			t.Fatal("renamed/moved named call lost installed identity", bound)
		}
	}
	enginetest.Run(t, e, "CREATE FUNCTION pg_stat_statements_reset() RETURNS timestamptz LANGUAGE SQL AS $$SELECT now()$$")
	raw := enginetest.Statement(t, "SELECT pg_stat_statements_reset()").(*parser.Select).Targets[0]
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	shadow, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if _, installed := shadow.(plan.InstalledPgStatStatementsResetExpr); installed {
		t.Fatal("ordinary canonical-name shadow acquired installed reset authority")
	}
}
