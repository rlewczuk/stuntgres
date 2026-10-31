package core_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func TestDropClosureRetiredRoutineCycle(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.Run(t, e, "CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()")
	for _, sql := range []string{
		"CREATE FUNCTION closure_a() RETURNS integer LANGUAGE SQL RETURN 1",
		"CREATE FUNCTION closure_b() RETURNS integer LANGUAGE SQL RETURN closure_a()",
		"CREATE OR REPLACE FUNCTION closure_a() RETURNS integer LANGUAGE SQL RETURN closure_b()",
		"CREATE FUNCTION closure_tail() RETURNS integer LANGUAGE SQL RETURN closure_b()",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	address := func(name string) ids.ObjectAddress {
		all := base.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name)
		if len(all) != 1 {
			t.Fatalf("procedure %s: %v", name, all)
		}
		return all[0]
	}
	a, b, tail := address("closure_a"), address("closure_b"), address("closure_tail")
	base = dropClosureRoutineCycleCatalog(t, e, base, []ids.ObjectAddress{a, b, tail})
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	reader.MarkDropRoots([]ids.ObjectAddress{a, b})
	for _, root := range []ids.ObjectAddress{a, b} {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), root)); err != nil {
			t.Fatal(err)
		}
	}
	before := stage.RemovalCount()
	edges, err := view.DropClosure(reader, []ids.ObjectAddress{a, a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].Address != tail || !edges[0].Normal || edges[0].Automatic || stage.RemovalCount() != before {
		t.Fatal("cycle did not retain exactly the live tail", edges)
	}
	for _, root := range []ids.ObjectAddress{a, b} {
		if got := stage.DropFlags[root]; !got.Original || !got.Normal {
			t.Fatal("on-stack/completed incoming reach lost", got)
		}
	}
}

// Build a closed private immutable generation before admitting any removal.
// CREATE OR REPLACE above leaves b's checked recipe holding the old a; that
// historical recipe is intentionally not valid removal authority for the new a.
// Rebind all three real definitions against private descriptor shells, then
// freeze the completed graph. No published descriptor or recipe is mutated.
func dropClosureRoutineCycleCatalog(t *testing.T, e *core.Engine, base catalog.Read, addresses []ids.ObjectAddress) catalog.Read {
	t.Helper()
	state := catalog.CloneState(base.State())
	read := base.WithState(state)
	procedures := make([]*catalog.Procedure, len(addresses))
	for i, address := range addresses {
		procedure := *base.MustProcedure(address)
		procedure.SQLBody = nil
		procedures[i] = &procedure
		state.SetDescriptor(address, &procedure)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth := enginetest.CatalogAuthorization(e, read)
	for _, procedure := range procedures {
		if _, _, err := schemacmds.BindSQLRoutineLocked(e, op, auth, procedure, sqltype.InputEnvironment{}, true); err != nil {
			t.Fatal("binding closed routine fixture", procedure.Name(), err)
		}
	}
	for _, procedure := range procedures {
		if err := read.Owner().Provider(ids.ProcedureClassID).Validate(read, procedure); err != nil {
			t.Fatal("admitting closed routine fixture", procedure.Name(), err)
		}
		if base.MustProcedure(procedure.Address()) == procedure {
			t.Fatal("fixture reused a published descriptor")
		}
	}
	if !catalog.ProcedureDefinitionDependsOn(read, procedures[0], addresses[1]) ||
		!catalog.ProcedureDefinitionDependsOn(read, procedures[1], addresses[0]) ||
		!catalog.ProcedureDefinitionDependsOn(read, procedures[2], addresses[1]) {
		t.Fatal("fixture lost mutual routine dependencies or live tail")
	}
	return read
}

func TestDropClosureRetiredPayloadOwners(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
		"CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()",
		"CREATE FUNCTION closure_value() RETURNS integer LANGUAGE SQL IMMUTABLE RETURN 1",
		"CREATE TABLE closure_pk(i integer PRIMARY KEY)",
		"CREATE TABLE closure_payload(i integer DEFAULT closure_value(), CONSTRAINT closure_ck CHECK(closure_value()>0), CONSTRAINT closure_fk FOREIGN KEY(i) REFERENCES closure_pk(i))",
		"CREATE POLICY closure_policy ON closure_payload USING(closure_value()>0)",
		"CREATE VIEW closure_view AS SELECT closure_value() AS i",
		"CREATE VIEW closure_live AS SELECT i FROM closure_view",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	owner := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_payload").Address)
	retiredView := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_view").Address)
	live := enginetest.RoleTableRef(t, e, "closure_live").Address
	procedure := base.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "closure_value")[0]
	parent := enginetest.RoleTableRef(t, e, "closure_pk").Address
	for _, roots := range [][]ids.ObjectAddress{{procedure, parent, owner.Address(), retiredView.Address()}, {retiredView.Address(), owner.Address(), parent, procedure}} {
		stage := e.BeginCatalogStage(base)
		reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
		reader.MarkDropRoots(roots)
		for _, address := range roots {
			if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), address)); err != nil {
				t.Fatal(err)
			}
		}
		before := stage.RemovalCount()
		edges, err := view.DropClosure(reader, roots)
		if err != nil {
			t.Fatal(err)
		}
		wanted := []ids.ObjectAddress{
			{ClassID: 2604, ObjectID: owner.ColumnMeta.Defaults[owner.Attributes[0]].OID},
			{ClassID: 2606, ObjectID: owner.Constraints.Check[0].OID},
			{ClassID: 2606, ObjectID: owner.Constraints.ForeignKeys[0].OID},
			{ClassID: ids.PolicyClassID, ObjectID: owner.Security.Policies[0].OID},
			{ClassID: 2618, ObjectID: retiredView.View.RuleOID()},
		}
		for _, address := range wanted {
			if got := stage.DropFlags[address]; !got.Normal || got.Original {
				t.Fatal("retired payload lost its own NORMAL", address, got)
			}
		}
		if stage.DropFlags[owner.Address()].Normal || !stage.DropFlags[retiredView.Address()].Normal {
			t.Fatal("payload flags leaked up or genuine reverse owner lost")
		}
		for fact := range catalog.ForeignKeyTriggerFacts(base) {
			if got := stage.DropFlags[ids.ObjectAddress{ClassID: 2620, ObjectID: fact.OID}]; got.Normal || got.Original {
				t.Fatal("FK flags propagated to INTERNAL RI trigger", fact, got)
			}
		}
		found := 0
		seen := map[ids.ObjectAddress]bool{}
		for _, edge := range edges {
			if seen[edge.Address] || slices.Contains(wanted, edge.Address) || slices.Contains(roots, edge.Address) {
				t.Fatal("retired owner created duplicate effect/reporting", edge)
			}
			seen[edge.Address] = true
			if edge.Address == live {
				found++
				if !edge.Normal || edge.Automatic {
					t.Fatal("redirected live reverse owner permission", edge)
				}
			}
		}
		if found != 1 || stage.RemovalCount() != before {
			t.Fatal("retired provider failed live-owner expansion", edges)
		}
		e.DiscardCatalogStage(stage)
	}
}

