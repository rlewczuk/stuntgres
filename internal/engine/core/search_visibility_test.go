package core_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

var searchInquiryKinds = []struct {
	kind  functions.VisibilityInquiryKind
	name  string
	class ids.OID
}{
	{functions.TSConfigVisibility, "pg_ts_config_is_visible", ids.TSConfigurationClassID},
	{functions.TSDictionaryVisibility, "pg_ts_dict_is_visible", ids.TSDictionaryClassID},
	{functions.TSParserVisibility, "pg_ts_parser_is_visible", ids.TSParserClassID},
	{functions.TSTemplateVisibility, "pg_ts_template_is_visible", ids.TSTemplateClassID},
	{functions.StatisticsObjectVisibility, "pg_statistics_obj_is_visible", ids.StatisticExtClassID},
}

func searchInquiryDefinitions(schema string) []string {
	return []string{
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + schema + ".input(x integer,y integer)",
		"CREATE TEXT SEARCH PARSER " + schema + ".v (START=pg_catalog.prsd_start,GETTOKEN=pg_catalog.prsd_nexttoken,END=pg_catalog.prsd_end,LEXTYPES=pg_catalog.prsd_lextype)",
		"CREATE TEXT SEARCH TEMPLATE " + schema + ".v (INIT=pg_catalog.dsimple_init,LEXIZE=pg_catalog.dsimple_lexize)",
		"CREATE TEXT SEARCH DICTIONARY " + schema + ".v (TEMPLATE=" + schema + ".v)",
		"CREATE TEXT SEARCH CONFIGURATION " + schema + ".v (PARSER=" + schema + ".v)",
		"CREATE STATISTICS " + schema + ".v ON x,y FROM " + schema + ".input",
	}
}

