package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestCreateAggregatePublicationCancellation(t *testing.T) {
	e := New()
	op, _ := conversionUnitAuth(t, e)
	before := e.CatalogRead()
	raw := statement(t, "CREATE AGGREGATE aggregate_unit_cancel(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')")
	e.Mu.Lock()
	stage, err := stageSchemaLocked(e, op, raw, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = publishCatalog(e, prepared, PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "aggregate_unit_cancel", Signature: "23"}
	after := e.CatalogRead()
	if after.State() != before.State() || after.State().HasDynamicProcedureName(key.Name) || after.NameAddress(key) != (ids.ObjectAddress{}) {
		t.Fatal("canceled aggregate published a catalog/index fragment")
	}
}

func TestCreateAggregatePayloadDeclarationFence(t *testing.T) {
	original := &catalog.Procedure{ObjectName: "aggregate_unit_identity", Signature: "23", RoutineKind: catalog.AggregateRoutine, Result: 23, Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: 90000, Kind: "n", Functions: [8]ids.OID{177}, TransitionType: 23, HasInitial: true, Initial: "0", FinalModify: "r", MovingFinalModify: "r"}}}
	copy := *original
	payload := *original.Aggregate
	copy.Aggregate = &payload
	if !exec.SameRoutineDeclaration(original, &copy) {
		t.Fatal("equal actual definition refused")
	}
	for _, change := range []func(*catalog.AggregateDefinition){
		func(d *catalog.AggregateDefinition) { d.Functions[0] = 179 },
		func(d *catalog.AggregateDefinition) { d.TransitionType = 20 },
		func(d *catalog.AggregateDefinition) { d.Initial = "1" },
		func(d *catalog.AggregateDefinition) { d.HasInitial = false },
		func(d *catalog.AggregateDefinition) { d.TransitionSpace = 32 },
	} {
		payload = *original.Aggregate
		change(&payload.Definition)
		if exec.SameRoutineDeclaration(original, &copy) {
			t.Fatal("changed aggregate payload silently retargeted retained declaration")
		}
	}
}

func TestCreateAggregateSupportActorPhases(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	roleExecute(t, e, admin, "CREATE ROLE aggregate_unit_reader LOGIN")
	roleExecute(t, e, admin, "CREATE AGGREGATE aggregate_unit_phase(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')")
	roleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.int4pl(integer,integer) FROM PUBLIC")
	reader, finish := roleOperation(t, e, "aggregate_unit_reader")
	t.Cleanup(finish)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(reader)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	read := auth.Catalog
	aggregateAddress := read.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "aggregate_unit_phase", Signature: "23"})
	aggregate, ok := read.Procedure(aggregateAddress)
	if !ok {
		t.Fatal("missing aggregate")
	}
	support, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 177})
	if !ok {
		t.Fatal("missing support")
	}
	call := exec.RoutineCall{Routine: support, Reference: e.Catalog.Domain.Ref(read.Epoch(), support.Address()), Actor: aggregate.Owner, SupportAggregate: aggregate}
	original := exec.RoutineProgram{Calls: []exec.RoutineCall{call}}
	authority := exec.RoutineAccess{View: auth.View, Env: auth}
	var phase exec.RoutineProgram
	if err = phase.InitializePhase(&original, authority); err != nil {
		t.Fatal("phase replaced support owner with body invoker", err)
	}
	if len(phase.Calls) != 1 || phase.Calls[0].Actor != aggregate.Owner {
		t.Fatal("lost support actor policy", phase.Calls)
	}
	if err = phase.Readmit(authority); err != nil {
		t.Fatal("owner support readmission", err)
	}
	call.SupportAggregate = nil
	ordinary := exec.RoutineProgram{Calls: []exec.RoutineCall{call}}
	var denied exec.RoutineProgram
	err = denied.InitializePhase(&ordinary, authority)
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42501" {
		t.Fatal("ordinary caller accidentally borrowed aggregate owner", err)
	}
}

