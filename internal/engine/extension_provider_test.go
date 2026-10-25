package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func extensionUnitStage(t *testing.T, e *Engine, op *session.Operation) *CatalogStage {
	t.Helper()
	e.Mu.Lock()
	stage, err := stageExtensionLocked(e, &StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return stage
}
func extensionUnitPublish(t *testing.T, e *Engine, op *session.Operation, stage *CatalogStage) {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalogOperation(op, nil, p, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
func extensionUnitInstalled(t *testing.T) (*Engine, *session.Operation, *catalog.Extension) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	roleExecute(t, e, op, "CREATE EXTENSION unaccent")
	a := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	return e, op, e.Catalog.State.MustExtension(a)
}
func TestExtensionProviderIdentity(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	view := e.CatalogRead()
	seen := map[ids.ObjectAddress]bool{}
	// extension.TestRegisterAddsOnlyValidImplementations covers the checks.
	if err := EngineRegisterExtension(e, "other", extension.StockUnaccent); err == nil {
		t.Fatal("initialized registry mutated")
	}
	for i, signature := range []catalog.ProcedureSignature{catalog.UnaccentDictionarySignature, catalog.UnaccentTextSignature, catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature} {
		proc := view.MustProcedure(ext.Members[i])
		if proc.Signature != signature {
			t.Fatal("declared signature", i, proc.Signature)
		}
		vol, strict, result := expr.Stable, true, ids.OID(25)
		if i >= 2 {
			vol, strict, result = expr.Volatile, false, 2281
		}
		if proc.Volatility != vol || proc.Strict != strict || proc.Result != result {
			t.Fatal("procedure properties", i)
		}
		// Every member holds the registered provider's one implementation
		// instance; typed views of another extension's kind stay nil.
		if proc.Implementation != e.Catalog.Extensions["unaccent"] || extension.UnaccentProviderOf(proc) != extension.StockUnaccent || extension.PgcryptoProviderOf(proc) != nil || extension.IntaggProviderOf(proc) != nil {
			t.Fatal("member implementation identity", i)
		}
	}
	if tmpl, _ := view.TSTemplate(ext.Members[4]); tmpl == nil || tmpl.Implementation != extension.StockUnaccent || extension.UnaccentTemplateProviderOf(tmpl) != extension.StockUnaccent {
		t.Fatal("template implementation identity")
	}
	if dict := view.MustTSDictionary(ext.Members[5]); dict.Implementation != extension.StockUnaccent || extension.UnaccentDictionaryProviderOf(dict) != extension.StockUnaccent {
		t.Fatal("dictionary implementation identity")
	}
	if extension.UnaccentProviderOf((&catalog.Procedure{})) != nil || extension.UnaccentDictionaryProviderOf((&catalog.TSDictionary{})) != nil {
		t.Fatal("absent implementation has a typed view")
	}
	for _, a := range ext.Members {
		if seen[a] {
			t.Fatal("duplicate member")
		}
		seen[a] = true
		d := view.Get(a)
		if err := e.Catalog.Providers[a.ClassID].Validate(view, d); err != nil {
			t.Fatal(err)
		}
		if d.Namespace() != ext.TargetNamespace || catalog.ExtensionMembership(d) != ext.Address() {
			t.Fatal("broken member placement")
		}
		if _, ok := d.(*catalog.TSTemplate); ok {
			if refs := descriptorReferences(d); len(refs) != 0 {
				t.Fatal("template acquired owner", refs)
			}
		} else {
			owner, _, _ := access.DescriptorACL(d)
			if owner != ext.Owner {
				t.Fatal("superuser member ownership")
			}
		}
	}
	key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "unaccent", Signature: string(catalog.UnaccentTextSignature)}
	text := view.NameAddress(key)
	key.Signature = string(catalog.UnaccentDictionarySignature)
	dictionary := view.NameAddress(key)
	if text == (ids.ObjectAddress{}) || dictionary == (ids.ObjectAddress{}) || text == dictionary {
		t.Fatal("overloads collapsed")
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), view, EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, edge := range edges {
		if edge.Referenced == ext.Address() && edge.DepType == 'e' {
			n++
			if !seen[edge.Dependent] {
				t.Fatal("foreign member")
			}
		}
	}
	if n != 6 {
		t.Fatal("membership count", n)
	}
	bad := *ext
	bad.Members = slices.Clone(ext.Members)
	bad.Members[1] = bad.Members[0]
	if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(view, &bad); err == nil {
		t.Fatal("duplicate members accepted")
	}
}
func TestExtensionExpressionIdentity(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.InstalledTextExpr)
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	text, _ := value.Text()
	if err != nil || text != "Hotel" || node.ResultUsage().Type.ID() != sqltype.Text || exec.PlannedVolatility(node) != expr.Stable {
		t.Fatal("typed stable evaluation", text, err)
	}
	if n.Procedure.Address() != ext.Members[1] || n.Dictionary.Address() != ext.Members[5] || n.Template.Address() != ext.Members[4] {
		t.Fatal("captured provider identity")
	}
	refs, err := stored.ExpressionDependRefs(context.Background(), e.CatalogRead(), node)
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.Ref.Address}) {
		t.Fatal("procedure dependency", refs, err)
	}
	n.Arg = plan.LiteralExpr{Info: expr.Info{Usage: node.ResultUsage()}, Value: sqltype.Null(node.ResultUsage().Type)}
	value, err = n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
		t.Fatal("strict typed NULL", value, err)
	}
	retagged, _, err := stored.RetagTypes(e.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	value, err = retagged.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	text, _ = value.Text()
	if err != nil || text != "Hotel" || retagged.ResultUsage() != node.ResultUsage() || exec.PlannedVolatility(retagged) != expr.Stable {
		t.Fatal("retagged provider consumer", text, err)
	}
	rendered, err := (stored.ScalarRenderer{}).SQL(context.Background(), e.CatalogRead(), node)
	if err != nil || !strings.Contains(rendered, "unaccent") {
		t.Fatal("stored renderer", rendered, err)
	}
	children := 0
	plan.ExpressionChildren(node, func(expr.Expr) { children++ })
	if children != 1 {
		t.Fatal("child traversal", children)
	}
}

