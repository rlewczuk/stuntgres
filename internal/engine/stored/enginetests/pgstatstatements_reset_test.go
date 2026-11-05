package enginetests_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func pgssResetStoredInstall(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "SET pg_stat_statements.track='none'")
	enginetest.SessionSQL(t, s, "CREATE EXTENSION pg_stat_statements")
	return s
}

func pgssResetStoredProjection(t *testing.T, e *core.Engine, s *session.Session, source string) *plan.ProjectNode {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	query := enginetest.BindSelect(t, e, op, source)
	project, ok := query.Root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 1 {
		t.Fatal("missing actual reset projection")
	}
	return project
}

func TestPgStatStatementsResetStored(t *testing.T) {
	source := engine.New()
	s := pgssResetStoredInstall(t, source)
	history, resetInfo := source.Cluster.StatementStatistics.Snapshot(), source.Cluster.StatementStatistics.Info()
	const sql = "SELECT pg_stat_statements_reset(minmax_only=>false,queryid=>17,userid=>0)"
	project := pgssResetStoredProjection(t, source, s, sql)
	call, ok := project.Expressions[0].(plan.InstalledPgStatStatementsResetExpr)
	if !ok || call.Occurrence == 0 || call.Ref.Epoch != source.CatalogRead().Epoch() || call.RoutineReference != call.Procedure {
		t.Fatal("stored reset lost original occurrence/reference")
	}
	view := source.CatalogRead()
	if err := stored.ValidateInstalledPgStatStatementsReset(call, view); err != nil {
		t.Fatal(err)
	}
	saved := call
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){call.Rebuild, call.Substitute} {
		children := append([]expr.Expr(nil), call.Children()...)
		next, err := rebuild(call.Info, children)
		if err != nil || !reflect.DeepEqual(next, saved) {
			t.Fatal("reconstruction changed source occurrence/children/default order", err)
		}
		children[0] = nil
		if !reflect.DeepEqual(next, saved) {
			t.Fatal("reconstruction retained mutable caller operand buffer")
		}
	}
	if next, changed, err := stored.RetagTypes(view, call, identity); err != nil || changed || !reflect.DeepEqual(next, saved) {
		t.Fatal("identity retag changed immutable declaration generation", changed, err)
	}
	if next, err := stored.RemapExpression(call, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil); err != nil || !reflect.DeepEqual(next, saved) {
		t.Fatal("identity remap changed immutable source", err)
	}
	for _, mutate := range []func(*plan.InstalledPgStatStatementsResetExpr){
		func(n *plan.InstalledPgStatStatementsResetExpr) { n.Ref.Epoch++ },
		func(n *plan.InstalledPgStatStatementsResetExpr) { d := *n.Procedure; n.Procedure = &d },
		func(n *plan.InstalledPgStatStatementsResetExpr) { d := *n.Procedure; n.RoutineReference = &d },
		func(n *plan.InstalledPgStatStatementsResetExpr) { n.Ref.Address.SubID = 1 },
		func(n *plan.InstalledPgStatStatementsResetExpr) { n.Order[1] = n.Order[0] },
		func(n *plan.InstalledPgStatStatementsResetExpr) { n.WrittenCount = 5 },
		func(n *plan.InstalledPgStatStatementsResetExpr) { n.Labels[0] = "userid" },
		func(n *plan.InstalledPgStatStatementsResetExpr) { n.Labels[3] = "dbid" },
		func(n *plan.InstalledPgStatStatementsResetExpr) {
			literal := n.Args[1].(plan.LiteralExpr)
			value, err := sqltype.OIDValue(literal.Value.Type(), 1)
			if err != nil {
				t.Fatal(err)
			}
			literal.Value = value
			n.Args[1] = literal
		},
		func(n *plan.InstalledPgStatStatementsResetExpr) { n.Args[3] = n.Args[0] },
		func(n *plan.InstalledPgStatStatementsResetExpr) { d := *n.Usage.Type; n.Usage.Type = &d },
		func(n *plan.InstalledPgStatStatementsResetExpr) {
			d := *n.Procedure
			d.Display.Source = "forged"
			n.Procedure, n.RoutineReference = &d, &d
		},
	} {
		bad := call
		mutate(&bad)
		if _, _, err := stored.RetagTypes(view, bad, identity); err == nil {
			t.Fatal("stale/malformed stored reset accepted")
		}
		if !reflect.DeepEqual(call, saved) {
			t.Fatal("failed stored validation mutated source recipe")
		}
	}
	// Validate actual candidate descriptor mutations, not just a copied pointer
	// rejected for being nonresident in the original catalog.
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Signature = "26,26,20" },
		func(p *catalog.Procedure) { p.Installed.Defaults = 3 },
		func(p *catalog.Procedure) { p.Installed.ArgumentNames[0] = "other" },
		func(p *catalog.Procedure) {
			p.LiteralDefaults = catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 23, Text: "1"})
		},
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Result = 2249 },
	} {
		bad := *call.Procedure
		mutate(&bad)
		candidate := catalog.CloneState(view.State())
		candidate.SetDescriptor(bad.Address(), &bad)
		if err := extension.StockPgStatStatements.ValidateProcedure(source.Catalog.Read(candidate), &bad); err == nil {
			t.Fatal("forged reset canonical recipe accepted")
		}
	}
	enginetest.SessionSQL(t, s, "CREATE SCHEMA reset_stored_move")
	enginetest.SessionSQL(t, s, "ALTER FUNCTION pg_stat_statements_reset(oid,oid,bigint,boolean) RENAME TO reset_current")
	enginetest.SessionSQL(t, s, "ALTER FUNCTION reset_current(oid,oid,bigint,boolean) SET SCHEMA reset_stored_move")
	current := source.CatalogRead()
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), current, call); err == nil {
		t.Fatal("volatile stage-dependent reset acquired closed DEFAULT admission")
	}
	for _, render := range []func() (string, error){
		func() (string, error) {
			return (stored.ScalarRenderer{}).ScalarFunctionSQL(context.Background(), current, call)
		},
		func() (string, error) {
			return (stored.Deparser{View: current}).SQL(context.Background(), call, stored.DeparseParent{})
		},
	} {
		text, err := render()
		if err != nil || !strings.Contains(text, "reset_stored_move") || !strings.Contains(text, "reset_current") {
			t.Fatal("deparser lost current genuine qualified identity", text, err)
		}
		last := -1
		for _, label := range []string{"minmax_only", "queryid", "userid"} {
			at := strings.Index(text, label)
			if at <= last {
				t.Fatal("deparser reordered written effects/default operands", text)
			}
			last = at
		}
		if strings.Contains(text, "dbid") {
			t.Fatal("omitted reset default acquired a written argument", text)
		}
	}
	if !reflect.DeepEqual(call, saved) || !reflect.DeepEqual(source.Cluster.StatementStatistics.Snapshot(), history) || source.Cluster.StatementStatistics.Info() != resetInfo {
		t.Fatal("binding/rendering mutated source or borrowed reset runtime")
	}
	t.Run("destination_provenance", func(t *testing.T) {
		target := engine.New()
		target.Mu.Lock()
		_, err := target.Catalog.AllocateOIDLocked()
		target.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		pgssResetStoredInstall(t, target)
		targetHistory, targetInfo := target.Cluster.StatementStatistics.Snapshot(), target.Cluster.StatementStatistics.Info()
		tv := target.CatalogRead()
		te := tv.MustExtension(tv.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
		se := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{se.Address(): te.Address()}
		for i, address := range se.Installation.Members {
			mapping[address] = te.Installation.Members[i]
		}
		ref := func(old ids.ObjectRef) ids.ObjectRef {
			if old == (ids.ObjectRef{}) {
				return old
			}
			address := old.Address
			if next, exists := mapping[address]; exists {
				address = next
			}
			return tv.Owner().Domain.Ref(tv.Epoch(), address)
		}
		types := func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == nil {
				return nil
			}
			next, _ := tv.LookupType(old.ID())
			return next
		}
		principal := func(old authorization.PrincipalRef) (authorization.PrincipalRef, error) {
			if old != (authorization.PrincipalRef{}) && old != view.Owner().Principals.Principal(old.ID()) {
				return authorization.PrincipalRef{}, fmt.Errorf("foreign source principal")
			}
			return tv.Owner().Principals.Principal(old.ID()), nil
		}
		principals := catalog.NewProcedureRemapper(catalog.PrincipalRemap{Principal: principal, SeededOwner: func(old authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
			return principal(old)
		}})
		procedureMappings := 0
		procedure := func(old *catalog.Procedure) (*catalog.Procedure, error) {
			procedureMappings++
			if err := extension.StockPgStatStatements.ValidateProcedure(view, old); err != nil {
				return nil, err
			}
			return principals(extension.RemapDescriptor(old, mapping).(*catalog.Procedure))
		}
		mapper := stored.ViewQueryRemapper{Catalog: tv, Ref: ref, Type: types, Procedure: procedure}
		mapped, err := mapper.Nested(project)
		if err != nil {
			t.Fatal("genuine installed reset remap", err)
		}
		next := mapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgStatStatementsResetExpr)
		if procedureMappings != 1 || next.RoutineReference != next.Procedure || next.Ref != ref(call.Ref) || next.Ref.Address == call.Ref.Address || next.Procedure == call.Procedure || next.Usage.Type != types(call.Usage.Type) || next.Usage.Type == call.Usage.Type || next.Source != call.Source || next.Occurrence != call.Occurrence || next.Order != call.Order || next.WrittenCount != call.WrittenCount || next.Labels != call.Labels {
			t.Fatal("reset remap retained source domain/generation or lost original provenance")
		}
		for i, arg := range next.Args {
			if arg.ResultUsage().Type != types(call.Args[i].ResultUsage().Type) {
				t.Fatal("reset remap lost destination formal generation", i)
			}
		}
		if err := stored.ValidateInstalledPgStatStatementsReset(next, tv); err != nil {
			t.Fatal("genuine destination recipe rejected", err)
		}
		if _, _, err := stored.RetagTypes(tv, call, identity); err == nil {
			t.Fatal("unmapped foreign source-domain reset accepted")
		}
		bad := next
		bad.Ref = call.Ref
		if err := stored.ValidateInstalledPgStatStatementsReset(bad, tv); err == nil {
			t.Fatal("mixed-domain reset carrier accepted")
		}
		if !reflect.DeepEqual(call, saved) || !reflect.DeepEqual(target.Cluster.StatementStatistics.Snapshot(), targetHistory) || target.Cluster.StatementStatistics.Info() != targetInfo {
			t.Fatal("destination mapping mutated source or executed reset")
		}
	})
}
