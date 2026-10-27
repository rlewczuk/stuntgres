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
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// TestDropReaderSeam pins the view.DropReader seam on dropProvider: a staged
// drop reads SQL dependencies through its stage, a prepared drop through its
// preparation, and a bare provider has no definition input. Path is the
// stage's dependency path, and RetirementDescriptor still reads a removed
// relation.
func TestDropReaderSeam(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE retiring(id INTEGER)")
	read := e.CatalogRead()
	relation := read.MustRelation(roleTableRef(t, e, "retiring").Address)

	var bare view.DropReader = core.StageDropProvider(context.Background(), nil, nil, nil, nil)
	if input := bare.DefinitionInput(); input != nil {
		t.Fatalf("bare DefinitionInput = %T, want nil", input)
	}

	stage := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(stage)
	path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
	stage.DependencyPath = path
	var staged view.DropReader = core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	if input, ok := staged.DefinitionInput().(core.StageDefinitionReader); !ok || core.StageDefinitionReaderStage(&input) != stage {
		t.Fatalf("staged DefinitionInput = %T, want the stage reader", staged.DefinitionInput())
	}
	if !slices.Equal(staged.Path(), path) {
		t.Fatalf("Path = %v, want %v", staged.Path(), path)
	}
	if err := stage.Remove(e.Catalog.Domain.Ref(read.Epoch(), relation.Address())); err != nil {
		t.Fatal(err)
	}
	if !staged.Removed(relation.Address()) || staged.RetirementDescriptor(relation.Address()) != relation {
		t.Fatal("RetirementDescriptor must still read the removed relation")
	}

	prepared := &table.DropPreparation{Base: read, Discovery: read}
	var preparedReader view.DropReader = &core.DropProvider{Base: read, Prepared: prepared}
	if input, ok := preparedReader.DefinitionInput().(*table.DropPreparation); !ok || input != prepared {
		t.Fatalf("prepared DefinitionInput = %T, want the preparation", preparedReader.DefinitionInput())
	}
}

// These units use the actual catalog-stage reader and admitted descriptors.
// Closure discovery must merge provenance without creating removal effects.
func TestDropClosureRetiredInheritance(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.Run(t, e, "CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()")
	enginetest.Run(t, e, "CREATE TABLE closure_parent(i integer)")
	enginetest.Run(t, e, "CREATE TABLE closure_child() INHERITS(closure_parent)")
	base := e.CatalogRead()
	parent := enginetest.RoleTableRef(t, e, "closure_parent").Address
	child := enginetest.RoleTableRef(t, e, "closure_child").Address
	for _, roots := range [][]ids.ObjectAddress{{parent, child}, {child, parent}} {
		stage := e.BeginCatalogStage(base)
		reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
		reader.MarkDropRoots(roots)
		for _, address := range roots {
			if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), address)); err != nil {
				t.Fatal(err)
			}
		}
		removals := stage.RemovalCount()
		for range 2 {
			edges, err := view.DropClosure(reader, append(slices.Clone(roots), roots...))
			if err != nil {
				t.Fatal(err)
			}
			if len(edges) != 0 || stage.RemovalCount() != removals {
				t.Fatal("retired roots produced duplicate effects/reporting", edges)
			}
			if got := stage.DropFlags[parent]; !got.Original || got.Normal {
				t.Fatal("parent flags", got)
			}
			if got := stage.DropFlags[child]; !got.Original || !got.Normal {
				t.Fatal("child lost independent NORMAL", got)
			}
			for _, address := range roots {
				relation := base.MustRelation(address)
				row := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
				for _, oid := range []ids.OID{ids.OID(relation.RowType), ids.OID(row.Array)} {
					if got := stage.DropFlags[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: oid}]; got.Original || got.Normal {
						t.Fatal("implicit type inherited flags", oid, got)
					}
				}
			}
		}
		e.DiscardCatalogStage(stage)
	}
	// The same real edge still admits a live dependent for RESTRICT/CASCADE.
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	edges, err := view.DropClosure(core.StageDropProvider(context.Background(), stage, nil, nil, nil), []ids.ObjectAddress{parent})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range edges {
		if edge.Address == child {
			found = true
			if !edge.Normal || edge.Automatic {
				t.Fatal("live inheritance permission changed", edge)
			}
		}
	}
	if !found {
		t.Fatal("live inheritance dependency disappeared")
	}
}

