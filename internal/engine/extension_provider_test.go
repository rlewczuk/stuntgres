package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func extensionUnitStage(t *testing.T, e *Engine, op *Operation) *CatalogStage {
	t.Helper()
	e.mu.Lock()
	stage, err := e.stageExtensionLocked(&StatementScope{op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return stage
}
func extensionUnitPublish(t *testing.T, e *Engine, op *Operation, stage *CatalogStage) {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.publishCatalogOperation(op, nil, p, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
func extensionUnitInstalled(t *testing.T) (*Engine, *Operation, *extensionDescriptor) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	stage := extensionUnitStage(t, e, op)
	extensionUnitPublish(t, e, op, stage)
	a := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	return e, op, e.catalog.state.MustExtension(a)
}
func TestExtensionProviderIdentity(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	view := e.CatalogRead()
	seen := map[ids.ObjectAddress]bool{}
	registry := &Engine{}
	if err := registry.registerExtension("unaccent", stockUnaccent); err != nil {
		t.Fatal(err)
	}
	if err := registry.registerExtension("unaccent", stockUnaccent); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	if err := registry.registerExtension("other", nil); err == nil {
		t.Fatal("nil implementation accepted")
	}
	if err := registry.registerExtension("other", &unaccentProvider{}); err == nil {
		t.Fatal("implementation without a version accepted")
	}
	if err := e.registerExtension("unaccent", stockUnaccent); err == nil {
		t.Fatal("initialized registry mutated")
	}
	for i, signature := range []procedureSignature{unaccentDictionarySignature, unaccentTextSignature, unaccentInitSignature, unaccentLexizeSignature} {
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
		// The registry and every member hold the one implementation instance;
		// typed views of another extension's kind stay nil.
		if proc.Implementation != e.catalog.Extensions["unaccent"] || proc.unaccentProvider() != stockUnaccent || proc.pgcryptoProvider() != nil || proc.intaggProvider() != nil {
			t.Fatal("member implementation identity", i)
		}
	}
	if tmpl, _ := view.TSTemplate(ext.Members[4]); tmpl == nil || tmpl.Implementation != stockUnaccent || tmpl.unaccentProvider() != stockUnaccent {
		t.Fatal("template implementation identity")
	}
	if dict := view.MustTSDictionary(ext.Members[5]); dict.Implementation != stockUnaccent || dict.unaccentProvider() != stockUnaccent {
		t.Fatal("dictionary implementation identity")
	}
	if (&procedureDescriptor{}).unaccentProvider() != nil || (&tsDictionaryDescriptor{}).unaccentProvider() != nil {
		t.Fatal("absent implementation has a typed view")
	}
	for _, a := range ext.Members {
		if seen[a] {
			t.Fatal("duplicate member")
		}
		seen[a] = true
		d := view.Get(a)
		if err := e.catalog.Providers[a.ClassID].Validate(view, d); err != nil {
			t.Fatal(err)
		}
		if d.Namespace() != ext.TargetNamespace || extensionMembership(d) != ext.Address() {
			t.Fatal("broken member placement")
		}
		if _, ok := d.(*tsTemplateDescriptor); ok {
			if refs := descriptorReferences(d); len(refs) != 0 {
				t.Fatal("template acquired owner", refs)
			}
		} else {
			owner, _, _ := descriptorACL(d)
			if owner != ext.Owner {
				t.Fatal("superuser member ownership")
			}
		}
	}
	key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "unaccent", Signature: string(unaccentTextSignature)}
	text := view.NameAddress(key)
	key.Signature = string(unaccentDictionarySignature)
	dictionary := view.NameAddress(key)
	if text == (ids.ObjectAddress{}) || dictionary == (ids.ObjectAddress{}) || text == dictionary {
		t.Fatal("overloads collapsed")
	}
	edges, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, edge := range edges {
		if edge.referenced == ext.Address() && edge.deptype == 'e' {
			n++
			if !seen[edge.dependent] {
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
	if err := (extensionCatalogProvider{ids.ExtensionClassID}).Validate(view, &bad); err == nil {
		t.Fatal("duplicate members accepted")
	}
}
func TestExtensionExpressionIdentity(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}}
	node, err := b.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(installedFunctionExpression)
	value, err := node.Eval(&EvalContext{Context: context.Background()})
	text, _ := value.Text()
	if err != nil || text != "Hotel" || node.ResultUsage().Type.ID() != sqltype.Text || plannedVolatility(node) != expr.Stable {
		t.Fatal("typed stable evaluation", text, err)
	}
	if n.procedure.Address() != ext.Members[1] || n.dictionary.Address() != ext.Members[5] || n.template.Address() != ext.Members[4] {
		t.Fatal("captured provider identity")
	}
	refs, err := pgDependExpressionRefs(context.Background(), e.CatalogRead(), node)
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.ref.Address}) {
		t.Fatal("procedure dependency", refs, err)
	}
	n.Arg = literalExpression{Info: expr.Info{Usage: node.ResultUsage()}, value: sqltype.Null(node.ResultUsage().Type)}
	value, err = n.Eval(&EvalContext{Context: context.Background()})
	if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
		t.Fatal("strict typed NULL", value, err)
	}
	retagged, _, err := retagBoundTypes(e.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	value, err = retagged.Eval(&EvalContext{Context: context.Background()})
	text, _ = value.Text()
	if err != nil || text != "Hotel" || retagged.ResultUsage() != node.ResultUsage() || plannedVolatility(retagged) != expr.Stable {
		t.Fatal("retagged provider consumer", text, err)
	}
	rendered, err := (scalarRenderer{}).sql(context.Background(), e.CatalogRead(), node)
	if err != nil || !strings.Contains(rendered, "unaccent") {
		t.Fatal("stored renderer", rendered, err)
	}
	children := 0
	boundExpressionChildren(node, func(expr.Expr) { children++ })
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
			ext := source.descriptors[source.names[ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}]].(*extensionDescriptor)
			loose := e.BeginCatalogStage(e.CatalogRead())
			var conflict Descriptor
			if kind == "procedure" || kind == "dictionary_procedure" || kind == "init" || kind == "lexize" {
				index := map[string]int{"procedure": 1, "dictionary_procedure": 0, "init": 2, "lexize": 3}[kind]
				n := *source.descriptors[ext.Members[index]].(*procedureDescriptor)
				n.Extension = ids.ObjectAddress{}
				n.Dictionary = ids.ObjectAddress{}
				conflict = &n
				if err := loose.Create(conflict); err != nil {
					t.Fatal(err)
				}
			} else {
				catalogNS := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
				for _, i := range []int{2, 3} {
					n := *source.descriptors[ext.Members[i]].(*procedureDescriptor)
					n.Extension = ids.ObjectAddress{}
					n.ObjectNamespace = catalogNS
					if err := loose.Create(&n); err != nil {
						t.Fatal(err)
					}
				}
				tmpl := *source.descriptors[ext.Members[4]].(*tsTemplateDescriptor)
				tmpl.Extension = ids.ObjectAddress{}
				if kind == "dictionary" {
					tmpl.ObjectNamespace = catalogNS
				}
				if err := loose.Create(&tmpl); err != nil {
					t.Fatal(err)
				}
				conflict = &tmpl
				if kind == "dictionary" {
					dict := *source.descriptors[ext.Members[5]].(*tsDictionaryDescriptor)
					dict.Extension = ids.ObjectAddress{}
					conflict = &dict
					if err := loose.Create(&dict); err != nil {
						t.Fatal(err)
					}
				}
			}
			extensionUnitPublish(t, e, op, loose)
			before := e.catalog.state
			e.mu.Lock()
			_, err := e.stageExtensionLocked(&StatementScope{op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}})
			e.mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) {
				t.Fatal("collision accepted", err)
			}
			want := "23505"
			if _, ok := conflict.(*procedureDescriptor); ok {
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
			if e.catalog.state != before || e.catalog.state.Get(conflict.Address()) != conflict || e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}) != (ids.ObjectAddress{}) {
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
			if e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}) != (ids.ObjectAddress{}) {
				t.Fatal("private installation leaked")
			}
			if reason == "competing" {
				peer, peerDone := roleOperation(t, e, "postgres")
				defer peerDone()
				extensionUnitPublish(t, e, peer, extensionUnitStage(t, e, peer))
			}
			if reason == "cancel" {
				ctx, cancel := context.WithCancel(op.Context())
				op.ctx = ctx
				cancel()
			}
			e.mu.Lock()
			switch reason {
			case "epoch":
				e.catalog.state = cloneCatalog(e.catalog.state)
				e.catalog.state.SetEpoch(e.catalog.state.Epoch() + 1)
			case "roles":
				e.roles = advanceRoleRevision(e.roles)
			case "database":
				next := *e.catalog.state.MustDatabase(currentDatabaseAddress)
				next.ObjectRevision++
				e.catalog.state = cloneCatalog(e.catalog.state)
				e.catalog.state.setDescriptor(currentDatabaseAddress, &next)
			case "namespace":
				a := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
				next := *e.catalog.state.MustNamespace(a)
				next.ObjectRevision++
				e.catalog.state = cloneCatalog(e.catalog.state)
				e.catalog.state.setDescriptor(a, &next)
			}
			e.mu.Unlock()
			committed := e.catalog.state
			failure := scope.Finish(nil)
			if failure == nil {
				t.Fatal("stale installation published")
			}
			if reason == "competing" {
				extensionUnitError(t, failure, "23505")
			}
			if e.catalog.state != committed {
				t.Fatal("failed installation changed the committed catalog")
			}
		})
	}
	extensionLiveAuthorityFences(t)
}

func TestExtensionOwnerMetadata(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	encoded, err := extensionOwnerMetadata(e.CatalogRead(), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, recognized, err := decodeExtensionOwners(strings.TrimPrefix(strings.TrimSpace(encoded), "-- "))
	if err != nil || !recognized || len(record.Members) != 5 || record.Owner != "postgres" {
		t.Fatal("owner record", record, recognized, err)
	}
	for _, payload := range []string{`{}`, `{"name":null}`, `{"name":"unaccent","name":"unaccent"}`, `[]`} {
		_, recognized, err := decodeExtensionOwners(extensionOwnersMarker + " " + base64.StdEncoding.EncodeToString([]byte(payload)))
		if !recognized || err == nil {
			t.Fatal("invalid ownership metadata accepted", payload)
		}
	}
	for _, comment := range []string{"stuntgres-extension-owners-v2 YQ==", extensionOwnersMarker + " !!!!"} {
		if _, _, err := decodeExtensionOwners(comment); err == nil {
			t.Fatal("invalid marker/base64")
		}
	}
	if _, recognized, err := decodeExtensionOwners("ordinary SQL comment"); err != nil || recognized {
		t.Fatal("ordinary comment interpreted")
	}
	extensionOwnershipImportUnits(t)
}
