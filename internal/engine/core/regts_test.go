package core_test

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine/core"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// unaccentSQLInstalled installs unaccent through SQL.
func unaccentSQLInstalled(t *testing.T) (*core.Engine, *session.Operation, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION unaccent")
	return e, op, e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
}

// Algorithm snapshots clone real installed descriptors. The extra names/IDs
// are private unit inputs, never published providers or public dictionary DDL.
func regTSUnitSnapshot(t *testing.T) (*core.Engine, *session.Operation, catalog.Read, core.AuthorizationView, []ids.ObjectAddress) {
	t.Helper()
	e, op, ext := unaccentSQLInstalled(t)
	view := e.CatalogRead()
	view = view.WithState(catalog.CloneState(view.State()))
	var installed *catalog.TSDictionary
	for _, address := range ext.Members {
		if d, ok := view.TSDictionary(address); ok {
			installed = d
		}
	}
	if installed == nil {
		t.Fatal("installed dictionary missing")
	}
	namespaces := make([]ids.ObjectAddress, 4)
	for i, name := range []string{"Reg.TS", "reg_ts_left", "reg_ts_right", `Reg"TS`} {
		ns := *view.MustNamespace(ext.TargetNamespace)
		ns.ObjectAddress = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(90000 + i)}
		ns.ObjectName = name
		if i == 3 {
			ns.Temporary = e.Catalog.Domain.Session(99)
		}
		view.State().SetDescriptor(ns.Address(), &ns)
		view.State().BindName(catalog.DescriptorNameKey(&ns), ns.Address())
		namespaces[i] = ns.Address()
		d := *installed
		d.ObjectAddress = ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(91000 + i)}
		d.ObjectNamespace = ns.Address()
		view.State().SetDescriptor(d.Address(), &d)
		view.State().BindName(catalog.DescriptorNameKey(&d), d.Address())
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.Recapture(view)
	return e, op, view, auth, namespaces
}
func TestRegTSProviderResolution(t *testing.T) {
	_, _, view, auth, namespaces := regTSUnitSnapshot(t)
	for _, alias := range []sqltype.ID{sqltype.Regconfig, sqltype.Regdictionary} {
		typ, ok := view.LookupType(alias)
		if !ok || sqltype.CommonTypeCategory(typ) != 'N' {
			t.Fatal("text-search numeric category", alias)
		}
	}
	path := core.EffectivePath(auth, []ids.ObjectAddress{namespaces[3], namespaces[1], namespaces[2]}, analyze.OperatorLookup)
	if len(path) != 3 || path[1] != namespaces[1] {
		t.Fatal("TEMP text-search path isolation", path)
	}
	capture := core.InputCapture(auth, path)
	input := regobj.NewTextSearchInput(capture)
	id, err := input.ResolveRegdictionary(context.Background(), "UNACCENT")
	if err != nil || id != 91001 {
		t.Fatal("first-path lookup", id, err)
	}
	if got := regobj.TextSearchSpelling(view, path, 91002, sqltype.Regdictionary, false); got != "reg_ts_right.unaccent" {
		t.Fatal("shadowed output", got)
	}
	for _, text := range []string{` "Reg.TS" . UNACCENT `, `"Reg.TS".unaccent`} {
		id, err := input.ResolveRegdictionary(context.Background(), text)
		if err != nil || id != 91000 {
			t.Fatal("quoted namespace", id, err)
		}
	}
	if got := regobj.TextSearchSpelling(view, path, 91000, sqltype.Regdictionary, false); got != `"Reg.TS".unaccent` {
		t.Fatal("off-path quoting", got)
	}
	if got := regobj.TextSearchSpelling(view, path, 91000, sqltype.Regconfig, false); got != "91000" {
		t.Fatal("cross-class output", got)
	}
	if got := regobj.TextSearchSpelling(view, path, 0, sqltype.Regdictionary, false); got != "-" {
		t.Fatal("zero output", got)
	}
	if id, err := input.ResolveRegdictionary(context.Background(), "simple"); err != nil || id != 3765 {
		t.Fatal("genuine builtin dictionary", id, err)
	}
	for _, tc := range []struct{ input, code string }{{"reg_ts_missing.unaccent", "42704"}, {"a..b", "42602"}, {"a.b.c.d", "42601"}, {"reg_ts_foreign.public.unaccent", "0A000"}} {
		_, err := input.ResolveRegdictionary(context.Background(), tc.input)
		extensionUnitError(t, err, tc.code)
	}
	if id, err := input.ResolveRegconfig(context.Background(), "simple"); err != nil || id != 3748 {
		t.Fatal("genuine builtin configuration", id, err)
	}
	_, err = input.ResolveRegconfig(context.Background(), "reg_ts_missing.simple")
	extensionUnitError(t, err, "42704")
	_, err = input.ResolveRegconfig(context.Background(), "a..b")
	extensionUnitError(t, err, "42602")
	rows := &exec.FinalRows{}
	for _, alias := range []sqltype.ID{sqltype.Regdictionary, sqltype.Regconfig} {
		typ, _ := view.LookupType(alias)
		value, _ := sqltype.RegTSValue(typ, 91000)
		if err := regobj.CaptureTextSearchOutputValue(rows, view, path, value); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := rows.FormatRegdictionary(91000); err != nil || got != `"Reg.TS".unaccent` {
		t.Fatal("captured dictionary spelling", got, err)
	}
	// Mutating the private successor snapshot cannot change returned output.
	next := view
	next = next.WithState(catalog.CloneState(view.State()))
	ns := *next.MustNamespace(namespaces[0])
	ns.ObjectName = "renamed"
	next.State().SetDescriptor(ns.Address(), &ns)
	if got := regobj.TextSearchSpelling(next, path, 91000, sqltype.Regdictionary, false); got != "renamed.unaccent" {
		t.Fatal("fresh spelling", got)
	}
	if got, err := rows.FormatRegdictionary(91000); err != nil || got != `"Reg.TS".unaccent` {
		t.Fatal("retained output changed", got, err)
	}
	quoted := view
	quoted = quoted.WithState(catalog.CloneState(view.State()))
	for i, name := range []string{`Odd".TS`, strings.Repeat("x", 63), "select"} {
		d := *view.MustTSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 91000})
		d.ObjectAddress.ObjectID = ids.OID(92001 + i)
		d.ObjectName = name
		quoted.State().SetDescriptor(d.Address(), &d)
		quoted.State().BindName(catalog.DescriptorNameKey(&d), d.Address())
	}
	quotedCapture := capture
	quotedCapture.Authority.View.Recapture(quoted)
	quotedInput := regobj.NewTextSearchInput(quotedCapture)
	id, err = quotedInput.ResolveRegdictionary(context.Background(), `"Reg.TS".`+strings.Repeat("x", 70))
	if err != nil || id != 92002 {
		t.Fatal("identifier byte truncation", id, err)
	}
	if got := regobj.TextSearchSpelling(quoted, path, 92001, sqltype.Regdictionary, false); got != `"Reg.TS"."Odd"".TS"` {
		t.Fatal("embedded quote/dot output", got)
	}
	if got := regobj.TextSearchSpelling(quoted, path, 92003, sqltype.Regdictionary, false); got != `"Reg.TS"."select"` {
		t.Fatal("keyword output", got)
	}
	id, err = input.ResolveRegdictionary(context.Background(), catalog.QuoteIdentifier(capture.Database)+`."Reg.TS".unaccent`)
	if err != nil || id != 91000 {
		t.Fatal("same database lookup", id, err)
	}
}