func TestDropClosureRetiredDiamond(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.Run(t, e, "CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()")
	for _, sql := range []string{
		"CREATE TABLE closure_top(i integer)",
		"CREATE TABLE closure_left() INHERITS(closure_top)",
		"CREATE TABLE closure_right() INHERITS(closure_top)",
		"CREATE TABLE closure_bottom() INHERITS(closure_left,closure_right)",
		"CREATE VIEW closure_consumer AS SELECT i FROM closure_bottom",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	var roots []ids.ObjectAddress
	for _, name := range []string{"closure_top", "closure_left", "closure_right", "closure_bottom"} {
		address := enginetest.RoleTableRef(t, e, name).Address
		roots = append(roots, address)
		reader.MarkDropRoots([]ids.ObjectAddress{address})
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), address)); err != nil {
			t.Fatal(err)
		}
	}
	consumer := enginetest.RoleTableRef(t, e, "closure_consumer").Address
	before := stage.RemovalCount()
	edges, err := view.DropClosure(reader, append(roots, roots[0]))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[ids.ObjectAddress]int{}
	for _, edge := range edges {
		counts[edge.Address]++
		if counts[edge.Address] != 1 || slices.Contains(roots, edge.Address) {
			t.Fatal("duplicate/retired result", edge)
		}
	}
	if counts[consumer] != 1 || !stage.DropFlags[consumer].Normal || stage.DropFlags[consumer].Original || stage.RemovalCount() != before {
		t.Fatal("retired diamond lost live reverse owner or created effects", edges)
	}
	for i, address := range roots {
		if got := stage.DropFlags[address]; !got.Original || got.Normal != (i != 0) {
			t.Fatal("diamond provenance", i, got)
		}
	}
}

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
		if _, _, err := schemacmds.BindSQLRoutineLocked(e, op, auth, procedure, sqltype.InputEnvironment{}, nil); err != nil {
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
	if !catalog.ProcedureDefinitionDependsOn(procedures[0], addresses[1]) ||
		!catalog.ProcedureDefinitionDependsOn(procedures[1], addresses[0]) ||
		!catalog.ProcedureDefinitionDependsOn(procedures[2], addresses[1]) {
		t.Fatal("fixture lost mutual routine dependencies or live tail")
	}
	return read
}

func TestDropClosureNormalAutomaticUnion(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.Run(t, e, "CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()")
	enginetest.Run(t, e, "CREATE TABLE closure_serial(s serial)")
	base := e.CatalogRead()
	relation := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_serial").Address)
	column := relation.Address()
	column.SubID = relation.Attributes[0]
	sequence := enginetest.RoleTableRef(t, e, "closure_serial_s_seq").Address
	attrdef := ids.ObjectAddress{ClassID: 2604, ObjectID: relation.ColumnMeta.Defaults[column.SubID].OID}
	for _, roots := range [][]ids.ObjectAddress{{column, sequence}, {sequence, column}} {
		stage := e.BeginCatalogStage(base)
		reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
		edges, err := view.DropClosure(reader, roots)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, edge := range edges {
			if edge.Address == attrdef {
				found++
				if !edge.Normal || !edge.Automatic {
					t.Fatal("NORMAL and automatic permission are independent", edge)
				}
			}
		}
		if found != 1 || !stage.DropFlags[attrdef].Normal || stage.DropFlags[relation.Address()].Normal || stage.RemovalCount() != 0 {
			t.Fatal("union duplicated effects or leaked to whole owner", edges)
		}
		e.DiscardCatalogStage(stage)
	}
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

func TestDropClosurePassiveViewGuard(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
		"CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()",
		"CREATE DOMAIN closure_domain AS integer",
		"CREATE TABLE closure_source(c closure_domain)",
		"CREATE VIEW closure_passive AS SELECT c FROM closure_source",
		"CREATE VIEW closure_explicit AS SELECT NULL::closure_domain AS c",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	source := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_source").Address)
	domain := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Columns[0].Usage.Type.ID())}
	passive := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_passive").Address)
	explicit := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_explicit").Address)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	for _, relation := range []*catalog.Relation{passive, explicit} {
		got, err := view.PassiveTypeDependency(reader, relation, domain)
		if err != nil || got != (relation == passive) {
			t.Fatal("actual view dependency classification", relation.Name(), got, err)
		}
	}
	edges, err := view.DropClosure(reader, []ids.ObjectAddress{domain})
	if err != nil {
		t.Fatal(err)
	}
	found := map[ids.ObjectAddress]bool{}
	for _, edge := range edges {
		if edge.Address == passive.Address() || edge.Address == explicit.Address() {
			found[edge.Address] = true
			if !edge.Normal || (edge.Target == domain) != (edge.Address == explicit.Address()) {
				t.Fatal("passive capture displaced genuine source-column owner", edge)
			}
		}
	}
	if !found[passive.Address()] || !found[explicit.Address()] || stage.DropFlags[source.Address()].Normal {
		t.Fatal("view reach lost or subobject flags leaked upward", edges)
	}
	for _, relation := range []*catalog.Relation{passive, explicit} {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), relation.Address())); err != nil {
			t.Fatal(err)
		}
		got, err := view.PassiveTypeDependency(reader, relation, domain)
		if err != nil || got != (relation == passive) {
			t.Fatal("retirement bypassed passive-type classification", got, err)
		}
	}
	retiredEdges, err := view.DropClosure(reader, []ids.ObjectAddress{domain, passive.Address(), explicit.Address()})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range retiredEdges {
		if edge.Relation != nil && (edge.Relation.Address() == passive.Address() || edge.Relation.Address() == explicit.Address()) {
			t.Fatal("retired view or output payload became an effect", edge)
		}
	}
}
