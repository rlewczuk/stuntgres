package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
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
	a := e.state.names[ids.NameKey{ClassID: ExtensionClassID, Name: "unaccent"}]
	return e, op, e.state.descriptors[a].(*extensionDescriptor)
}
func TestExtensionProviderIdentity(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	view := e.CatalogRead()
	seen := map[ids.ObjectAddress]bool{}
	registry := &Engine{}
	registration := registeredExtension{version: "1.1", trusted: true, superuser: true, relocatable: true, provider: stockUnaccent}
	if err := registry.registerExtension("unaccent", registration); err != nil {
		t.Fatal(err)
	}
	if err := registry.registerExtension("unaccent", registration); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	if err := e.registerExtension("unaccent", registration); err == nil {
		t.Fatal("initialized registry mutated")
	}
	for i, signature := range []procedureSignature{unaccentDictionarySignature, unaccentTextSignature, unaccentInitSignature, unaccentLexizeSignature} {
		proc := view.state.descriptors[ext.members[i]].(*procedureDescriptor)
		if proc.signature != signature {
			t.Fatal("declared signature", i, proc.signature)
		}
		vol, strict, result := Stable, true, ids.OID(25)
		if i >= 2 {
			vol, strict, result = Volatile, false, 2281
		}
		if proc.volatility != vol || proc.strict != strict || proc.result != result {
			t.Fatal("procedure properties", i)
		}
	}
	for _, a := range ext.members {
		if seen[a] {
			t.Fatal("duplicate member")
		}
		seen[a] = true
		d := view.state.descriptors[a]
		if err := e.providers[a.ClassID].Validate(view, d); err != nil {
			t.Fatal(err)
		}
		if d.Namespace() != ext.targetNamespace || extensionMembership(d) != ext.address {
			t.Fatal("broken member placement")
		}
		if _, ok := d.(*tsTemplateDescriptor); ok {
			if refs := descriptorReferences(d); len(refs) != 0 {
				t.Fatal("template acquired owner", refs)
			}
		} else {
			owner, _, _ := descriptorACL(d)
			if owner != ext.owner {
				t.Fatal("superuser member ownership")
			}
		}
	}
	key := ids.NameKey{ClassID: ProcedureClassID, Namespace: ext.targetNamespace, Name: "unaccent", Signature: string(unaccentTextSignature)}
	text := view.state.names[key]
	key.Signature = string(unaccentDictionarySignature)
	dictionary := view.state.names[key]
	if text == (ids.ObjectAddress{}) || dictionary == (ids.ObjectAddress{}) || text == dictionary {
		t.Fatal("overloads collapsed")
	}
	edges, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, edge := range edges {
		if edge.referenced == ext.address && edge.deptype == 'e' {
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
	bad.members = slices.Clone(ext.members)
	bad.members[1] = bad.members[0]
	if err := (extensionCatalogProvider{ExtensionClassID}).Validate(view, &bad); err == nil {
		t.Fatal("duplicate members accepted")
	}
}
func TestExtensionExpressionIdentity(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}}
	node, err := b.bindInstalledFunction(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(installedFunctionExpression)
	value, err := node.Eval(&EvalContext{Context: context.Background()})
	text, _ := value.Text()
	if err != nil || text != "Hotel" || node.ResultUsage().Type.ID() != sqltype.Text || plannedVolatility(node) != Stable {
		t.Fatal("typed stable evaluation", text, err)
	}
	if n.procedure.address != ext.members[1] || n.dictionary.address != ext.members[5] || n.template.address != ext.members[4] {
		t.Fatal("captured provider identity")
	}
	refs, err := pgDependExpressionRefs(context.Background(), e.CatalogRead(), node)
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.ref.Address}) {
		t.Fatal("procedure dependency", refs, err)
	}
	n.arg = literalExpression{expressionInfo: expressionInfo{usage: node.ResultUsage()}, value: sqltype.Null(node.ResultUsage().Type)}
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
	if err != nil || text != "Hotel" || retagged.ResultUsage() != node.ResultUsage() || plannedVolatility(retagged) != Stable {
		t.Fatal("retagged provider consumer", text, err)
	}
	rendered, err := (scalarRenderer{}).sql(context.Background(), e.CatalogRead(), node)
	if err != nil || !strings.Contains(rendered, "unaccent") {
		t.Fatal("stored renderer", rendered, err)
	}
	children := 0
	boundExpressionChildren(node, func(BoundExpr) { children++ })
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
			ext := source.descriptors[source.names[ids.NameKey{ClassID: ExtensionClassID, Name: "unaccent"}]].(*extensionDescriptor)
			loose := e.BeginCatalogStage(e.CatalogRead())
			var conflict Descriptor
			if kind == "procedure" || kind == "dictionary_procedure" || kind == "init" || kind == "lexize" {
				index := map[string]int{"procedure": 1, "dictionary_procedure": 0, "init": 2, "lexize": 3}[kind]
				n := *source.descriptors[ext.members[index]].(*procedureDescriptor)
				n.extension = ids.ObjectAddress{}
				n.dictionary = ids.ObjectAddress{}
				conflict = &n
				if err := loose.Create(conflict); err != nil {
					t.Fatal(err)
				}
			} else {
				catalogNS := ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
				for _, i := range []int{2, 3} {
					n := *source.descriptors[ext.members[i]].(*procedureDescriptor)
					n.extension = ids.ObjectAddress{}
					n.namespace = catalogNS
					if err := loose.Create(&n); err != nil {
						t.Fatal(err)
					}
				}
				tmpl := *source.descriptors[ext.members[4]].(*tsTemplateDescriptor)
				tmpl.extension = ids.ObjectAddress{}
				if kind == "dictionary" {
					tmpl.namespace = catalogNS
				}
				if err := loose.Create(&tmpl); err != nil {
					t.Fatal(err)
				}
				conflict = &tmpl
				if kind == "dictionary" {
					dict := *source.descriptors[ext.members[5]].(*tsDictionaryDescriptor)
					dict.extension = ids.ObjectAddress{}
					conflict = &dict
					if err := loose.Create(&dict); err != nil {
						t.Fatal(err)
					}
				}
			}
			extensionUnitPublish(t, e, op, loose)
			before := e.state
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
			if e.state != before || e.state.descriptors[conflict.Address()] != conflict || e.state.names[ids.NameKey{ClassID: ExtensionClassID, Name: "unaccent"}] != (ids.ObjectAddress{}) {
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
			if e.state.names[ids.NameKey{ClassID: ExtensionClassID, Name: "unaccent"}] != (ids.ObjectAddress{}) {
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
				e.state = cloneCatalog(e.state)
				e.state.epoch++
			case "roles":
				e.roles = advanceRoleRevision(e.roles)
			case "database":
				next := *e.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
				next.revision++
				e.state = cloneCatalog(e.state)
				e.state.descriptors[currentDatabaseAddress] = &next
			case "namespace":
				a := ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
				next := *e.state.descriptors[a].(*namespaceDescriptor)
				next.revision++
				e.state = cloneCatalog(e.state)
				e.state.descriptors[a] = &next
			}
			e.mu.Unlock()
			committed := e.state
			failure := scope.Finish(nil)
			if failure == nil {
				t.Fatal("stale installation published")
			}
			if reason == "competing" {
				extensionUnitError(t, failure, "23505")
			}
			if e.state != committed {
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