func TestExtensionProviderConflicts(t *testing.T) {
	for _, kind := range []string{"procedure", "dictionary_procedure", "init", "lexize", "template", "dictionary"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			source := extensionUnitStage(t, e, op)
			extensionAddress, _ := source.NameBinding(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
			ext := source.Get(extensionAddress).(*catalog.Extension)
			loose := e.BeginCatalogStage(e.CatalogRead())
			var conflict catalog.Descriptor
			if kind == "procedure" || kind == "dictionary_procedure" || kind == "init" || kind == "lexize" {
				index := map[string]int{"procedure": 1, "dictionary_procedure": 0, "init": 2, "lexize": 3}[kind]
				n := *source.Get(ext.Members[index]).(*catalog.Procedure)
				n.Extension = ids.ObjectAddress{}
				n.Dictionary = ids.ObjectAddress{}
				conflict = &n
				if err := loose.Create(conflict); err != nil {
					t.Fatal(err)
				}
			} else {
				catalogNS := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
				for _, i := range []int{2, 3} {
					n := *source.Get(ext.Members[i]).(*catalog.Procedure)
					n.Extension = ids.ObjectAddress{}
					n.ObjectNamespace = catalogNS
					if err := loose.Create(&n); err != nil {
						t.Fatal(err)
					}
				}
				tmpl := *source.Get(ext.Members[4]).(*catalog.TSTemplate)
				tmpl.Extension = ids.ObjectAddress{}
				if kind == "dictionary" {
					tmpl.ObjectNamespace = catalogNS
				}
				if err := loose.Create(&tmpl); err != nil {
					t.Fatal(err)
				}
				conflict = &tmpl
				if kind == "dictionary" {
					dict := *source.Get(ext.Members[5]).(*catalog.TSDictionary)
					dict.Extension = ids.ObjectAddress{}
					conflict = &dict
					if err := loose.Create(&dict); err != nil {
						t.Fatal(err)
					}
				}
			}
			extensionUnitPublish(t, e, op, loose)
			before := e.Catalog.State
			e.Mu.Lock()
			_, err := stageExtensionLocked(e, &StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}})
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) {
				t.Fatal("collision accepted", err)
			}
			want := "23505"
			if _, ok := conflict.(*catalog.Procedure); ok {
				want = "42723"
			}
			if diagnostic.Code != want || diagnostic.Context == "" {
				t.Fatal(diagnostic)
			}
			if kind == "init" || kind == "lexize" {
				name, args, line := "unaccent_init", "internal", 16
				if kind == "lexize" {
					name, args, line = "unaccent_lexize", "internal,internal,internal,internal", 21
				}
				context := fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS internal\n\tAS '$libdir/unaccent', '%s'\n\tLANGUAGE C PARALLEL SAFE\"\nextension script file \"unaccent--1.1.sql\", near line %d", name, args, name, line)
				if diagnostic.Message != fmt.Sprintf(`function "%s" already exists with same argument types`, name) || diagnostic.Context != context {
					t.Fatal("helper diagnostics", diagnostic)
				}
			}
			if e.Catalog.State != before || e.Catalog.State.Get(conflict.Address()) != conflict || e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}) != (ids.ObjectAddress{}) {
				t.Fatal("partial failed installation")
			}
		})
	}
}

