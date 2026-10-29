package core_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var executionInquiryKinds = []struct {
	name string
	kind functions.VisibilityInquiryKind
}{
	{"pg_function_is_visible", functions.FunctionVisibility},
	{"pg_operator_is_visible", functions.OperatorVisibility},
	{"pg_opclass_is_visible", functions.OpclassVisibility},
	{"pg_opfamily_is_visible", functions.OpfamilyVisibility},
}

func TestVisibilityExecutionProtocol(t *testing.T) {
	for _, tc := range executionInquiryKinds {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, arg := range []string{"id", "NULL", "NULL::integer"} {
				enginetest.SessionSQL(t, s, "CREATE TABLE protocol_check(id OID CHECK("+tc.name+"("+arg+") IS NULL))")
				view := e.CatalogRead()
				relation := view.MustRelation(enginetest.CatalogRef(t, e, view, "protocol_check").Address)
				check := relation.Constraints.Check[0]
				child := check.Expression.(plan.NullTestExpr).Child
				if child.ResultUsage().Type.ID() != sqltype.Bool || check.VisibilitySnapshot != (arg != "NULL") { t.Fatal("typed result or demand", child, check.VisibilitySnapshot) }
				if arg == "NULL" {
					literal, ok := child.(plan.LiteralExpr)
					if !ok || !literal.Value.IsNull() || literal.Value.Type().ID() != sqltype.Bool { t.Fatal("untyped bare NULL", child) }
				} else {
					n := child.(functions.VisibilityInquiry)
					if n.Kind != tc.kind || n.Arg.ResultUsage().Type.ID() != sqltype.Oid || n.Properties().Volatility != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || n.CallCost() != 10 { t.Fatal("incorrect protocol", n) }
					if err := n.Validate(); err != nil { t.Fatal(err) }
					rebuilt, err := n.Rebuild(n.Info, n.Children())
					if err != nil || rebuilt.(functions.VisibilityInquiry).Info != n.Info { t.Fatal("rebuild lost provenance", err) }
					capture, ok, err := n.Capture(expr.CaptureFunction)
					if err != nil || !ok || capture.Count != 1 || capture.Inline[0] != n.Arg { t.Fatal("capture lost operand", capture, err) }
					if !n.SameGroup(rebuilt, func(a, b expr.Expr) bool { return a == b }, true) { t.Fatal("group identity") }
					rendered, err := n.Render()
					if err != nil || rendered.Name != tc.name || !rendered.Replay.AlwaysVisibilitySnapshot { t.Fatal("render/replay", rendered, err) }
					integer, _ := view.LookupType(sqltype.Int4)
					identity, ok := catalog.BuiltinRoutineIdentityBySignature(tc.name, catalog.ProcedureSignature("26"))
					if !ok || n.RoutineReference == nil || n.RoutineReference.RoutineOID() != uint32(identity.ID) { t.Fatal("catalog routine provenance", n.RoutineReference, identity) }
					for _, corrupt := range []func(*functions.VisibilityInquiry){
						func(n *functions.VisibilityInquiry) { n.Kind = 255 },
						func(n *functions.VisibilityInquiry) { n.Usage = integer.Usage() },
						func(n *functions.VisibilityInquiry) { n.Arg = plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)} },
						func(n *functions.VisibilityInquiry) { n.Property.Volatility = expr.Immutable },
					} {
						bad := n; corrupt(&bad)
						if _, err := (stored.ScalarRenderer{Owner: relation}).SQL(context.Background(), view, bad); err == nil { t.Fatal("stored validator accepted malformed inquiry", bad) }
						if _, err := (plan.BoundTree{Expression: bad}).StructuralOutput(context.Background()); err == nil { t.Fatal("structural validator accepted malformed inquiry") }
					}
				}
				if err := table.ValidateChecks(view, relation, map[string]bool{}); err != nil { t.Fatal(err) }
				enginetest.SessionSQL(t, s, "DROP TABLE protocol_check")
			}
		})
	}
}

func TestVisibilityExecutionReleasedAndHelperIdentities(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION intagg")
	view := e.CatalogRead()
	boolean, _ := view.LookupType(sqltype.Bool)
	path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
	if _, exists := view.Descriptor(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6399}); exists { t.Fatal("lookup-only pg_get_aios unexpectedly executable; retain a lookup-only control") }
	check := func(id ids.OID) {
		got, err := regobj.VisibilityInquiry(view, nil, path, functions.FunctionVisibility, uint32(id), boolean)
		visible, valueErr := got.Boolean()
		if err != nil || valueErr != nil || !visible { t.Fatal(id, got, err, valueErr) }
	}
	for _, id := range []ids.OID{1397, 6399, 2108} { check(id) }
	count := 0
	for _, owner := range view.HelperOwners() {
		for _, helper := range owner.Aggregate.Helpers {
			if _, exists := view.Descriptor(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}); exists { t.Fatal("helper materialized as descriptor") }
			check(helper.ID); count++
		}
	}
	if count == 0 { t.Fatal("no actual aggregate helpers exercised") }
}

