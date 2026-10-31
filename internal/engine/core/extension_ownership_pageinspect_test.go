package core_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ownershipPageinspectIdentity(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "pageinspect")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	original := before.MustProcedure(ext.Installation.Members[0])
	recipe := original.Origin.Installation
	originals := make([]*catalog.Procedure, len(recipe.Members))
	for i, address := range recipe.Members {
		originals[i] = before.MustProcedure(address)
	}
	symbols := [...]string{"brin_page_type", "brin_metapage_info", "brin_revmap_data", "brin_page_items", "bt_page_items", "fsm_page_contents", "gin_metapage_info", "gin_page_opaque_info", "gin_leafpage_items", "page_header", "page_checksum", "hash_page_type", "hash_page_stats", "hash_page_items", "hash_metapage_info", "heap_page_items", "heap_tuple_infomask_flags", "tuple_data_split", "tuple_data_split", "heap_page_item_attrs", "heap_page_item_attrs"}
	if len(originals) != len(symbols) {
		t.Fatal("incomplete released pageinspect inventory")
	}
	for _, q := range []string{
		`CREATE FUNCTION public.eo_extra(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`,
		`ALTER EXTENSION pageinspect ADD FUNCTION public.eo_extra(integer)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.brin_page_type(bytea)`,
		`ALTER FUNCTION public.brin_page_type(bytea) RENAME TO eo_page_type`,
		`CREATE EXTENSION "uuid-ossp"`,
		`ALTER EXTENSION "uuid-ossp" ADD FUNCTION public.eo_page_type(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.eo_extra(integer)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.brin_metapage_info(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.brin_revmap_data(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.brin_page_items(bytea,regclass)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.bt_page_items(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.fsm_page_contents(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.gin_metapage_info(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.gin_page_opaque_info(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.gin_leafpage_items(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.page_header(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.page_checksum(bytea,bigint)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.hash_page_type(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.hash_page_stats(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.hash_page_items(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.hash_metapage_info(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.heap_page_items(bytea)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.heap_tuple_infomask_flags(integer,integer)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.tuple_data_split(oid,bytea,integer,integer,text)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.tuple_data_split(oid,bytea,integer,integer,text,boolean)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.heap_page_item_attrs(bytea,regclass,boolean)`,
		`ALTER EXTENSION pageinspect DROP FUNCTION public.heap_page_item_attrs(bytea,regclass)`,
		`DROP EXTENSION pageinspect`,
		`ALTER EXTENSION "uuid-ossp" DROP FUNCTION public.eo_page_type(bytea)`,
	} {
		enginetest.RoleExecute(t, e, op, q)
		view := e.CatalogRead()
		p := view.MustProcedure(original.Address())
		if p.Origin.Installation != recipe || p.Implementation != original.Implementation || p.Installed != original.Installed || p.Display != original.Display || p.Owner != original.Owner {
			t.Fatal("membership/identity changes rewrote pageinspect release declaration", q)
		}
		if symbol, err := extension.ProcedureSymbol(view, p); err != nil || symbol != "brin_page_type" {
			t.Fatal("pageinspect lost immutable symbol", q, symbol, err)
		}
		if err := extension.StockPageinspect.ValidateProcedure(view, p); err != nil {
			t.Fatal("genuine pageinspect procedure rejected", q, err)
		}
		if current, err := exec.ValidateStoredRoutineDescriptor(view, original); err != nil || current != p {
			t.Fatal("membership invalidated retained pageinspect carrier", q, err)
		}
		for index, captured := range originals {
			current := view.MustProcedure(captured.Address())
			if current.Origin.Installation != recipe || current.Display != captured.Display || current.Installed != captured.Installed {
				t.Fatal("pageinspect mutation changed a released declaration", q)
			}
			if symbol, err := extension.ProcedureSymbol(view, current); err != nil || symbol != symbols[index] {
				t.Fatal("pageinspect release symbol changed", q, symbol, err)
			}
			if _, err := exec.ValidateStoredRoutineDescriptor(view, captured); err != nil {
				t.Fatal("pageinspect retained capture rejected", q, err)
			}
		}
		if current, ok := view.Extension(ext.Address()); ok {
			if err := extension.StockPageinspect.ValidateExtension(view, current); err != nil {
				t.Fatal("mutable pageinspect member count rejected", q, err)
			}
		}
	}
	view := e.CatalogRead()
	p := view.MustProcedure(original.Address())
	if view.Get(ext.Address()) != nil || catalog.ExtensionMembership(p) != (ids.ObjectAddress{}) || before.MustProcedure(original.Address()) != original || original.Extension != ext.Address() {
		t.Fatal("absent/detached membership mutated immutable predecessor")
	}
	for _, corrupt := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) {
			origin := *p.Origin
			copied := *recipe
			origin.Installation = &copied
			p.Origin = &origin
		},
		func(p *catalog.Procedure) {
			origin := *p.Origin
			origin.Declaration = 1
			p.Origin = &origin
		},
		func(p *catalog.Procedure) { p.Signature = "23" },
		func(p *catalog.Procedure) { p.Installed.Parallel = 'u' },
		func(p *catalog.Procedure) { p.Display.Source = "brin_metapage_info" },
	} {
		candidate := catalog.CloneState(view.State())
		forged := *p
		corrupt(&forged)
		candidate.SetDescriptor(p.Address(), &forged)
		if err := extension.StockPageinspect.ValidateProcedure(view.WithState(candidate), &forged); err == nil {
			t.Fatal("pageinspect accepted forged release declaration")
		}
	}
}

