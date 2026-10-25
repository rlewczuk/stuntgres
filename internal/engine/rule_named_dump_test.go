package engine

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// This supplements the public dual-mode scenarios with real analyzed provider
// metadata and the production export -> parse -> canonical reanalysis path.
func TestRuleNamedConstraintDumpReanalysis(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, cte := range []bool{false, true} {
			for _, reuse := range []bool{false, true} {
				t.Run(fmt.Sprintf("update=%v/cte=%v/reuse=%v", update, cte, reuse), func(t *testing.T) {
					e := New()
					s := sessionStart(t, e, nil)
					for _, q := range []string{
						"CREATE TABLE src(id INT, other INT)",
						"CREATE TABLE sink(id INT CONSTRAINT sink_key UNIQUE, other INT CONSTRAINT other_key UNIQUE)",
						"CREATE TABLE input(id INT, other INT)",
						"CREATE TABLE log(id INT)",
					} {
						sessionSQL(t, s, q)
					}
					conflictSQL := " ON CONFLICT ON CONSTRAINT sink_key DO NOTHING"
					if update {
						conflictSQL = " ON CONFLICT ON CONSTRAINT sink_key DO UPDATE SET other=EXCLUDED.other"
					}
					actionSQL := "INSERT INTO sink VALUES(NEW.id,NEW.other)" + conflictSQL
					if cte {
						actionSQL = "WITH moved AS (INSERT INTO sink SELECT id,other FROM input" + conflictSQL + " RETURNING id) INSERT INTO log SELECT NEW.id FROM moved"
					}
					sessionSQL(t, s, "CREATE RULE r AS ON INSERT TO src DO ALSO "+actionSQL)
					before := e.CatalogRead()
					srcRef := catalogRef(t, e, before, "src")
					sinkRef := catalogRef(t, e, before, "sink")
					inputRef := catalogRef(t, e, before, "input")
					parentBefore := before.MustRelation(sinkRef.Address)
					intended, other := parentBefore.Constraints.Unique[0], parentBefore.Constraints.Unique[1]
					if intended.OID == 0 || other.OID == 0 || intended.OID == other.OID || intended.Index == other.Index {
						t.Fatal("fixture lacks two distinct real named constraint providers")
					}
					persisted := stored.RulesOf(before.MustRelation(srcRef.Address))[0]
					conflict := func(r stored.Rule) *analyze.InsertConflict {
						t.Helper()
						b := r.Actions()[0].Binding()
						if cte {
							if len(b.CTEs) != 1 || b.CTEs[0].Mutation == nil {
								t.Fatal("analyzed action lost its modifying CTE")
							}
							b = *cteMutationOf(b.CTEs[0])
						}
						if b.Insert.Conflict == nil {
							t.Fatal("analyzed action lost its named conflict binding")
						}
						return b.Insert.Conflict
					}
					original := conflict(persisted)
					frozen := *original
					intendedAddress := ids.ObjectAddress{ClassID: 2606, ObjectID: intended.OID}
					if original.Constraint != intendedAddress || original.NamedIndex != intended.Index {
						t.Fatal("creation did not bind the actual named constraint and physical id provider")
					}
					assertSources := func(r stored.Rule) {
						t.Helper()
						if !cte {
							return
						}
						action := r.Actions()[0]
						body := action.Binding().CTEs[0]
						presentation := action.ModifierPresentations()[body.Statement]
						project := cteMutationOf(body).Query.Root.(*plan.ProjectNode)
						outer := action.Binding().Query.Root.(*plan.ProjectNode)
						if project.Frame == outer.Frame {
							t.Fatal("modifier and consumer share a capture frame")
						}
						for i, expr := range project.Expressions {
							column, ok := expr.(plan.ColumnExpr)
							if !ok || column.Scope == analyze.RulePseudoScope || column.Frame != project.Frame ||
								column.Origin.Address.ClassID != inputRef.Address.ClassID ||
								column.Origin.Address.ObjectID != inputRef.Address.ObjectID || column.Origin.Address.SubID != int32(i+1) {
								t.Fatal("modifier projection lost its own input attribute")
							}
							path := analyze.ViewLiteralPath{Query: project.Frame, Clause: analyze.ViewTargetLiteral, Target: i}
							captured, ok := presentation.Columns()[path]
							if !ok || captured.RuleSide != 0 || captured.Frame != project.Frame {
								t.Fatal("modifier source presentation lost its own capture")
							}
						}
						path := analyze.ViewLiteralPath{Query: outer.Frame, Clause: analyze.ViewTargetLiteral}
						if action.Presentation().Columns()[path].RuleSide != 2 {
							t.Fatal("consumer lost its separate NEW recipe")
						}
					}
					assertSources(persisted)
					sessionSQL(t, s, "ALTER TABLE sink RENAME CONSTRAINT sink_key TO renamed_key")
					if reuse {
						sessionSQL(t, s, "ALTER TABLE sink RENAME CONSTRAINT other_key TO sink_key")
					}
					current := e.CatalogRead()
					parent := current.MustRelation(sinkRef.Address)
					if parent.Constraints.Unique[0].OID != intended.OID || parent.Constraints.Unique[0].Index != intended.Index || parent.Constraints.Unique[0].Name != "renamed_key" {
						t.Fatal("rename changed provider identity or failed to publish current metadata")
					}
					if parentBefore.Constraints.Unique[0].Name != "sink_key" {
						t.Fatal("rename mutated the borrowed parent snapshot")
					}
					e.Mu.Lock()
					roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, current)
					e.Mu.Unlock()
					groups, err := stored.RuleDumpGroups(context.Background(), current, roles)
					if err != nil {
						t.Fatal(err)
					}
					if len(groups) != 1 {
						t.Fatalf("exported rule groups=%d want 1", len(groups))
					}
					parsed, err := parser.Parse(groups[0].SQL)
					if err != nil || len(parsed) != 1 {
						t.Fatalf("actual exported declaration cannot reparse: %v", err)
					}
					if _, ok := parsed[0].(*parser.CreateRule); !ok {
						t.Fatalf("exported declaration is %T", parsed[0])
					}
					sessionSQL(t, s, "DROP RULE r ON src")
					op, err := s.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					_, err = e.Execute(op, parsed[0], 0, nil)
					op.Done()
					if err != nil {
						t.Fatalf("exported rule reanalysis: %v\n%s", err, groups[0].SQL)
					}
					reboundView := e.CatalogRead()
					rebound := stored.RulesOf(reboundView.MustRelation(srcRef.Address))[0]
					assertSources(rebound)
					if conflict(rebound).Constraint != intendedAddress {
						t.Fatal("reanalysis did not capture the actual class2606 renamed constraint identity")
					}
					provider := conflict(rebound).NamedIndex
					if provider != intended.Index || provider == other.Index {
						t.Fatalf("reanalysis retargeted provider: got=%v intended=%v other=%v", provider, intended.Index, other.Index)
					}
					// Match actual current parent metadata by provider identity, not
					// creation spelling or a source-text assertion on the export.
					var oid ids.OID
					for _, constraint := range parent.Constraints.Unique {
						if constraint.Index == provider {
							oid = constraint.OID
						}
					}
					if oid != intended.OID {
						t.Fatal("reanalysis did not preserve the actual renamed constraint identity")
					}
					if !reflect.DeepEqual(*original, frozen) {
						t.Fatal("export or reanalysis mutated the published conflict template")
					}
					// Exercise actual native import remapping, not a hand-built
					// identity carrier or a mock mapper.
					var dump bytes.Buffer
					if err := e.Dump(context.Background(), &dump); err != nil {
						t.Fatal(err)
					}
					if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					imported := e.CatalogRead()
					importedSrc := catalogRef(t, e, imported, "src")
					importedSink := catalogRef(t, e, imported, "sink")
					importedParent := imported.MustRelation(importedSink.Address)
					importedRule := stored.RulesOf(imported.MustRelation(importedSrc.Address))[0]
					importedConflict := conflict(importedRule)
					var importedProvider catalog.UniqueConstraint
					for _, definition := range importedParent.Constraints.Unique {
						if definition.Name == "renamed_key" {
							importedProvider = definition
						}
					}
					if importedProvider.OID == 0 ||
						importedConflict.Constraint != (ids.ObjectAddress{ClassID: 2606, ObjectID: importedProvider.OID}) ||
						importedConflict.NamedIndex != importedProvider.Index ||
						len(importedProvider.Attributes) != 1 || importedProvider.Attributes[0] != importedParent.Attributes[0] {
						t.Fatal("actual native import lost the named id constraint/backing relationship")
					}
					for _, definition := range importedParent.Constraints.Unique {
						if definition.Name == "sink_key" && (definition.OID == importedConflict.Constraint.ObjectID || definition.Index == importedConflict.NamedIndex) {
							t.Fatal("actual native import retargeted the reused other-column provider")
						}
					}
					if !reflect.DeepEqual(*original, frozen) {
						t.Fatal("actual native import mutated the borrowed conflict template")
					}
				})
			}
		}
	}
}