func TestVisibilityExecutionCapturedFrontier(t *testing.T) {
	for _, tc := range executionInquiryKinds {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE SCHEMA captured", "CREATE FUNCTION captured.f(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION captured.op(integer,integer) RETURNS boolean LANGUAGE SQL AS 'SELECT true'",
				"CREATE OPERATOR captured.## (LEFTARG=integer, RIGHTARG=integer, FUNCTION=captured.op)",
				"CREATE OPERATOR CLASS captured.c FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)",
				"CREATE OPERATOR FAMILY captured.fam USING btree", "SET search_path=captured",
			} { enginetest.SessionSQL(t, s, q) }
			view := e.CatalogRead()
			var address ids.ObjectAddress
			for _, d := range view.Descriptors() {
				if d.Address().ClassID == tc.kind.Class() && d.Namespace() == schemaRef(t, e, "captured").Address { address = d.Address(); break }
			}
			if address.ObjectID == 0 { t.Fatal("missing real target") }
			op, err := s.Acquire(ctx); if err != nil { t.Fatal(err) }; defer op.Done()
			scope, err := e.StartStatement(op); if err != nil { t.Fatal(err) }; defer scope.Finish(context.Canceled)
			e.Mu.Lock(); err = core.EngineCaptureVisibilitySnapshotLocked(e, scope); e.Mu.Unlock()
			if err != nil { t.Fatal(err) }
			captured := core.InquiryVisibilityCatalog(&scope.Inquiry)
			oid, _ := captured.LookupType(sqltype.Oid); boolean, _ := captured.LookupType(sqltype.Bool)
			value, err := sqltype.OIDValue(oid, uint32(address.ObjectID)); if err != nil { t.Fatal(err) }
			n := functions.VisibilityInquiry{Info: expr.Info{Usage: boolean.Usage()}, Kind: tc.kind, Arg: plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: value}}
			enginetest.SessionSQL(t, peer, "DROP SCHEMA captured CASCADE")
			got, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: core.HostOf(scope)}})
			visible, valueErr := got.Boolean()
			if err != nil || valueErr != nil || !visible || core.InquiryVisibilityCatalog(&scope.Inquiry) != captured || captured == e.CatalogRead() { t.Fatal("retained frontier changed", got, err, valueErr) }
			fresh := enginetest.Tuples(enginetest.SessionSQL(t, peer, fmt.Sprintf("SELECT %s(%d::oid)", tc.name, address.ObjectID)))
			if len(fresh) != 1 || fresh[0][0] != nil { t.Fatal("fresh frontier retained dropped target", fresh) }
		})
	}
}

func TestVisibilityExecutionTemporaryCapture(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TEMP TABLE temp_anchor(id integer)",
		"CREATE FUNCTION public.temp_fn(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
		"CREATE FUNCTION pg_temp.temp_fn(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
		"CREATE FUNCTION public.temp_op(integer,integer) RETURNS boolean LANGUAGE SQL AS 'SELECT true'",
		"CREATE OPERATOR public.## (LEFTARG=integer, RIGHTARG=integer, FUNCTION=public.temp_op)",
		"CREATE OPERATOR pg_temp.## (LEFTARG=integer, RIGHTARG=integer, FUNCTION=public.temp_op)",
		"CREATE OPERATOR CLASS public.temp_class FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)",
		"CREATE OPERATOR CLASS pg_temp.temp_class FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)",
		"CREATE OPERATOR FAMILY public.temp_family USING btree",
		"CREATE OPERATOR FAMILY pg_temp.temp_family USING btree",
		"SET search_path=pg_temp,public",
	} { enginetest.SessionSQL(t, s, q) }
	op, err := s.Acquire(context.Background()); if err != nil { t.Fatal(err) }; defer op.Done()
	scope, err := e.StartStatement(op); if err != nil { t.Fatal(err) }; defer scope.Finish(context.Canceled)
	e.Mu.Lock(); err = core.EngineCaptureVisibilitySnapshotLocked(e, scope); e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	captured := core.InquiryVisibilityCatalog(&scope.Inquiry)
	oid, _ := captured.LookupType(sqltype.Oid); boolean, _ := captured.LookupType(sqltype.Bool)
	for _, tc := range executionInquiryKinds {
		count := 0
		for _, d := range captured.Descriptors() {
			if d.Address().ClassID != tc.kind.Class() { continue }
			namespace, ok := captured.Namespace(d.Namespace())
			if !ok || namespace.Temporary == (ids.SessionToken{}) { continue }
			value, err := sqltype.OIDValue(oid, uint32(d.Address().ObjectID)); if err != nil { t.Fatal(err) }
			n := functions.VisibilityInquiry{Info: expr.Info{Usage: boolean.Usage()}, Kind: tc.kind, Arg: plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: value}}
			got, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}})
			visible, valueErr := got.Boolean()
			if err != nil || valueErr != nil || visible { t.Fatal("TEMP leaked into captured nonrelation path", tc.name, got, err, valueErr) }
			count++
		}
		if count == 0 { t.Fatal("missing actual temporary descriptor", tc.name) }
	}
}