func TestRegTSPersistenceRemap(t *testing.T) {
	_, _, view, _, _ := regTSUnitSnapshot(t)
	ctx := context.Background()
	dictionary, _ := view.LookupType(sqltype.Regdictionary)
	config, _ := view.LookupType(sqltype.Regconfig)
	live, _ := sqltype.RegTSValue(dictionary, 91000)
	refs := []ids.ObjectAddress{}
	if err := regobj.WalkTextSearchReferences(view, live, func(a ids.ObjectAddress) error { refs = append(refs, a); return nil }); err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{{ClassID: ids.TSDictionaryClassID, ObjectID: 91000}}) {
		t.Fatal("live reference", refs, err)
	}
	for _, alias := range []*sqltype.Descriptor{dictionary, config} {
		for _, id := range []uint32{0, 4294967295} {
			value, _ := sqltype.RegTSValue(alias, id)
			if err := regobj.WalkTextSearchReferences(view, value, func(ids.ObjectAddress) error { t.Fatal("invented reference"); return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: dictionary.Usage()}, Value: live}
	text, err := regobj.Literal(ctx, view, literal)
	if err != nil || !strings.Contains(text, `"Reg.TS".unaccent`) || strings.Contains(text, "91000") {
		t.Fatal("qualified logical reference", text, err)
	}
	mapped := func(a ids.ObjectAddress) ids.ObjectAddress {
		if a == (ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 91000}) {
			a.ObjectID = 92000
		}
		return a
	}
	for _, value := range []sqltype.Value{live, sqltype.Null(dictionary)} {
		got, err := regobj.RemapTextSearchDatum(value, mapped)
		if err != nil {
			t.Fatal(err)
		}
		if value.IsNull() {
			if !got.IsNull() {
				t.Fatal("NULL changed")
			}
			continue
		}
		id, _ := got.RegTSOID()
		if id != 92000 {
			t.Fatal("dictionary identity remap", id)
		}
	}
	domain, err := sqltype.Domain(95000, "reg_ts_domain", "REG_TS_DOMAIN", dictionary.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(live)
	if err != nil {
		t.Fatal(err)
	}
	composite, err := sqltype.Composite(95001, "reg_ts_row", "REG_TS_ROW", 95002, []sqltype.CompositeMember{{Attribute: 1, Name: "d", Usage: domain.Usage()}, {Attribute: 2, Name: "c", Usage: config.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	configValue, _ := sqltype.RegTSValue(config, 91000)
	row, err := sqltype.CompositeValue(composite, []sqltype.Value{wrapped, configValue})
	if err != nil {
		t.Fatal(err)
	}
	got, err := regobj.RemapTextSearchDatum(row, mapped)
	if err != nil {
		t.Fatal(err)
	}
	di, _ := got.CompositeField(0).RegTSOID()
	ci, _ := got.CompositeField(1).RegTSOID()
	if di != 92000 || ci != 91000 || got.CompositeField(0).Type() != domain {
		t.Fatal("class/domain/composite remap", di, ci)
	}
	remapped, err := stored.RemapExpression(literal, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address = mapped(ref.Address); return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := remapped.(plan.LiteralExpr).Value.RegTSOID()
	original, _ := literal.Value.RegTSOID()
	if id != 92000 || original != 91000 {
		t.Fatal("immutable literal remap", id, original)
	}
	d := view.MustTSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 91000})
	if predecessor, err := (control.Resolver{}).Predecessor(view, d.Address(), ids.ObjectAddress{}); err != nil || predecessor != 0 {
		t.Fatal("released declaration must replay in the installation prefix", predecessor, err)
	}
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	predecessor, err := (control.Resolver{}).Predecessor(view, installation.Address(), ids.ObjectAddress{})
	if err != nil || predecessor == 0 || predecessor != installation.Address().ObjectID || d.Extension != installation.Address() {
		t.Fatal("named extension predecessor", predecessor, err)
	}
	order, err := dump.Order(ctx, dump.Graph{{OID: 1}: {{OID: predecessor}}, {OID: predecessor}: nil}, nil)
	if err != nil || len(order) != 2 || order[0].OID != predecessor {
		t.Fatal("extension-before-consumer order", order, err)
	}
}

func TestRegTSPreparedCapture(t *testing.T) {
	e, op, _ := unaccentSQLInstalled(t)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	b.Query.Auth = &auth
	dictionary, _ := auth.Catalog.LookupType(sqltype.Regdictionary)
	config, _ := auth.Catalog.LookupType(sqltype.Regconfig)
	for _, tc := range []struct {
		typ    *sqltype.Descriptor
		text   string
		offset int
	}{{dictionary, "unaccent", 7}, {config, "8", 7}, {dictionary, "8", 8}} {
		if _, err := b.RegTSConstant(tc.text, tc.typ.Usage(), parser.Position{Offset: tc.offset, Character: tc.offset + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.Prepared.RegtypeConstants) != 3 || len(b.Prepared.RegtypeConstants[0].Operands) != 4 || len(b.Prepared.RegtypeConstants[1].Operands) != 0 || len(b.Prepared.RegtypeConstants[2].Operands) != 0 || b.Prepared.RegtypeConstants[0].Operator == b.Prepared.RegtypeConstants[1].Operator {
		t.Fatal("alias/occurrence/dependency capture", b.Prepared.RegtypeConstants)
	}
	dictionaryOID, err := b.Prepared.RegtypeConstants[0].Value.RegTSOID()
	if err != nil {
		t.Fatal(err)
	}
	selected := auth.Catalog.MustTSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(dictionaryOID)})
	template, ok := auth.Catalog.TSTemplate(selected.Template)
	if !ok {
		t.Fatal("missing selected template")
	}
	for i, address := range []ids.ObjectAddress{selected.Address(), template.Address(), template.Init, template.Lexize} {
		operand := b.Prepared.RegtypeConstants[0].Operands[i]
		if operand.Ref.Address != address || operand.Revision != auth.Catalog.Get(address).Revision() {
			t.Fatal("actual dictionary executable observation", i, operand)
		}
	}
	cell := &regobj.Analysis{Constants: append([]analyze.RegtypeConstant(nil), b.Prepared.RegtypeConstants...)}
	expected := core.Description{Regtypes: cell}
	held := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, &expected)
	held.Query.Auth = &auth
	v, err := held.RegTSConstant("reg_ts_missing", dictionary.Usage(), parser.Position{Offset: 7, Character: 8})
	if err != nil || !sqltype.SameDatum(v, cell.Constants[0].Value) {
		t.Fatal("held literal recaptured", err)
	}
	if !reflect.DeepEqual(cell.Constants, b.Prepared.RegtypeConstants) {
		t.Fatal("held bind mutated shared captures")
	}
}

func TestRegTSCapturedNamespaceACL(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, admin, "reg_ts_acl_actor", true, parser.RoleOptions{})
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 60000}, ObjectRevision: 1, ObjectName: "reg_ts_acl", Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ACL: authorization.ACLValue{Explicit: true}}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(ns); err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, admin, stage)
	install := &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}, Options: []parser.ExtensionOption{{Name: "schema", Value: parser.Identifier{Text: "reg_ts_acl"}}}}
	if err := extensionUnitExecute(t, e, admin, install); err != nil {
		t.Fatal(err)
	}
	op, actorDone := enginetest.RoleOperation(t, e, "reg_ts_acl_actor")
	defer actorDone()
	capture := func() regobj.TextSearchInput {
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		input, err := core.CaptureRegTSInput(auth)
		if err != nil {
			t.Fatal(err)
		}
		return input
	}
	denied := capture()
	_, err := denied.ResolveRegdictionary(context.Background(), "reg_ts_acl.unaccent")
	extensionUnitError(t, err, "42501")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: ns.Owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ns.Address()), acl); err != nil {
		t.Fatal(err)
	}
	granted := capture()
	id, err := granted.ResolveRegdictionary(context.Background(), "reg_ts_acl.unaccent")
	if err != nil || id == 0 {
		t.Fatal("granted coherent authority", id, err)
	}
	_, err = denied.ResolveRegdictionary(context.Background(), "reg_ts_acl.unaccent")
	extensionUnitError(t, err, "42501")
}

