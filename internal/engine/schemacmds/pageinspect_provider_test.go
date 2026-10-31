package schemacmds_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)
func TestPageinspectBrinProviderIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	view := e.CatalogRead()
	ext := view.MustExtension(view.State().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pageinspect"}))
	if len(ext.Members) != 17 || extension.StockPageinspect.Control().Version != "1.13" {
		t.Fatal(ext)
	}
	for i, address := range ext.Members {
		p := view.MustProcedure(address)
		m := extension.PageinspectMembers[i]
		if p.Name() != m.Name || p.Signature != m.Signature || p.Result != m.Result || p.Installed != m.Declaration || p.Volatility != expr.Volatile || !p.Strict || core.ProcedureReturnsSet(p) != m.Declaration.ReturnsSet {
			t.Fatal(p)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Result = 16 }, func(p *catalog.Procedure) { p.ObjectRevision = 0 }, func(p *catalog.Procedure) { p.Implementation = new(extension.PageinspectProvider) },
			func(p *catalog.Procedure) { p.Display.Source = "forged_source" },
			func(p *catalog.Procedure) {
				p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{25}, "page", "foreign")
			},
		} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(address, &bad)
			if err := extension.StockPageinspect.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("forged member accepted", bad)
			}
		}
	}
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "brin_page_type"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.InstalledPageinspectExpr)
	if allocations := testing.AllocsPerRun(100, func() {
		if err := n.ValidateArguments(n.Args); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatal("argument validation allocated", allocations)
	}
	invalid := n.Pageinspect
	invalid.Slots[0] = 1
	if err := invalid.ValidateArguments(invalid.Args); err == nil {
		t.Fatal("invalid formal slot accepted")
	}
	if err := n.ValidateArguments([]expr.Expr{nil}); err == nil {
		t.Fatal("nil operand accepted")
	}
	rebuilt, err := n.Rebuild(n.Info, n.Children())
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(plan.InstalledPageinspectExpr)
	if copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.Args[0] != n.Args[0] || copy.Member != n.Member {
		t.Fatal("lost immutable selected authority")
	}
	if err := stored.ValidateInstalledPageinspect(copy, view); err != nil {
		t.Fatal(err)
	}
	bad := n
	bad.Ref.Epoch++
	if err := extension.ValidatePageinspectIdentity(view, bad.Procedure, bad.Ref); err == nil {
		t.Fatal("foreign epoch accepted")
	}
	bad = n
	bad.Member = "brin_revmap_data"
	if err := stored.ValidateInstalledPageinspect(bad, view); err == nil {
		t.Fatal("foreign member accepted")
	}
	info := n.Info
	info.RoutineReference = nil
	if _, err := n.Rebuild(info, n.Children()); err == nil {
		t.Fatal("lost carrier accepted")
	}
	copy.Args[0] = nil
	if n.Args[0] == nil {
		t.Fatal("rebuilt operands alias original backing")
	}
}
func TestPageinspectBrinOwnerCodec(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, sql := range []string{
		"CREATE SCHEMA brin_part",
		"CREATE SCHEMA brin_moved",
		"CREATE ROLE pi_owner",
		"CREATE SCHEMA gin_moved",
		"CREATE ROLE gin_owner",
		"CREATE EXTENSION pageinspect SCHEMA brin_part",
		"ALTER EXTENSION pageinspect SET SCHEMA brin_moved",
		"ALTER FUNCTION brin_moved.brin_page_type(bytea) RENAME TO page_kind",
		"ALTER FUNCTION brin_moved.page_kind(bytea) OWNER TO pi_owner",
		"ALTER FUNCTION brin_moved.gin_metapage_info(bytea) SET SCHEMA gin_moved",
		"ALTER FUNCTION gin_moved.gin_metapage_info(bytea) RENAME TO meta_info",
		"ALTER FUNCTION gin_moved.meta_info(bytea) OWNER TO gin_owner",
		"ALTER FUNCTION brin_moved.gin_page_opaque_info(bytea) SET SCHEMA gin_moved",
		"ALTER FUNCTION gin_moved.gin_page_opaque_info(bytea) RENAME TO opaque_info",
		"ALTER FUNCTION brin_moved.gin_leafpage_items(bytea) SET SCHEMA gin_moved",
		"ALTER FUNCTION gin_moved.gin_leafpage_items(bytea) RENAME TO leaf_items",
		"ALTER FUNCTION gin_moved.leaf_items(bytea) OWNER TO gin_owner",
		"ALTER FUNCTION brin_moved.page_header(bytea) RENAME TO header_info",
		"ALTER FUNCTION brin_moved.header_info(bytea) SET SCHEMA gin_moved",
		"ALTER FUNCTION gin_moved.header_info(bytea) OWNER TO pi_owner",
		"ALTER FUNCTION brin_moved.page_checksum(bytea,bigint) RENAME TO page_sum",
		"ALTER FUNCTION brin_moved.page_sum(bytea,bigint) SET SCHEMA gin_moved",
		"ALTER FUNCTION gin_moved.page_sum(bytea,bigint) OWNER TO gin_owner",
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pageinspect"}))
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	metadata, err := extension.OwnerMetadata(view, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, present, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !present {
		t.Fatal(record, present, err)
	}
	first, items, ginLeaf, header, checksum := -1, -1, -1, -1, -1
	wants := []struct{ schema, name, signature, owner string }{
		{"brin_moved", "page_kind", "pg_catalog.bytea", "pi_owner"},
		{"brin_moved", "brin_metapage_info", "pg_catalog.bytea", "postgres"},
		{"brin_moved", "brin_revmap_data", "pg_catalog.bytea", "postgres"},
		{"brin_moved", "brin_page_items", "pg_catalog.bytea, pg_catalog.regclass", "postgres"},
		{"brin_moved", "bt_page_items", "pg_catalog.bytea", "postgres"},
		{"brin_moved", "fsm_page_contents", "pg_catalog.bytea", "postgres"},
		{"gin_moved", "meta_info", "pg_catalog.bytea", "gin_owner"},
		{"gin_moved", "opaque_info", "pg_catalog.bytea", "postgres"},
		{"gin_moved", "leaf_items", "pg_catalog.bytea", "gin_owner"},
		{"gin_moved", "header_info", "pg_catalog.bytea", "pi_owner"},
		{"gin_moved", "page_sum", "pg_catalog.bytea, bigint", "gin_owner"},
		{"brin_moved", "hash_page_type", "pg_catalog.bytea", "postgres"},
		{"brin_moved", "hash_page_stats", "pg_catalog.bytea", "postgres"},
		{"brin_moved", "hash_page_items", "pg_catalog.bytea", "postgres"},
		{"brin_moved", "hash_metapage_info", "pg_catalog.bytea", "postgres"},
		{"brin_moved", "heap_page_items", "pg_catalog.bytea", "postgres"},
		{"brin_moved", "heap_tuple_infomask_flags", "integer, integer", "postgres"},
	}
	if len(record.Members) != len(wants) {
		t.Fatal("incomplete released owner inventory", record)
	}
	seen := make([]bool, len(wants))
	for i, member := range record.Members {
		if member.Declaration == nil {
			t.Fatal("released procedure lost its immutable declaration", member)
		}
		slot := *member.Declaration
		if slot < 0 || slot >= len(wants) || seen[slot] {
			t.Fatal("invalid released declaration identity", member)
		}
		seen[slot] = true
		want := wants[slot]
		if member.Kind != "procedure" || member.Schema != want.schema || member.Name != want.name || member.Signature != want.signature || member.Owner != want.owner || member.Version != "1.13" || member.Extension != "pageinspect" {
			t.Fatal("released current authority changed", slot, member, want)
		}
		switch *member.Declaration {
		case 0:
			first = i
		case 3:
			items = i
		case 8:
			ginLeaf = i
		case 9:
			header = i
		case 10:
			checksum = i
		}
	}
	if first < 0 || items < 0 || ginLeaf < 0 || header < 0 || checksum < 0 || record.Members[first].Name != "page_kind" || record.Members[first].Owner != "pi_owner" || record.Members[first].Schema != "brin_moved" {
		t.Fatal("current owner identity lost after genuine relocation", record)
	}
	encode := func(record extension.OwnerRecord) string {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		return extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)
	}
	decoded, present, err := extension.DecodeOwners(encode(record))
	if err != nil || !present || !reflect.DeepEqual(decoded, record) {
		t.Fatal("valid moved member owners rejected", decoded, present, err)
	}
	if _, err := extension.ImportOwners(context.Background(), catalog.NewRead(view.Owner(), catalog.CloneState(view.State())), roles, []extension.OwnerRecord{decoded}); err != nil {
		t.Fatal("genuine current authority failed import", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*extension.OwnerRecord)
	}{
		{"version", func(r *extension.OwnerRecord) { r.Version = "1.12" }},
		{"missing_member", func(r *extension.OwnerRecord) { r.Members = r.Members[:3] }},
		{"extra_member", func(r *extension.OwnerRecord) { r.Members = append(r.Members, r.Members[0]) }},
		{"duplicate_declaration", func(r *extension.OwnerRecord) { r.Members[items].Declaration = r.Members[first].Declaration }},
		{"negative_declaration", func(r *extension.OwnerRecord) { declaration := -1; r.Members[first].Declaration = &declaration }},
		{"foreign_declaration", func(r *extension.OwnerRecord) { declaration := len(wants); r.Members[items].Declaration = &declaration }},
		{"wrong_signature", func(r *extension.OwnerRecord) { r.Members[items].Signature = "pg_catalog.bytea, pg_catalog.oid" }},
		{"gin_wrong_signature", func(r *extension.OwnerRecord) { r.Members[ginLeaf].Signature = "pg_catalog.text" }},
		{"header_wrong_signature", func(r *extension.OwnerRecord) { r.Members[header].Signature = "pg_catalog.text" }},
		{"checksum_wrong_second_type", func(r *extension.OwnerRecord) {
			r.Members[checksum].Signature = "pg_catalog.bytea, pg_catalog.regclass"
		}},
		{"wrong_kind", func(r *extension.OwnerRecord) { r.Members[first].Kind = "type" }},
		{"missing_namespace", func(r *extension.OwnerRecord) { r.Members[first].Schema = "" }},
		{"missing_owner", func(r *extension.OwnerRecord) { r.Members[first].Owner = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := record
			bad.Members = append([]extension.MemberOwner(nil), record.Members...)
			tc.mutate(&bad)
			decoded, present, err := extension.DecodeOwners(encode(bad))
			if !present {
				t.Fatal("strict v3 marker was not recognized")
			}
			if err == nil {
				_, err = extension.ImportOwners(context.Background(), catalog.NewRead(view.Owner(), catalog.CloneState(view.State())), roles, []extension.OwnerRecord{decoded})
			}
			if err == nil {
				t.Fatal("invalid reconstructed owner authority accepted", bad)
			}
			if e.CatalogRead().State() != view.State() {
				t.Fatal("owner rejection changed committed source")
			}
		})
	}
}

func TestPageinspectBrinNamedValidation(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "brin_page_items"}}},
		Args: []parser.Expr{
			parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}},
			parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}},
		},
		ArgNames: []parser.Identifier{{Text: "index_oid"}, {Text: "page"}},
	}
	bound, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	call := bound.(plan.InstalledPageinspectExpr)
	if call.Slots != ([2]uint8{1, 0}) {
		t.Fatal("written argument order lost", call.Slots)
	}
	if err := call.ValidateArguments(call.Args); err != nil {
		t.Fatal(err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if err := call.ValidateArguments(call.Args); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatal("named validation allocated", allocations)
	}
	if err := call.ValidateArguments(call.Args[:1]); err == nil {
		t.Fatal("missing operand accepted")
	}
	if err := call.ValidateArguments(append(append([]expr.Expr(nil), call.Args...), call.Args[0])); err == nil {
		t.Fatal("extra operand accepted")
	}
	if err := call.ValidateArguments([]expr.Expr{call.Args[1], call.Args[0]}); err == nil {
		t.Fatal("wrong operand types accepted")
	}
	if err := call.ValidateArguments([]expr.Expr{nil, call.Args[1]}); err == nil {
		t.Fatal("nil named operand accepted")
	}
	typeless := call.Pageinspect
	typeless.Usage = sqltype.Usage{}
	if err := call.ValidateArguments([]expr.Expr{typeless, call.Args[1]}); err == nil {
		t.Fatal("operand without a result type accepted")
	}
	invalid := call.Pageinspect
	invalid.Slots[1] = 1
	if err := invalid.ValidateArguments(invalid.Args); err == nil {
		t.Fatal("duplicate slot accepted")
	}
	invalid = call.Pageinspect
	invalid.Slots[0] = 2
	if err := invalid.ValidateArguments(invalid.Args); err == nil {
		t.Fatal("out-of-range slot accepted")
	}
	borrowed := append([]expr.Expr(nil), call.Args...)
	rebuilt, err := call.Rebuild(call.Info, borrowed)
	if err != nil {
		t.Fatal(err)
	}
	borrowed[0] = nil
	if rebuilt.(plan.InstalledPageinspectExpr).Args[0] == nil || call.Args[0] == nil {
		t.Fatal("rebuild retained borrowed backing")
	}
	assertGinPageinspectIdentity(t, e, b)
}

func TestPageinspectBtreeProviderIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "bt_page_items"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}}
	bound, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := bound.(plan.InstalledPageinspectExpr)
	view := e.CatalogRead()
	if n.Procedure.Display.Source != "bt_page_items_bytea" || n.Member != "bt_page_items" || n.Ref.Domain() != view.Owner().Domain || n.Ref.Epoch != view.Epoch() {
		t.Fatal(n)
	}
	outputs := []sqltype.ID{21, 27, 21, 16, 16, 25, 16, 27, 1010}
	names := []string{"itemoffset", "ctid", "itemlen", "nulls", "vars", "data", "dead", "htid", "tids"}
	if n.ResultUsage().Type.CompositeLen() != len(outputs) {
		t.Fatal(n.ResultUsage())
	}
	for i, id := range outputs {
		m := n.ResultUsage().Type.CompositeMember(i)
		collation := uint32(0)
		if id == sqltype.Text {
			collation = 100
		}
		if m.Attribute != int32(i+1) || m.Name != names[i] || m.Usage.Type.ID() != id || m.Usage.Typmod != -1 || m.Usage.Collation.ID != collation {
			t.Fatal(i, m)
		}
	}
	children := append([]expr.Expr(nil), n.Args...)
	rebuilt, err := n.Rebuild(n.Info, children)
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(plan.InstalledPageinspectExpr)
	children[0] = nil
	if copy.Args[0] == nil || &copy.Args[0] == &n.Args[0] || copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.RoutineReference != n.RoutineReference {
		t.Fatal("retained authority/backing changed")
	}
	// Stored carriers resolve a same-domain declaration against the current view;
	// live invocation identity separately requires the exact catalog epoch.
	stale := n
	stale.Ref.Epoch++
	if err := extension.ValidatePageinspectIdentity(view, stale.Procedure, stale.Ref); err == nil {
		t.Fatal("foreign invocation epoch accepted")
	}
	for _, mutate := range []func(*plan.InstalledPageinspectExpr){
		func(x *plan.InstalledPageinspectExpr) { x.Ref.Address.SubID = 1 },
		func(x *plan.InstalledPageinspectExpr) { x.Ref.Address.ObjectID++ },
		func(x *plan.InstalledPageinspectExpr) { x.Member = "brin_page_items" },
		func(x *plan.InstalledPageinspectExpr) { x.RoutineReference = nil },
		func(x *plan.InstalledPageinspectExpr) {
			x.Usage.Type = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}).SQLType
		},
	} {
		bad := n
		mutate(&bad)
		if err := stored.ValidateInstalledPageinspect(bad, view); err == nil {
			t.Fatal("forged retained authority accepted", bad)
		}
	}
	other := engine.New()
	if err := stored.ValidateInstalledPageinspect(n, other.CatalogRead()); err == nil {
		t.Fatal("foreign domain accepted")
	}
	for _, source := range []string{"bt_page_items", "brin_page_items", ""} {
		state := catalog.CloneState(view.State())
		bad := *n.Procedure
		bad.Display.Source = source
		state.SetDescriptor(bad.Address(), &bad)
		if _, err := extension.ProcedureSymbol(e.Catalog.Read(state), &bad); err == nil {
			t.Fatal("forged source published", source)
		}
	}
	if err := stored.ValidateInstalledPageinspect(n, view); err != nil {
		t.Fatal("rejected candidate mutated original", err)
	}
	if n.Procedure.Display.Source != "bt_page_items_bytea" || n.Args[0] == nil {
		t.Fatal("original retained definition changed")
	}
}

func TestPageinspectFSMProviderIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	view := e.CatalogRead()
	for _, named := range []bool{false, true} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "fsm_page_contents"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}}
		if named {
			raw.ArgNames = []parser.Identifier{{Text: "page"}}
		}
		bound, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := bound.(plan.InstalledPageinspectExpr)
		if n.Member != "fsm_page_contents" || n.Procedure.Display.Source != "fsm_page_contents" || n.Procedure.Signature != "17" || n.ResultUsage() != view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}).SQLType.Usage() || len(n.Args) != 1 || n.Args[0].ResultUsage().Type.ID() != sqltype.Bytea || n.Slots != ([2]uint8{}) {
			t.Fatal(n)
		}
		if n.Ref.Domain() != view.Owner().Domain || n.Ref.Epoch != view.Epoch() || n.Ref.Address != n.Procedure.Address() || n.RoutineReference != n.Procedure {
			t.Fatal("invalid selected identity")
		}
		allowed, sets := n.SourceAdmission()
		if !allowed || sets || n.ScalarSourceOutName() != "" || n.Pageinspect.ScalarSourceOutName() != "" || n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
			t.Fatal("invalid scalar contract")
		}
		if allocations := testing.AllocsPerRun(100, func() {
			if err := n.ValidateArguments(n.Args); err != nil {
				panic(err)
			}
		}); allocations != 0 {
			t.Fatal("validation allocated", allocations)
		}
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
			children := append([]expr.Expr(nil), n.Args...)
			rebuilt, err := rebuild(n.Info, children)
			if err != nil {
				t.Fatal(err)
			}
			copy := rebuilt.(plan.InstalledPageinspectExpr)
			children[0] = nil
			if copy.Args[0] != n.Args[0] || &copy.Args[0] == &n.Args[0] || copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.RoutineReference != n.RoutineReference || copy.Member != n.Member {
				t.Fatal("reconstruction changed authority/backing")
			}
			copy.Args[0] = nil
			if n.Args[0] == nil {
				t.Fatal("children backing aliased")
			}
		}
		for _, mutate := range []func(*plan.InstalledPageinspectExpr){
			func(x *plan.InstalledPageinspectExpr) { x.Member = "brin_page_type" },
			func(x *plan.InstalledPageinspectExpr) { x.Ref.Address.SubID = 1 },
			func(x *plan.InstalledPageinspectExpr) { x.Ref.Address.ObjectID++ },
			func(x *plan.InstalledPageinspectExpr) { x.RoutineReference = nil },
			func(x *plan.InstalledPageinspectExpr) {
				x.Usage.Type = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType
			},
		} {
			bad := n
			mutate(&bad)
			if err := stored.ValidateInstalledPageinspect(bad, view); err == nil {
				t.Fatal("forged carrier accepted")
			}
		}
		other := engine.New()
		if err := stored.ValidateInstalledPageinspect(n, other.CatalogRead()); err == nil {
			t.Fatal("foreign domain accepted")
		}
		for _, source := range []string{"brin_page_type", "fsm_page_contents_bytea", ""} {
			state := catalog.CloneState(view.State())
			bad := *n.Procedure
			bad.Display.Source = source
			state.SetDescriptor(bad.Address(), &bad)
			if _, err := extension.ProcedureSymbol(e.Catalog.Read(state), &bad); err == nil {
				t.Fatal("forged source accepted")
			}
		}
		if err := stored.ValidateInstalledPageinspect(n, view); err != nil {
			t.Fatal("rejection changed original", err)
		}
		if n.Args[0] == nil || n.Procedure.Display.Source != "fsm_page_contents" {
			t.Fatal("original carrier changed")
		}
	}
}