func TestVisibilityInquirySearchKindsAndPaths(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, query := range searchInquiryDefinitions("search_owner") {
		sessionSQL(t, s, query)
	}
	ns := schemaRef(t, e, "search_owner")
	sessionSQL(t, s, "CREATE TEMP TABLE search_temp_seed(x integer)")
	view := e.CatalogRead()
	temporary := s.TemporaryNamespace
	stage := e.BeginCatalogStage(view)
	// Construct real same-class TEMP descriptors through the catalog owner,
	// independently of PostgreSQL's class-specific public DDL restrictions.
	for i, tc := range searchInquiryKinds {
		source := view.Get(view.NameAddress(ids.NameKey{ClassID: tc.class, Namespace: ns.Address, Name: "v"}))
		address := ids.ObjectAddress{ClassID: tc.class, ObjectID: ids.OID(90000 + i)}
		var clone catalog.Descriptor
		switch d := source.(type) {
		case *catalog.TSConfiguration:
			c := *d
			c.ObjectAddress, c.ObjectNamespace, c.ObjectRevision = address, temporary, 1
			clone = &c
		case *catalog.TSDictionary:
			c := *d
			c.ObjectAddress, c.ObjectNamespace, c.ObjectRevision = address, temporary, 1
			clone = &c
		case *catalog.TSParser:
			c := *d
			c.ObjectAddress, c.ObjectNamespace, c.ObjectRevision = address, temporary, 1
			clone = &c
		case *catalog.TSTemplate:
			c := *d
			c.ObjectAddress, c.ObjectNamespace, c.ObjectRevision = address, temporary, 1
			clone = &c
		case *catalog.Statistics:
			c := *d
			c.ObjectAddress, c.ObjectNamespace, c.ObjectRevision = address, temporary, 1
			clone = &c
		default:
			t.Fatalf("unexpected descriptor %T", d)
		}
		if err := stage.Create(clone); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	oid, _ := view.LookupType(sqltype.Oid)
	boolean, _ := view.LookupType(sqltype.Bool)
	integer, _ := view.LookupType(sqltype.Int4)
	for _, tc := range searchInquiryKinds {
		t.Run(tc.name, func(t *testing.T) {
			if tc.kind.Class() != tc.class {
				t.Fatal("class", tc.kind.Class(), tc.class)
			}
			address := view.NameAddress(ids.NameKey{ClassID: tc.class, Namespace: ns.Address, Name: "v"})
			for _, visible := range []bool{true, false} {
				relationPath := []ids.ObjectAddress{ns.Address}
				otherPath := []ids.ObjectAddress{}
				if visible {
					relationPath, otherPath = otherPath, relationPath
				}
				got, err := regobj.VisibilityInquiry(view, relationPath, otherPath, tc.kind, uint32(address.ObjectID), boolean)
				if err != nil {
					t.Fatal(err)
				}
				v, err := got.Boolean()
				if err != nil || v != visible {
					t.Fatal("relation/non-TEMP path authority", v, visible, err)
				}
			}
			// A same-class TEMP descriptor would win on the relation path;
			// the inquiry must choose the non-TEMP descriptor instead.
			relations := []ids.ObjectAddress{temporary, ns.Address}
			others := []ids.ObjectAddress{ns.Address}
			tempAddress := view.NameAddress(ids.NameKey{ClassID: tc.class, Namespace: temporary, Name: "v"})
			for _, subject := range []struct {
				address ids.ObjectAddress
				want    bool
			}{{address, true}, {tempAddress, false}} {
				got, err := regobj.VisibilityInquiry(view, relations, others, tc.kind, uint32(subject.address.ObjectID), boolean)
				if err != nil {
					t.Fatal(err)
				}
				visible, err := got.Boolean()
				if err != nil || visible != subject.want {
					t.Fatal("TEMP shadow entered inquiry", visible, subject.want, err)
				}
			}
			got, err := regobj.VisibilityInquiry(view, nil, others, tc.kind, 0, boolean)
			if err != nil || !got.IsNull() || got.Type() != boolean {
				t.Fatal("typed missing identity", got, err)
			}
			value, err := sqltype.OIDValue(oid, uint32(address.ObjectID))
			if err != nil {
				t.Fatal(err)
			}
			child := plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: value}
			n := functions.VisibilityInquiry{
				Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{Volatility: expr.Stable}},
				Kind: tc.kind, Arg: child,
			}
			if err := n.Validate(); err != nil {
				t.Fatal(err)
			}
			rendering, err := n.Render()
			if err != nil || rendering.Name != tc.name {
				t.Fatal(rendering, err)
			}
			if n.PlanningFold() != expr.FoldNever || n.CallCost() != 10 || len(n.Children()) != 1 {
				t.Fatal("inquiry protocol")
			}
			rebuilt, err := n.Rebuild(n.Info, []expr.Expr{child})
			if err != nil || !n.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
				t.Fatal("rebuild authority", err)
			}
			if _, err := n.Rebuild(n.Info, nil); err == nil {
				t.Fatal("invalid rebuild arity accepted")
			}
			operands, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || operands.Count != 1 || !reflect.DeepEqual(operands.Inline[0], child) {
				t.Fatal("capture operand authority", operands, ok, err)
			}
			catalogDemand, textDemand, visibilityDemand := n.SnapshotDemand(func(expr.Expr) bool { return false })
			if catalogDemand || textDemand || !visibilityDemand || n.IntrinsicVolatility() != expr.Stable {
				t.Fatal("inquiry snapshot/property protocol")
			}
			for _, change := range []func(*functions.VisibilityInquiry){
				func(n *functions.VisibilityInquiry) { n.Kind = 255 },
				func(n *functions.VisibilityInquiry) { n.Arg = nil },
				func(n *functions.VisibilityInquiry) { n.Usage = integer.Usage() },
				func(n *functions.VisibilityInquiry) { n.Arg = plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}} },
				func(n *functions.VisibilityInquiry) { n.Property.Volatility = expr.Immutable },
			} {
				bad := n
				change(&bad)
				if err := bad.Validate(); err == nil {
					t.Fatal("invalid inquiry accepted", bad)
				}
				if _, err := bad.Render(); err == nil {
					t.Fatal("malformed stored inquiry rendered")
				}
				if _, err := (plan.BoundTree{Expression: bad}).StructuralOutput(context.Background()); err == nil {
					t.Fatal("malformed stored inquiry serialized")
				}
			}
			for _, other := range searchInquiryKinds {
				if other.kind == tc.kind {
					continue
				}
				different := n
				different.Kind = other.kind
				if n.SameGroup(different, func(a, b expr.Expr) bool { return true }, true) {
					t.Fatal("cross-kind semantic group")
				}
			}
		})
	}
}