func TestCreateAggregateDetachedPolymorphicSupport(t *testing.T) {
	e := New()
	read := e.CatalogRead()
	support, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 378})
	if !ok {
		t.Fatal("missing real array_append")
	}
	array, ok := read.LookupType(1007)
	if !ok {
		t.Fatal("missing integer array")
	}
	integer, ok := read.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing integer")
	}
	formals, result, err := routine.AggregateSupportTypes(read, nil, support, nil, []sqltype.Usage{array.Usage(), integer.Usage()})
	if err != nil || len(formals) != 2 || formals[0].Type != array || formals[1].Type != integer || result.Type != array {
		t.Fatalf("concrete specialization=%v/%v/%v", formals, result, err)
	}
	if support.Signature != "5078,5077" || support.Result != 5078 || support.Address().ObjectID != 378 {
		t.Fatal("specialization rewrote the actual polymorphic declaration", support)
	}
	aggregate := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90000}, ObjectName: "aggregate_unit_collect", Signature: "23", RoutineKind: catalog.AggregateRoutine, Result: 1007, Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: 90000, Kind: "n", Functions: [8]ids.OID{378}, TransitionType: 1007, HasInitial: true, Initial: "{}", FinalModify: "r", MovingFinalModify: "r"}}}
	if err := routine.ValidateAggregateDefinition(read, aggregate); err != nil {
		t.Fatal("detached real specialization refused", err)
	}
	wrong := *aggregate
	wrong.Signature = "25"
	if err := routine.ValidateAggregateDefinition(read, &wrong); err == nil {
		t.Fatal("incompatible concrete element accepted")
	}
	payload := *aggregate.Aggregate
	wrong = *aggregate
	wrong.Aggregate = &payload
	wrong.Result, payload.Definition.TransitionType = 1016, 1016
	if err := routine.ValidateAggregateDefinition(read, &wrong); err == nil {
		t.Fatal("nonbinary integer-to-bigint support edge accepted")
	}
	wrong = *aggregate
	wrong.Result = 23
	if err := routine.ValidateAggregateDefinition(read, &wrong); err == nil {
		t.Fatal("aggregate state/result disagreement accepted")
	}
}