func TestDropClosureSuppressedPayloadClassification(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
		"CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()",
		"CREATE FUNCTION closure_value() RETURNS integer LANGUAGE SQL IMMUTABLE RETURN 1",
		"CREATE DOMAIN closure_domain AS integer CONSTRAINT closure_domain_ck CHECK(closure_value()>0)",
		"CREATE TABLE closure_columns(c closure_domain DEFAULT closure_value(), g integer GENERATED ALWAYS AS(c+closure_value()) STORED, CONSTRAINT closure_column_ck CHECK(c+closure_value()>0))",
		"CREATE INDEX closure_expression ON closure_columns((c+closure_value()))",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	procedure := base.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "closure_value")[0]
	relation := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_columns").Address)
	domain := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.Columns[0].Usage.Type.ID())})
	idx := enginetest.RoleTableRef(t, e, "closure_expression").Address
	wanted := []ids.ObjectAddress{
		{ClassID: 2606, ObjectID: domain.DomainChecks[0].OID},
		{ClassID: 2604, ObjectID: relation.ColumnMeta.Defaults[relation.Attributes[0]].OID},
		{ClassID: 2604, ObjectID: relation.ColumnMeta.Generations[relation.Attributes[1]].OID},
		{ClassID: 2606, ObjectID: relation.Constraints.Check[0].OID},
		idx,
	}
	for _, roots := range [][]ids.ObjectAddress{{domain.Address(), procedure}, {procedure, domain.Address()}} {
		stage := e.BeginCatalogStage(base)
		reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
		edges, err := view.DropClosure(reader, roots)
		if err != nil {
			t.Fatal(err)
		}
		for _, address := range wanted {
			if got := stage.DropFlags[address]; !got.Normal || got.Original {
				t.Fatal("effect guard hid genuine NORMAL", address, got)
			}
		}
		for _, edge := range edges {
			if edge.Address == idx && (!edge.Normal || !edge.Automatic) {
				t.Fatal("automatic index lost external NORMAL", edge)
			}
		}
		if stage.DropFlags[relation.Address()].Normal || stage.RemovalCount() != 0 {
			t.Fatal("column/payload provenance became whole-owner effect")
		}
		e.DiscardCatalogStage(stage)
	}
}