func TestRegTSStoredDependencyProviders(t *testing.T) {
	e, _, extension := unaccentSQLInstalled(t)
	view := e.CatalogRead()
	var dictionary *catalog.TSDictionary
	for _, address := range extension.Members {
		if d, ok := view.TSDictionary(address); ok {
			dictionary = d
		}
	}
	if dictionary == nil {
		t.Fatal("real dictionary provider missing")
	}
	ref := e.Catalog.Domain.Ref(view.Epoch(), dictionary.Address())
	consumer := func(ref ids.ObjectRef) *catalog.Relation {
		return &catalog.Relation{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 95000},
			Constraints: catalog.RelationConstraints{
				Check: []catalog.CheckConstraint{{Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: dictionary.Revision()}}}},
			},
			ColumnMeta: catalog.ColumnMetadata{
				Defaults:    map[int32]catalog.ColumnDefault{1: {Dependencies: []ids.ObjectRef{ref}}},
				Generations: map[int32]catalog.ColumnGeneration{1: {Dependencies: []ids.ObjectRef{ref}}},
			},
		}
	}
	for name, validate := range map[string]func(catalog.Read, *catalog.Relation) error{
		"check":      table.ValidateCheckProviders,
		"default":    table.ValidateDefaultProviders,
		"generation": table.ValidateGenerationProviders,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(view, consumer(ref)); err != nil {
				t.Fatal("actual dictionary dependency rejected", err)
			}
			stale := ref
			stale.Epoch = view.Epoch() + 1
			if err := validate(view, consumer(stale)); err == nil {
				t.Fatal("future-epoch dependency admitted")
			}
			missing := ref
			missing.Address.ObjectID = 4294967295
			if err := validate(view, consumer(missing)); err == nil {
				t.Fatal("missing dictionary dependency admitted")
			}
		})
	}
}
