package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"testing"
)

// The SQL below constructs real analyzed templates and index providers. The
// invariant under test is the production export -> parse -> rule-analysis path,
// not public SQL results or a hand-built imitation of conflictKey.
func TestRuleInferenceDumpReanalysis(t *testing.T) {
	for _, expression := range []bool{false, true} {
		for _, cte := range []bool{false, true} {
			t.Run(fmt.Sprintf("expression=%v/cte=%v", expression, cte), func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE src(v TEXT)", "CREATE TABLE sink(v TEXT)", "CREATE TABLE log(v TEXT)", "CREATE TABLE input(v TEXT)"} {
					sessionSQL(t, s, q)
				}
				key := "v"
				if expression {
					key = "(lower(v))"
				}
				decoration := ` COLLATE pg_catalog."C" pg_catalog.text_pattern_ops`
				// Column indexes are supported providers; an expression inference
				// key must retain its expression and fail to match either one.
				sessionSQL(t, s, "CREATE UNIQUE INDEX matching ON sink(v"+decoration+")")
				sessionSQL(t, s, "CREATE UNIQUE INDEX ordinary ON sink(v)")
				action := "INSERT INTO sink VALUES(NEW.v) ON CONFLICT(" + key + decoration + ") DO NOTHING"
				if cte {
					action = "WITH moved AS (INSERT INTO sink SELECT v FROM input ON CONFLICT(" + key + decoration + ") DO NOTHING RETURNING v) INSERT INTO log SELECT NEW.v FROM moved"
				}
				sessionSQL(t, s, "CREATE RULE r AS ON INSERT TO src DO "+action)
				sessionSQL(t, s, "ALTER TABLE sink RENAME COLUMN v TO key")
				view := e.CatalogRead()
				srcRef := catalogRef(t, e, view, "src")
				sinkRef := catalogRef(t, e, view, "sink")
				inputRef := catalogRef(t, e, view, "input")
				matchingRef := catalogRef(t, e, view, "matching")
				ordinaryRef := catalogRef(t, e, view, "ordinary")
				stored := rulesOf(view.MustRelation(srcRef.Address))[0]
				assertSourceOwnership := func(r ruleDefinition) {
					t.Helper()
					if !cte {
						return
					}
					action := r.actions[0]
					body := action.binding.CTEs[0]
					presentation := action.modifierPresentations[body.Statement]
					project := cteMutationOf(body).Query.Root.(*plan.ProjectNode)
					outer := action.binding.Query.Root.(*plan.ProjectNode)
					if project.Frame == outer.Frame {
						t.Fatal("modifier and consumer share a SELECT capture frame")
					}
					source, ok := project.Expressions[0].(plan.ColumnExpr)
					if !ok || source.Scope == rulePseudoScope || source.Frame != project.Frame ||
						source.Origin.Address.ClassID != inputRef.Address.ClassID ||
						source.Origin.Address.ObjectID != inputRef.Address.ObjectID || source.Origin.Address.SubID != 1 {
						t.Fatal("modifier's analyzed projection no longer belongs to input.v")
					}
					path := viewLiteralPath{Query: project.Frame, Clause: analyze.ViewTargetLiteral}
					column, ok := presentation.columns[path]
					if !ok || column.RuleSide != 0 || column.Frame != project.Frame {
						t.Fatal("modifier input column was replaced by the consumer's NEW recipe")
					}
					path.Query = outer.Frame
					if action.presentation.columns[path].RuleSide != 2 {
						t.Fatal("consumer lost its independent NEW recipe")
					}
				}
				assertSourceOwnership(stored)
				conflict := func(r ruleDefinition) *boundInsertConflict {
					t.Helper()
					b := r.actions[0].binding
					if cte {
						if len(b.CTEs) != 1 || b.CTEs[0].Mutation == nil {
							t.Fatal("analyzed action lost its modifying CTE")
						}
						b = *cteMutationOf(b.CTEs[0])
					}
					if b.Insert.Conflict == nil || len(insertConflictOf(b.Insert.Conflict).keys) != 1 {
						t.Fatal("analyzed modifier lost its inference key")
					}
					return insertConflictOf(b.Insert.Conflict)
				}
				original := conflict(stored)
				assertKey := func(c *boundInsertConflict, catalog catalog.Read) {
					t.Helper()
					k := c.keys[0]
					if k.collation != 950 || k.class.Name != "text_pattern_ops" {
						t.Fatalf("retained decorations: collation=%d class=%q", k.collation, k.class.Name)
					}
					if expression {
						if k.attribute != 0 || k.expression == nil {
							t.Fatal("expression inference became attribute inference")
						}
					} else if k.attribute != 1 || k.expression != nil {
						t.Fatal("stable attribute inference changed")
					}
					parent := catalog.MustRelation(sinkRef.Address)
					for _, test := range []struct {
						ref  ids.ObjectRef
						want bool
					}{{matchingRef, !expression}, {ordinaryRef, false}} {
						index := catalog.MustRelation(test.ref.Address).SQLIndex
						got, err := c.matches(parent, index)
						if err != nil || got != test.want {
							t.Fatalf("canonical inference match=%v want=%v error=%v", got, test.want, err)
						}
					}
				}
				assertKey(original, view)
				e.mu.Lock()
				roles := e.roles.read(e.catalog.owner(), view, e.roles.published)
				e.mu.Unlock()
				groups, err := ruleDumpGroups(context.Background(), view, roles)
				if err != nil {
					t.Fatal(err)
				}
				if len(groups) != 1 {
					t.Fatalf("exported rule groups=%d want 1", len(groups))
				}
				parsed, err := parser.Parse(groups[0].sql)
				if err != nil || len(parsed) != 1 {
					t.Fatalf("actual exported declaration cannot reparse: %v", err)
				}
				if _, ok := parsed[0].(*parser.CreateRule); !ok {
					t.Fatalf("exported declaration is %T", parsed[0])
				}
				// Reinstall the actual declaration through canonical rule analysis.
				sessionSQL(t, s, "DROP RULE r ON src")
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_, err = e.Execute(op, parsed[0], 0, nil)
				op.Done()
				if err != nil {
					t.Fatalf("exported rule reanalysis: %v\n%s", err, groups[0].sql)
				}
				reboundView := e.CatalogRead()
				rebound := rulesOf(reboundView.MustRelation(srcRef.Address))[0]
				assertSourceOwnership(rebound)
				assertKey(conflict(rebound), reboundView)
				assertKey(original, view)
			})
		}
	}
}