// Public collation scenarios own SQL outcomes. This unit inspects the actual
// checked private formal frame and remapped providers, which SQL cannot expose.
func TestCreateAggregateCollationBinding(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE aggregate_unit_coll(v text COLLATE "C",w text COLLATE "POSIX",d text,id integer)`)
	run(t, e, "CREATE AGGREGATE aggregate_unit_text(text) (SFUNC=pg_catalog.textcat,STYPE=text,INITCOND='')")
	run(t, e, "CREATE FUNCTION aggregate_unit_count_step(integer,text,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1+length($2)+length($3)$$")
	run(t, e, "CREATE AGGREGATE aggregate_unit_count(text,text) (SFUNC=aggregate_unit_count_step,STYPE=integer,INITCOND='0')")
	run(t, e, "CREATE FUNCTION aggregate_unit_zero_step(text) RETURNS text LANGUAGE SQL AS $$SELECT $1$$")
	run(t, e, "CREATE AGGREGATE aggregate_unit_zero(*) (SFUNC=aggregate_unit_zero_step,STYPE=text,INITCOND='')")
	run(t, e, `CREATE DOMAIN aggregate_unit_state_c AS text COLLATE "C"`)
	run(t, e, "CREATE FUNCTION aggregate_unit_domain_step(aggregate_unit_state_c,text) RETURNS aggregate_unit_state_c LANGUAGE SQL AS $$SELECT $1$$")
	run(t, e, "CREATE AGGREGATE aggregate_unit_domain(text) (SFUNC=aggregate_unit_domain_step,STYPE=aggregate_unit_state_c,INITCOND='seed')")
	run(t, e, "CREATE FUNCTION aggregate_unit_domain_zero_step(aggregate_unit_state_c) RETURNS aggregate_unit_state_c LANGUAGE SQL AS $$SELECT $1$$")
	run(t, e, "CREATE AGGREGATE aggregate_unit_domain_zero(*) (SFUNC=aggregate_unit_domain_zero_step,STYPE=aggregate_unit_state_c,INITCOND='seed')")
	run(t, e, "CREATE FUNCTION aggregate_unit_domain_integer_step(aggregate_unit_state_c,integer) RETURNS aggregate_unit_state_c LANGUAGE SQL AS $$SELECT $1$$")
	run(t, e, "CREATE AGGREGATE aggregate_unit_domain_integer(integer) (SFUNC=aggregate_unit_domain_integer_step,STYPE=aggregate_unit_state_c,INITCOND='seed')")
	stateAddress := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "aggregate_unit_state_c"})
	domain, ok := e.CatalogRead().LookupType(sqltype.ID(stateAddress.ObjectID))
	if !ok || !domain.IsDomain() {
		t.Fatal("missing actual C-collated state domain")
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, tc := range []struct {
		name, expression   string
		result, input      uint32
		explicit, conflict bool
	}{
		{"C", `aggregate_unit_text(v COLLATE "C")`, 950, 950, true, false},
		{"POSIX", `aggregate_unit_text(w COLLATE "POSIX")`, 951, 951, true, false},
		{"indeterminate", `aggregate_unit_text(COALESCE(v,w))`, 100, 0, false, true},
		{"zero_input", `aggregate_unit_zero(*)`, 100, 0, false, false},
		{"input_only", `aggregate_unit_count(v COLLATE "C",w COLLATE "C")`, 0, 950, true, false},
		{"modifiers", `aggregate_unit_text(v COLLATE "C" ORDER BY w COLLATE "POSIX") FILTER(WHERE (w COLLATE "POSIX")<>'skip')`, 950, 950, true, false},
		{"domain_posix", `aggregate_unit_domain(w)`, 951, 951, false, false},
		{"domain_default", `aggregate_unit_domain(d)`, 100, 100, false, false},
		{"domain_modifiers", `aggregate_unit_domain(w ORDER BY d COLLATE "C") FILTER(WHERE (d COLLATE "C")<>'skip')`, 951, 951, false, false},
		{"domain_zero", `aggregate_unit_domain_zero(*)`, 950, 0, false, false},
		{"domain_integer", `aggregate_unit_domain_integer(id)`, 950, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := domainUnitBindSelect(t, e, op, "SELECT "+tc.expression+" AS v FROM aggregate_unit_coll")
			node, ok := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
			if !ok || len(node.Calls) != 1 {
				t.Fatal("missing actual transition aggregate")
			}
			provider, ok := node.Calls[0].Provider.(*exec.TransitionAggregate)
			if !ok || provider.Result.Collation.ID != tc.result || provider.InputCollation.ID != tc.input {
				t.Fatal("input/result collation authority", node.Calls[0].Provider)
			}
			projection := query.Root.(*plan.ProjectNode).Expressions[0]
			if tc.result != 0 && ((projection.ExplicitCollation().Character > 0) != tc.explicit || projection.CollationIndeterminate() != tc.conflict) {
				t.Fatal("result discarded full derivation", expr.InfoOf(projection))
			}
			check := func(p *exec.TransitionAggregate) {
				t.Helper()
				if sqltype.ID(p.Aggregate.Result) == domain.ID() && (p.StateUsage.Type != domain || p.Result.Type != domain || p.Inputs[0].Type != domain || p.Formals[0].Type != domain) {
					t.Fatal("input derivation replaced the actual domain state type")
				}
				want := tc.result
				if want == 0 {
					want = tc.input
				}
				for i, input := range p.Inputs {
					if input.Collation.ID != 0 && input.Collation.ID != want {
						t.Fatal("private input defaulted collation", i, input)
					}
				}
				for i, formal := range p.Formals {
					if formal.Collation.ID != 0 && formal.Collation.ID != want {
						t.Fatal("private formal defaulted collation", i, formal)
					}
				}
				if p.Call == nil {
					return
				}
				if p.Call.ResultUsage() != p.StateUsage || p.Call.CollationIndeterminate() != tc.conflict {
					t.Fatal("builtin support result discarded collation", expr.InfoOf(p.Call))
				}
				parameters := 0
				plan.WalkExpression(p.Call, func(child expr.Expr) {
					parameter, ok := child.(plan.ParameterExpr)
					if !ok {
						return
					}
					parameters++
					if parameter.ResultUsage() != p.Formals[parameter.Number-1] || (parameter.ExplicitCollation().Character > 0) != tc.explicit || parameter.CollationIndeterminate() != tc.conflict {
						t.Fatal("private parameter lost full checked Info", parameter)
					}
					if tc.conflict {
						left, right, position := parameter.ImplicitCollationConflict()
						if left.ID != 950 || right.ID != 951 || position.Character == 0 {
							t.Fatal("private conflict lost origins", parameter)
						}
					}
				})
				if parameters != len(p.Formals) {
					t.Fatal("private formal coverage", parameters, len(p.Formals))
				}
			}
			check(provider)
			if provider.StateUsage.Type == domain {
				// Effective default100 must override a declared domainC formal;
				// no ordinary input instead leaves that declared fallback intact.
				body := "SELECT $1 AS s"
				if len(provider.Formals) == 2 {
					body += ",$2 AS a"
				}
				raw := statement(t, body)
				e.Mu.Lock()
				auth, bindErr := e.AuthorizationLocked(op)
				var binding analyze.Binding
				if bindErr == nil {
					binding, bindErr = analyze.DescribeRoutine(op.Context(), e.AnalysisHost(op), raw, AuthorityOf(auth), ids.ObjectRef{}, sqltype.InputEnvironment{}, analyze.Expected{}, nil, provider.Support.SQL, provider.Support.Name(), provider.InputCollation)
				}
				e.Mu.Unlock()
				if bindErr != nil {
					t.Fatal(bindErr)
				}
				for i, parameter := range binding.Query.Root.(*plan.ProjectNode).Expressions {
					want := provider.Support.SQL.Inputs[i]
					if provider.InputCollation.ID != 0 && want.Collation.ID != 0 {
						want.Collation = provider.InputCollation
					}
					if parameter.ResultUsage() != want || parameter.ExplicitCollation().Character != 0 || parameter.CollationIndeterminate() {
						t.Fatal("domain closed formal inherited state instead of input", i, expr.InfoOf(parameter), want)
					}
				}
			}
			read := e.CatalogRead()
			mapped, err := stored.RemapViewQuery(read, node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
			if err != nil {
				t.Fatal("collated private frame remap", err)
			}
			rebuilt := mapped.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
			check(rebuilt)
			if rebuilt == provider || rebuilt.InputCollation != provider.InputCollation || rebuilt.StateUsage != provider.StateUsage || rebuilt.Result != provider.Result {
				t.Fatal("remap lost effective input/result collation")
			}
			original, originalInput := provider.Formals[0], provider.Inputs[0]
			rebuilt.Formals[0], rebuilt.Inputs[0] = sqltype.Usage{}, sqltype.Usage{}
			if provider.Formals[0] != original || provider.Inputs[0] != originalInput {
				t.Fatal("remap mutated source input/formal frame")
			}
			if tc.name == "C" || provider.StateUsage.Type == domain {
				// A same-type/different-collation forgery must still fail the
				// exact source formal check; do not weaken it to type equality.
				forged := *provider
				forged.Formals = append([]sqltype.Usage(nil), provider.Formals...)
				index, foreignID := 1, uint32(951)
				if provider.StateUsage.Type == domain {
					index = 0
				}
				if provider.Formals[index].Collation.ID == foreignID {
					foreignID = 950
				}
				foreign, err := sqltype.RegisteredCollation(foreignID, 950)
				if err != nil {
					t.Fatal(err)
				}
				forged.Formals[index].Collation = foreign
				candidate := *node
				candidate.Calls = append([]plan.AggregateCall(nil), node.Calls...)
				candidate.Calls[0].Provider = &forged
				if _, err := stored.RemapViewQuery(read, &candidate, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }); err == nil {
					t.Fatal("same-type foreign formal collation accepted")
				}
			}
		})
	}
	// Quoted support formals inherit only implicit effective collation. The
	// actual definition still owns slot/type contracts; frozen/ordinary remain
	// creation/default-bound rather than receiving the caller's explicit Info.
	read := e.CatalogRead()
	address := read.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "aggregate_unit_count_step", Signature: "23,25,25"})
	support, ok := read.Procedure(address)
	if !ok {
		t.Fatal("missing actual SQL support")
	}
	collation, err := sqltype.RegisteredCollation(950, 950)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"quoted", "ordinary", "frozen"} {
		t.Run(mode+"_formals", func(t *testing.T) {
			definition := *support.SQL
			input := collation
			if mode == "ordinary" {
				input = sqltype.CollationRef{}
			}
			definition.Standard = mode == "frozen"
			raw := statement(t, "SELECT $1 AS s,$2 AS a,$3 AS b")
			e.Mu.Lock()
			auth, authErr := e.AuthorizationLocked(op)
			var binding analyze.Binding
			if authErr == nil {
				binding, authErr = analyze.DescribeRoutine(op.Context(), e.AnalysisHost(op), raw, AuthorityOf(auth), ids.ObjectRef{}, sqltype.InputEnvironment{}, analyze.Expected{}, nil, &definition, definition.Name, input)
			}
			e.Mu.Unlock()
			if authErr != nil {
				t.Fatal(authErr)
			}
			for i, parameter := range binding.Query.Root.(*plan.ProjectNode).Expressions {
				want := definition.Inputs[i]
				if mode == "quoted" && want.Collation.ID != 0 {
					want.Collation = collation
				}
				if parameter.ResultUsage() != want || parameter.ExplicitCollation().Character != 0 || parameter.CollationIndeterminate() || binding.Parameters.Types[i].Type != definition.Inputs[i].Type {
					t.Fatal("closed SQL formal authority changed", i, expr.InfoOf(parameter), want)
				}
			}
		})
	}
}

func TestAggregateFinalCollationBinding(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE final_unit_coll(v text COLLATE "C",w text COLLATE "POSIX",d text,id integer)`)
	run(t, e, `CREATE DOMAIN final_unit_result AS text COLLATE "C"`)
	run(t, e, `CREATE FUNCTION final_unit_step(integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1+length($2)$$`)
	run(t, e, `CREATE FUNCTION final_unit_text(integer) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$`)
	run(t, e, `CREATE FUNCTION final_unit_domain(integer) RETURNS final_unit_result LANGUAGE SQL AS $$SELECT $1::text::final_unit_result$$`)
	run(t, e, `CREATE FUNCTION final_unit_extra(integer,text) RETURNS final_unit_result LANGUAGE SQL AS $$SELECT $1::text::final_unit_result$$`)
	run(t, e, `CREATE AGGREGATE final_unit_plain(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=final_unit_text)`)
	run(t, e, `CREATE AGGREGATE final_unit_fallback(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC=final_unit_domain)`)
	run(t, e, `CREATE AGGREGATE final_unit_collated(text) (SFUNC=final_unit_step,STYPE=integer,INITCOND='0',FINALFUNC=final_unit_extra,FINALFUNC_EXTRA)`)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	domainAddress := read.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "final_unit_result"})
	domain, ok := read.LookupType(sqltype.ID(domainAddress.ObjectID))
	if !ok || !domain.IsDomain() {
		t.Fatal("missing final result domain")
	}
	for _, tc := range []struct {
		name, expression string
		result, input    uint32
		domain, extra    bool
	}{
		{"plain", `final_unit_plain(id)`, 100, 0, false, false},
		{"domain_fallback", `final_unit_fallback(id)`, 950, 0, true, false},
		{"C", `final_unit_collated(v COLLATE "C")`, 950, 950, true, true},
		{"POSIX", `final_unit_collated(w COLLATE "POSIX")`, 951, 951, true, true},
		{"default", `final_unit_collated(d)`, 100, 100, true, true},
		{"modifiers", `final_unit_collated(w ORDER BY v COLLATE "C") FILTER(WHERE (v COLLATE "C")<>'skip')`, 951, 951, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := domainUnitBindSelect(t, e, op, "SELECT "+tc.expression+" AS v FROM final_unit_coll")
			node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
			p := node.Calls[0].Provider.(*exec.TransitionAggregate)
			if p.StateUsage != integer.Usage() || p.Inputs[0] != integer.Usage() || p.Formals[0] != integer.Usage() || p.Result.Collation.ID != tc.result || p.InputCollation.ID != tc.input || p.Result.Type.ID() != sqltype.ID(p.Aggregate.Result) || p.ResultUsage() != p.Result {
				t.Fatal("state/result or effective collation conflated", p)
			}
			if tc.domain && p.Result.Type != domain {
				t.Fatal("final result domain identity/generation lost")
			}
			if p.Final == nil || p.Final.Address().ObjectID != p.Aggregate.Aggregate.Definition.Functions[1] || p.FinalFormals[0] != integer.Usage() || p.FinalInputs[0] != integer.Usage() || len(p.FinalConversions) != len(p.FinalInputs) {
				t.Fatal("final private frame is not selected descriptor-owned", p)
			}
			if tc.extra {
				if len(p.FinalInputs) != 2 || len(p.FinalFormals) != 2 || p.FinalFormals[1] != p.Inputs[1] || p.FinalInputs[1] != p.Inputs[1] || p.FinalFormals[1].Collation.ID != tc.input {
					t.Fatal("typed extra/formal input collation lost", p.FinalInputs, p.FinalFormals)
				}
			} else if len(p.FinalInputs) != 1 || len(p.FinalFormals) != 1 {
				t.Fatal("ordinary final captured actual aggregated values")
			}
			// Remapping a captured aggregate uses each complete result/formal
			// usage; it cannot reconstruct the result from the transition type.
			mapped, err := stored.RemapViewQuery(read, node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return old })
			if err != nil {
				t.Fatal(err)
			}
			next := mapped.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
			if next.Result != p.Result || next.StateUsage != p.StateUsage || next.Final != p.Final || next.InputCollation != p.InputCollation {
				t.Fatal("final binding authority changed during remap")
			}
			next.FinalFormals[0] = sqltype.Usage{}
			if p.FinalFormals[0] != integer.Usage() {
				t.Fatal("remapped final mutated source frame")
			}
		})
	}
}