func TestExtensionProviderPublication(t *testing.T) {
	for _, reason := range []string{"cancel", "epoch", "roles", "database", "namespace", "competing"} {
		t.Run(reason, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(&parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}, 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			if e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}) != (ids.ObjectAddress{}) {
				t.Fatal("private installation leaked")
			}
			if reason == "competing" {
				peer, peerDone := roleOperation(t, e, "postgres")
				defer peerDone()
				extensionUnitPublish(t, e, peer, extensionUnitStage(t, e, peer))
			}
			if reason == "cancel" {
				ctx, cancel := context.WithCancel(op.Context())
				op.Ctx = ctx
				cancel()
			}
			e.Mu.Lock()
			switch reason {
			case "epoch":
				e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
				e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
			case "roles":
				e.RoleStore.PublishLocked(e.RoleStore.Published.Advance())
			case "database":
				next := *e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
				next.ObjectRevision++
				e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
				e.Catalog.State.SetDescriptor(catalog.CurrentDatabaseAddress, &next)
			case "namespace":
				a := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
				next := *e.Catalog.State.MustNamespace(a)
				next.ObjectRevision++
				e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
				e.Catalog.State.SetDescriptor(a, &next)
			}
			e.Mu.Unlock()
			committed := e.Catalog.State
			failure := scope.Finish(nil)
			if failure == nil {
				t.Fatal("stale installation published")
			}
			if reason == "competing" {
				extensionUnitError(t, failure, "23505")
			}
			if e.Catalog.State != committed {
				t.Fatal("failed installation changed the committed catalog")
			}
		})
	}
	extensionLiveAuthorityFences(t)
}

func TestExtensionOwnerMetadata(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	encoded, err := extension.OwnerMetadata(e.CatalogRead(), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, recognized, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(encoded), "-- "))
	if err != nil || !recognized || len(record.Members) != 5 || record.Owner != "postgres" {
		t.Fatal("owner record", record, recognized, err)
	}
	for i, slot := range []int{0, 1, 2, 3, 5} {
		if record.Members[i].Slot != slot || record.Members[i].Schema != "public" {
			t.Fatal("owner record lost manifest slot/current schema", record.Members[i])
		}
	}
	for _, payload := range []string{`{}`, `{"name":null}`, `{"name":"unaccent","name":"unaccent"}`, `[]`} {
		_, recognized, err := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString([]byte(payload)))
		if !recognized || err == nil {
			t.Fatal("invalid ownership metadata accepted", payload)
		}
	}
	for _, comment := range []string{"stuntgres-extension-owners-v1 YQ==", extension.OwnersMarker + " !!!!"} {
		if _, _, err := extension.DecodeOwners(comment); err == nil {
			t.Fatal("invalid marker/base64")
		}
	}
	if _, recognized, err := extension.DecodeOwners("ordinary SQL comment"); err != nil || recognized {
		t.Fatal("ordinary comment interpreted")
	}
	extensionOwnershipImportUnits(t)
}