func TestVisibilityExecutionFoldedNullCapture(t *testing.T) {
	for i, tc := range executionInquiryKinds {
		t.Run(tc.name, func(t *testing.T) {
			b := protocolBinder(t)
			raw, bound := protocolBind(t, b, "SELECT pg_catalog."+tc.name+"(NULL)")
			executable, ok := bound.(plan.LiteralExpr)
			if !ok || !executable.Value.IsNull() || executable.Value.Type().ID() != sqltype.Bool || b.Consumer.VisibilitySnapshot { t.Fatal("bare NULL execution contract", bound) }
			if err := b.CaptureViewLiterals(raw, bound, analyze.ViewTargetLiteral, 0); err != nil { t.Fatal(err) }
			path := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral, Node: 1}
			captured, ok := b.Capture.Literals[path]
			if !ok || len(b.Capture.Literals) != 1 || captured.ResultUsage().Type.ID() != sqltype.Oid || !captured.Value.IsNull() { t.Fatal("actual normalized typed NULL operand capture", b.Capture.Literals) }
			if b.Consumer.VisibilitySnapshot { t.Fatal("capture introduced executable visibility demand") }
			structure, err := functions.BindVisibilityInquiryStructure(b, raw.(parser.FunctionExpr))
			if err != nil || structure.Kind != tc.kind || structure.ResultUsage().Type.ID() != sqltype.Bool || structure.Arg.ResultUsage().Type.ID() != sqltype.Oid { t.Fatal("normalized reconstruction", structure, err) }
			id := []ids.OID{2081, 2082, 2083, 3829}[i]
			if structure.RoutineReference == nil || structure.RoutineReference.RoutineOID() != uint32(id) { t.Fatal("reconstruction lost original routine identity") }
			p := b.BindingCatalog().MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id})
			d := p.Builtin
			if d == nil || d.Identity.Name != tc.name || d.Identity.ID != id || d.Identity.Signature != "26" || d.Result != 16 || d.Kind != catalog.FunctionRoutine || !d.Strict || d.Volatility != expr.Stable || d.Parallel != 's' || d.ReturnsSet || d.Defaults != 0 || d.Variadic != 0 || d.Providers != functions.CatalogRoutineProvider { t.Fatal("canonical executable declaration", d) }
			if p.Display.Language != catalog.InternalRoutineLanguage || p.Display.Source != tc.name || p.Cost != 10 || routine.BuiltinRoutineCost(d) != 10 { t.Fatal("canonical immutable display and cost", p.Display, p.Cost) }
		})
	}
}

func TestVisibilityExecutionRepresentedIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE FUNCTION public.abs(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
	view := e.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	pg := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	user := view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: public, Name: "abs", Signature: "23"})
	if user.ObjectID == 0 { t.Fatal("missing user overload") }
	original := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1397})
	changed := *original
	changed.ObjectName, changed.ObjectNamespace = "shifted_abs", public
	changed.ObjectRevision++
	stage := e.BeginCatalogStage(view)
	if err := stage.Replace(&changed); err != nil { t.Fatal(err) }
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	boolean, _ := view.LookupType(sqltype.Bool)
	identity, ok := regobj.RoutineIdentityByOID(view, 1397)
	if !ok || identity.Name != "shifted_abs" || identity.Namespace != ids.PublicNamespaceOID { t.Fatal("stale represented identity", identity) }
	for _, tc := range []struct {
		id ids.OID
		path []ids.ObjectAddress
		want bool
	}{
		{1397, []ids.ObjectAddress{pg}, false},
		{1397, []ids.ObjectAddress{pg, public}, true},
		{user.ObjectID, []ids.ObjectAddress{pg, public}, true},
		{1396, []ids.ObjectAddress{pg, public}, true},
	} {
		got, err := regobj.VisibilityInquiry(view, nil, tc.path, functions.FunctionVisibility, uint32(tc.id), boolean)
		visible, valueErr := got.Boolean()
		if err != nil || valueErr != nil || visible != tc.want { t.Fatal("represented rename/move or stale release alias", tc.id, got, err, valueErr) }
	}
}

func TestVisibilityExecutionMissingTyped(t *testing.T) {
	view := engine.New().CatalogRead()
	boolean, _ := view.LookupType(sqltype.Bool)
	for _, tc := range executionInquiryKinds {
		for _, id := range []ids.OID{0, 4294967295} {
			if _, exists := view.Descriptor(ids.ObjectAddress{ClassID: tc.kind.Class(), ObjectID: id}); exists { t.Fatal("missing-OID control exists", tc.name, id) }
			if tc.kind == functions.FunctionVisibility {
				if _, exists := regobj.RoutineIdentityByOID(view, id); exists { t.Fatal("missing lookup-only identity control exists", id) }
			}
			value, err := regobj.VisibilityInquiry(view, nil, nil, tc.kind, uint32(id), boolean)
			if err != nil || !value.IsNull() || value.Type() != boolean { t.Fatal("runtime missing result lost BOOL identity", tc.name, value, err) }
		}
	}
}