func TestVisibilityInquirySearchCapturedFrontier(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	for _, query := range searchInquiryDefinitions("search_frontier") {
		sessionSQL(t, s, query)
	}
	sessionSQL(t, s, "SET search_path=search_frontier")
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			op.Done()
		}
	}()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !released {
			scope.Finish(context.Canceled)
		}
	}()
	e.Mu.Lock()
	err = core.EngineCaptureVisibilitySnapshotLocked(e, scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	captured := core.InquiryVisibilityCatalog(&scope.Inquiry)
	path := core.InquiryVisibilityOtherPath(&scope.Inquiry)
	savedPath := append([]ids.ObjectAddress(nil), path...)
	relations := core.InquiryVisibilityRelationPath(&scope.Inquiry)
	if len(path) == 0 || len(relations) == 0 || &path[0] == &relations[0] {
		t.Fatal("relation and non-TEMP captures share mutable backing")
	}
	e.Mu.Lock()
	err = core.EngineCaptureVisibilitySnapshotLocked(e, scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	recaptured := core.InquiryVisibilityOtherPath(&scope.Inquiry)
	if len(recaptured) != len(path) || &recaptured[0] != &path[0] {
		t.Fatal("repeated demand replaced owned immutable backing")
	}
	ns := schemaRef(t, e, "search_frontier")
	addresses := make([]ids.ObjectAddress, len(searchInquiryKinds))
	for i, tc := range searchInquiryKinds {
		addresses[i] = captured.NameAddress(ids.NameKey{ClassID: tc.class, Namespace: ns.Address, Name: "v"})
	}
	// Retire each captured descriptor through its owning public DROP seam,
	// removing dependents before their parser/template dependencies.
	for _, query := range []string{
		"DROP TEXT SEARCH CONFIGURATION search_frontier.v",
		"DROP TEXT SEARCH DICTIONARY search_frontier.v",
		"DROP TEXT SEARCH PARSER search_frontier.v",
		"DROP TEXT SEARCH TEMPLATE search_frontier.v",
		"DROP STATISTICS search_frontier.v",
	} {
		sessionSQL(t, peer, query)
	}
	oid, _ := captured.LookupType(sqltype.Oid)
	boolean, _ := captured.LookupType(sqltype.Bool)
	for i, tc := range searchInquiryKinds {
		value, err := sqltype.OIDValue(oid, uint32(addresses[i].ObjectID))
		if err != nil {
			t.Fatal(err)
		}
		n := functions.VisibilityInquiry{Info: expr.Info{Usage: boolean.Usage()}, Kind: tc.kind, Arg: plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: value}}
		got, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: core.HostOf(scope)}})
		if err != nil {
			t.Fatal(err)
		}
		visible, err := got.Boolean()
		if err != nil || !visible {
			t.Fatal("old catalog authority", tc.name, got, err)
		}
	}
	if core.InquiryVisibilityCatalog(&scope.Inquiry) != captured || len(path) != len(savedPath) {
		t.Fatal("capture replaced")
	}
	for i := range path {
		if path[i] != savedPath[i] {
			t.Fatal("captured path backing mutated")
		}
	}
	scope.Finish(context.Canceled)
	op.Done()
	released = true
	sessionSQL(t, s, "SET search_path=public")
	for i, tc := range searchInquiryKinds {
		func() {
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			result, err := scope.Execute(enginetest.Statement(t, fmt.Sprintf("SELECT pg_catalog.%s(%d::oid)", tc.name, addresses[i].ObjectID)), 0, nil)
			if result.Rows != nil {
				defer result.Rows.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			fresh := core.InquiryVisibilityCatalog(&scope.Inquiry)
			if fresh == captured {
				t.Fatal("new execution retained obsolete catalog")
			}
			if _, exists := fresh.Descriptor(addresses[i]); exists {
				t.Fatal("new frontier retained dropped descriptor")
			}
			for _, namespace := range core.InquiryVisibilityOtherPath(&scope.Inquiry) {
				if namespace == ns.Address {
					t.Fatal("new frontier retained old search path")
				}
			}
		}()
	}
}