// The registry holds the stock providers, which stage themselves; installed
// members hold that same stock provider, so member identity never depends on
// engine staging code.
func TestExtensionMembersHoldBareProviders(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, tc := range []struct {
		name  string
		stock catalog.ExtensionImplementation
	}{{"amcheck", extension.StockAmcheck}, {"citext", extension.StockCitext}, {"cube", extension.StockCube}, {"earthdistance", extension.StockEarthdistance}, {"lo", extension.StockLo}, {"unaccent", extension.StockUnaccent}, {"intarray", extension.StockIntarray}, {"fuzzystrmatch", extension.StockFuzzystrmatch}, {"intagg", extension.StockIntagg}, {"pgcrypto", extension.StockPgcrypto}, {"pgrowlocks", extension.StockPgrowlocks}, {"uuid-ossp", extension.StockUUIDOssp}} {
		if registered, ok := e.Catalog.Extensions[tc.name].(extension.Installer); !ok || registered != tc.stock {
			t.Fatal("registered implementation is not the stock installer", tc.name)
		}
		e.Mu.Lock()
		stage, err := stageExtensionLocked(e, &StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: tc.name}})
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(tc.name, err)
		}
		extensionUnitPublish(t, e, op, stage)
		view := e.CatalogRead()
		ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: tc.name}))
		if tc.stock == extension.StockLo {
			// Lo carries authority in its immutable domain recipe and reciprocal,
			// extension-owned companion, without implementation-bearing routines.
			if err := tc.stock.ValidateExtension(view, ext); err != nil {
				t.Fatal(err)
			}
			for _, a := range ext.Members {
				d, ok := view.Get(a).(*catalog.Type)
				if !ok || d.Extension != ext.Address() {
					t.Fatal("invalid domain-only member", a)
				}
			}
			continue
		}
		if tc.stock == extension.StockEarthdistance {
			if err := tc.stock.ValidateExtension(view, ext); err != nil {
				t.Fatal(err)
			}
		}
		members := 0
		for _, a := range ext.Members {
			var implementation catalog.ExtensionImplementation
			switch d := view.Get(a).(type) {
			case *catalog.Procedure:
				implementation = d.Implementation
			case *catalog.TSTemplate:
				implementation = d.Implementation
			case *catalog.TSDictionary:
				implementation = d.Implementation
			default:
				continue
			}
			members++
			if implementation != tc.stock {
				t.Fatal("member does not hold the bare stock provider", tc.name, a)
			}
		}
		if members == 0 {
			t.Fatal("no implementation-bearing members", tc.name)
		}
	}
}

func TestExtensionProviderRelabelIdentity(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	view := e.CatalogRead()
	for slot, want := range []string{"unaccent", "unaccent", "unaccent_init", "unaccent_lexize"} {
		t.Run(fmt.Sprint(slot), func(t *testing.T) {
			original := view.MustProcedure(ext.Members[slot])
			if symbol, err := extension.ProcedureSymbol(view, original); err != nil || symbol != want {
				t.Fatal("invalid relabel baseline", symbol, err)
			}
			renamed := *original
			renamed.ObjectName = "relabeled_" + want
			renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
			state := catalog.CloneState(view.State())
			state.UnbindName(catalog.DescriptorNameKey(original))
			state.SetDescriptor(renamed.Address(), &renamed)
			state.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
			current := e.Catalog.Read(state)
			if symbol, err := extension.ProcedureSymbol(current, &renamed); err != nil || symbol != want {
				t.Fatal(symbol, err)
			}
			if err := (extension.CatalogProvider{ids.ProcedureClassID}).Validate(current, &renamed); err != nil {
				t.Fatal("relabel rejected", err)
			}
			copy := *ext
			copy.Members = slices.Clone(ext.Members)
			if err := extension.StockUnaccent.ValidateExtension(current, &copy); err != nil {
				t.Fatal("equivalent detached manifest rejected", err)
			}
			copy.Members[0], copy.Members[1] = copy.Members[1], copy.Members[0]
			if err := extension.StockUnaccent.ValidateExtension(current, &copy); err == nil {
				t.Fatal("reordered manifest accepted")
			}
			detached := renamed
			detached.Extension, detached.Dictionary = ids.ObjectAddress{}, ids.ObjectAddress{}
			state.SetDescriptor(detached.Address(), &detached)
			if err := (extension.CatalogProvider{ids.ProcedureClassID}).Validate(current, &detached); err != nil {
				t.Fatal("incumbent detached metadata rejected", err)
			}
			if _, err := extension.ProcedureSymbol(current, &detached); err == nil {
				t.Fatal("detached metadata acquired installed capability")
			}
			detached.Strict = !detached.Strict
			if err := (extension.CatalogProvider{ids.ProcedureClassID}).Validate(current, &detached); err == nil {
				t.Fatal("forged detached declaration accepted")
			}
			if view.Get(original.Address()) != original || ext.TargetNamespace != original.Namespace() {
				t.Fatal("source installation mutated")
			}
		})
	}
}