func ownershipPageinspectCanonicalRemap(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "pageinspect")
	view := e.CatalogRead()
	recipe := ext.Installation
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		ext.Address():       {ClassID: ids.ExtensionClassID, ObjectID: 940000},
		ext.TargetNamespace: {ClassID: ids.NamespaceClassID, ObjectID: 940001},
	}
	for i, a := range recipe.Members {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(940100 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	mapped := remap(ext).(*catalog.Extension)
	next := mapped.Installation
	if next == recipe || !next.Valid() || next.Cohort != mapping[ext.Address()] || next.Implementation != recipe.Implementation || next.Version != "1.13" || len(next.Declarations) != 21 {
		t.Fatal("pageinspect remap lost sealed release")
	}
	for i, a := range recipe.Members {
		original := view.MustProcedure(a)
		p := remap(original).(*catalog.Procedure)
		canonical := next.Declarations[i].(*catalog.Procedure)
		if p.Origin.Installation != next || p.Origin.Declaration != i || !p.Origin.Valid(mapping[a]) || next.Members[i] != mapping[a] || canonical.Address() != mapping[a] || canonical.Namespace() != mapping[ext.TargetNamespace] || canonical.Origin != nil {
			t.Fatal("pageinspect remap split canonical release authority", i)
		}
		if p.Installed != original.Installed || canonical.Installed != original.Installed || canonical.Signature != original.Signature || canonical.Result != original.Result || canonical.Display != original.Display {
			t.Fatal("pageinspect remap changed released formals or immutable OUT payload", i)
		}
		if recipe.Declarations[i].Address() != a || original.Origin.Installation != recipe {
			t.Fatal("pageinspect remap mutated source recipe", i)
		}
	}
	forged := *recipe
	if forged.Valid() {
		t.Fatal("copied pageinspect recipe forged a seal")
	}
}

func TestPageinspectLiveCaptureAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION pageinspect")
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, "SELECT brin_page_type(NULL::bytea)").(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	call := node.(plan.InstalledPageinspectExpr)
	scope := pgrowlocksUnitScope(t, e, s)
	if _, err := scope.WriteHost().Pageinspect(call, 0); err != nil {
		t.Fatal("genuine live pageinspect capture rejected", err)
	}
	foreign, ext := ownershipUnitInstall(t, "pageinspect")
	foreignOwner := foreign.CatalogRead().MustProcedure(ext.Installation.Members[0]).Owner
	for _, mutate := range []func(*plan.InstalledPageinspectExpr){
		func(n *plan.InstalledPageinspectExpr) { n.Ref.Epoch++ },
		func(n *plan.InstalledPageinspectExpr) {
			p := *n.Procedure
			p.Owner = foreignOwner
			n.Procedure, n.RoutineReference = &p, &p
		},
		func(n *plan.InstalledPageinspectExpr) {
			p := *n.Procedure
			installation := *p.Origin.Installation
			origin := *p.Origin
			origin.Installation = &installation
			p.Origin = &origin
			n.Procedure, n.RoutineReference = &p, &p
		},
	} {
		bad := call
		mutate(&bad)
		if _, err := scope.WriteHost().Pageinspect(bad, 0); err == nil {
			t.Fatal("forged live pageinspect capture accepted")
		}
	}
	// Exercise the real installed evaluator and statement host. Decoded LSN/XID
	// values and their descriptors must outlive changed input and a later call,
	// not merely compare equal after recomputing an old expression.
	rawHeader := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "page_header"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}}
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	var bound expr.Expr
	if err == nil {
		headerBinder := core.NewExpressionBinder(scope.Op.Context(), e, scope.Input, scope.Op, nil)
		headerBinder.Query.Auth = &auth
		headerBinder.Clause.AggregateClause = "SELECT"
		bound, err = headerBinder.BindFunctionProvider(rawHeader)
		if err == nil {
			scope.RoutineAuth, scope.RoutineDependencies = auth, headerBinder.Dependencies
		}
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	header := bound.(plan.InstalledPageinspectExpr)
	page := make([]byte, 8192)
	binary.LittleEndian.PutUint32(page, 0x01234567)
	binary.LittleEndian.PutUint32(page[4:], 0x89abcdef)
	binary.LittleEndian.PutUint32(page[20:], 0xffffffff)
	bytea := header.Args[0].ResultUsage()
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Catalog: auth.Catalog, Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output}}
	evaluate := func(raw []byte) sqltype.Value {
		t.Helper()
		valueInput, err := sqltype.Binary(bytea.Type, string(raw))
		if err != nil {
			t.Fatal(err)
		}
		argument := plan.LiteralExpr{Info: expr.Info{Usage: bytea}, Value: valueInput}
		next, err := header.Rebuild(header.Info, []expr.Expr{argument})
		if err != nil {
			t.Fatal(err)
		}
		value, err := next.Eval(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := evaluate(page)
	lsnType, xidType := header.Usage.Type.CompositeMember(0).Usage.Type, header.Usage.Type.CompositeMember(8).Usage.Type
	clear(page)
	second := evaluate(bytes.Repeat([]byte{255}, 8192))
	if first.Type() != header.Usage.Type || first.CompositeField(0).Type() != lsnType || first.CompositeField(8).Type() != xidType {
		t.Fatal("decoded result lost exact descriptor authority")
	}
	firstLSN, err := first.CompositeField(0).PGLSN()
	if err != nil || firstLSN != 0x0123456789abcdef {
		t.Fatal("retained LSN changed", firstLSN, err)
	}
	firstXID, err := first.CompositeField(8).XID()
	if err != nil || firstXID != 0xffffffff {
		t.Fatal("retained XID changed", firstXID, err)
	}
	secondLSN, err := second.CompositeField(0).PGLSN()
	if err != nil || secondLSN != 0xffffffffffffffff {
		t.Fatal("fresh LSN did not read changed bytes", secondLSN, err)
	}
	if value, err := header.Args[0].Eval(evaluation); err != nil {
		t.Fatal(err)
	} else if raw, err := value.Binary(); err != nil || raw != "" {
		t.Fatal("rebuild changed captured predecessor input", raw, err)
	}
}
