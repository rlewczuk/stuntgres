package enginetests_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestRoutineProgramMixedPurposeNormalization(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE FUNCTION normalization_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)$$")
	enginetest.SessionSQL(t, s, "CREATE AGGREGATE normalization_sum(integer)(SFUNC=normalization_step,STYPE=integer,INITCOND='0')")
	enginetest.SessionSQL(t, s, "CREATE TABLE normalization_input(v integer)")
	enginetest.SessionSQL(t, s, "CREATE FUNCTION normalization_call() RETURNS integer BEGIN ATOMIC SELECT normalization_step(1,2); END")
	enginetest.SessionSQL(t, s, "CREATE FUNCTION normalization_literal() RETURNS regprocedure BEGIN ATOMIC SELECT 'normalization_step(integer,integer)'::regprocedure; END")
	enginetest.SessionSQL(t, s, "CREATE FUNCTION normalization_mixed() RETURNS regprocedure BEGIN ATOMIC SELECT CASE WHEN normalization_step(1,2)=3 THEN 'normalization_step(integer,integer)'::regprocedure ELSE NULL::regprocedure END; END")
	enginetest.SessionSQL(t, s, "CREATE FUNCTION normalization_support() RETURNS integer BEGIN ATOMIC SELECT normalization_sum(v) FROM normalization_input; END")
	read := e.CatalogRead()
	program := func(name string) *stored.RoutineProgram {
		t.Helper()
		_, d := preparedFoldStoredDescription(t, e, s, "SELECT '"+name+"()'::regprocedure")
		if d.Regtypes == nil || len(d.Regtypes.Constants) != 1 {
			t.Fatal("missing fixture identity", name)
		}
		oid, err := d.Regtypes.Constants[0].Value.ProcedureOID()
		if err != nil {
			t.Fatal(err)
		}
		callee, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(oid)})
		if !ok {
			t.Fatal("missing fixture procedure", name)
		}
		p, err := stored.RoutineProgramOf(callee)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	call, literal, mixed, support := program("normalization_call"), program("normalization_literal"), program("normalization_mixed"), program("normalization_support")
	var definition catalog.ExpressionDependency
	for _, d := range literal.Dependencies() {
		if d.Ref.Address.ClassID == ids.ProcedureClassID && d.Purpose == catalog.OperandDependency {
			definition = d
			definition.Purpose = catalog.DefinitionDependency
			break
		}
	}
	if definition.Revision == 0 {
		t.Fatal("missing actual callable capture")
	}
	operand := definition
	operand.Purpose = catalog.OperandDependency
	supportStamp := definition
	supportStamp.Purpose = catalog.AggregateSupportDependency
	contains := func(dependencies []catalog.ExpressionDependency, want catalog.ExpressionDependency) bool {
		return slices.Contains(dependencies, want)
	}
	if !contains(call.Dependencies(), definition) || !contains(literal.Dependencies(), operand) || !contains(support.Dependencies(), supportStamp) {
		t.Fatal("fixture did not independently capture callable, literal and selected support")
	}
	reversed := mixed.Action(0).With(func(parts *stored.RuleActionParts) {
		parts.Binding.Dependencies = slices.Clone(parts.Binding.Dependencies)
		slices.Reverse(parts.Binding.Dependencies)
		parts.Presentation = parts.Presentation.With(func(v *stored.ViewParts) {
			v.Dependencies = slices.Clone(v.Dependencies)
			slices.Reverse(v.Dependencies)
		})
	})
	for _, tc := range []struct {
		name string
		actions []stored.RuleAction
	}{
		{"same-action", []stored.RuleAction{mixed.Action(0)}},
		{"same-action-reversed", []stored.RuleAction{reversed}},
		{"definition-first", []stored.RuleAction{call.Action(0), literal.Action(0)}},
		{"operand-first", []stored.RuleAction{literal.Action(0), call.Action(0)}},
		{"support-first", []stored.RuleAction{support.Action(0), literal.Action(0), call.Action(0)}},
		{"support-last", []stored.RuleAction{call.Action(0), literal.Action(0), support.Action(0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := stored.NewRoutineProgram(tc.actions)
			before := slices.Clone(p.Dependencies())
			originalBindings := make([][]catalog.ExpressionDependency, len(tc.actions))
			originalPresentations := make([][]catalog.ExpressionDependency, len(tc.actions))
			for i, action := range tc.actions {
				originalBindings[i] = slices.Clone(action.Binding().Dependencies)
				originalPresentations[i] = slices.Clone(action.Presentation().Dependencies())
				if err := p.ValidateAction(read, i); err != nil {
					t.Fatal("exact original action rejected", i, err)
				}
			}
			if !contains(before, definition) || contains(before, operand) {
				t.Fatal("same-stamp definition dominance", before)
			}
			if contains(before, supportStamp) != (len(tc.actions) == 3) {
				t.Fatal("support lane collapsed or invented", before)
			}
			// Exercise both actual production boundaries even when one fails.
			if err := p.Validate(read); err != nil {
				t.Error("constructor-normalized program rejected by Validate", err)
			}
			rebound, err := p.Rebind(read, nil, stored.NewRoutineRemap(read, nil, nil, nil, false))
			if err != nil {
				t.Error("constructor-normalized program rejected by Rebind", err)
			} else if err := rebound.Validate(read); err != nil || !slices.Equal(rebound.Dependencies(), before) {
				t.Error("remap lost normalized stamps/purposes", err, rebound.Dependencies())
			}
			if !slices.Equal(p.Dependencies(), before) {
				t.Fatal("validation/remap mutated source manifest")
			}
			for i, action := range tc.actions {
				if !slices.Equal(action.Binding().Dependencies, originalBindings[i]) || !slices.Equal(action.Presentation().Dependencies(), originalPresentations[i]) {
					t.Fatal("validation/remap mutated original per-action captures", i)
				}
			}
		})
	}
	// Changing both supplied manifests cannot forge the original callable's
	// exact purpose, lifetime or descriptor stamp.
	for _, kind := range []string{"missing", "purpose", "invalid-purpose", "support-only", "zero-stamp", "wrong-stamp", "foreign-domain", "stale-epoch", "forged-address", "subobject"} {
		t.Run(kind, func(t *testing.T) {
			action := call.Action(0)
			change := func(dependencies []catalog.ExpressionDependency) []catalog.ExpressionDependency {
				next := slices.Clone(dependencies)
				for i := range next {
					if next[i] != definition {
						continue
					}
					switch kind {
					case "missing":
						return slices.DeleteFunc(next, func(d catalog.ExpressionDependency) bool { return d == definition })
					case "purpose":
						next[i].Purpose = catalog.OperandDependency
					case "invalid-purpose":
						next[i].Purpose = catalog.DependencyPurpose(255)
					case "support-only":
						next[i].Purpose = catalog.AggregateSupportDependency
					case "zero-stamp":
						next[i].Revision = 0
					case "wrong-stamp":
						next[i].Revision++
					case "foreign-domain":
						next[i].Ref = new(ids.Domain).Ref(read.Epoch(), definition.Ref.Address)
					case "stale-epoch":
						next[i].Ref.Epoch++
					case "forged-address":
						next[i].Ref.Address.ObjectID++
					case "subobject":
						next[i].Ref.Address.SubID = 1
					}
				}
				return next
			}
			action = action.With(func(parts *stored.RuleActionParts) {
				parts.Binding.Dependencies = change(parts.Binding.Dependencies)
				parts.Presentation = parts.Presentation.With(func(v *stored.ViewParts) { v.Dependencies = change(v.Dependencies) })
			})
			p := stored.NewRoutineProgram([]stored.RuleAction{action})
			if err := p.Validate(read); err == nil {
				t.Error("forged original callable capture accepted")
			}
			if _, err := p.Rebind(read, nil, stored.NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
				t.Error("forged original callable capture remapped")
			}
			if err := call.Validate(read); err != nil {
				t.Fatal("negative copy mutated genuine source", err)
			}
		})
	}
	t.Run("different-revision-remains-independent", func(t *testing.T) {
		historical := operand
		historical.Revision++
		replace := func(ds []catalog.ExpressionDependency) []catalog.ExpressionDependency {
			ds = slices.Clone(ds)
			for i := range ds {
				if ds[i] == operand {
					ds[i] = historical
				}
			}
			return ds
		}
		action := literal.Action(0).With(func(parts *stored.RuleActionParts) {
			parts.Binding.Dependencies = replace(parts.Binding.Dependencies)
			parts.Presentation = parts.Presentation.With(func(v *stored.ViewParts) { v.Dependencies = replace(v.Dependencies) })
		})
		for _, actions := range [][]stored.RuleAction{{call.Action(0), action}, {action, call.Action(0)}} {
			p := stored.NewRoutineProgram(actions)
			if !contains(p.Dependencies(), definition) || !contains(p.Dependencies(), historical) {
				t.Fatal("different original stamps collapsed", p.Dependencies())
			}
			if err := p.Validate(read); err != nil {
				t.Error("independent historical operand rejected", err)
			}
			mapped, err := p.Rebind(read, nil, stored.NewRoutineRemap(read, nil, nil, nil, false))
			if err != nil {
				t.Error("historical operand remap rejected", err)
			} else if !contains(mapped.Dependencies(), historical) || !contains(mapped.Dependencies(), definition) {
				t.Error("remap refreshed original stamps", mapped.Dependencies())
			}
		}
	})
	t.Run("column-coverage-is-purpose-local", func(t *testing.T) {
		relation := preparedFoldStoredRelation(t, e, "normalization_input")
		whole := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Revision: relation.Revision()}
		for _, purpose := range []catalog.DependencyPurpose{catalog.DefinitionDependency, catalog.OperandDependency, catalog.AggregateSupportDependency} {
			column := whole
			column.Ref.Address.SubID = relation.Attributes[0]
			column.Purpose = purpose
			for _, reverse := range []bool{false, true} {
				extra := []catalog.ExpressionDependency{whole, column}
				if reverse {
					slices.Reverse(extra)
				}
				action := call.Action(0).With(func(parts *stored.RuleActionParts) {
					parts.Binding.Dependencies = append(slices.Clone(parts.Binding.Dependencies), extra...)
				})
				p := stored.NewRoutineProgram([]stored.RuleAction{action})
				before := slices.Clone(p.Dependencies())
				if !contains(before, column) || contains(before, whole) != (purpose != catalog.DefinitionDependency) {
					t.Fatal("column coverage crossed purpose boundary", purpose, reverse, before)
				}
				if err := p.Validate(read); err != nil {
					t.Error("valid purpose-local column coverage rejected", err)
				}
				mapped, err := p.Rebind(read, nil, stored.NewRoutineRemap(read, nil, nil, nil, false))
				if err != nil {
					t.Error("column coverage remap rejected", err)
				} else if !slices.Equal(mapped.Dependencies(), before) {
					t.Error("column coverage remap changed manifest", mapped.Dependencies())
				}
				if !slices.Equal(p.Dependencies(), before) {
					t.Fatal("column coverage mutated source")
				}
			}
		}
	})
}
