package schema_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
)

func transactionalRoutineDescriptor(t *testing.T, e *core.Engine, name string) catalog.Descriptor {
	t.Helper()
	for _, d := range e.CatalogRead().Descriptors() {
		if d.Name() == name && d.Namespace().ObjectID == ids.PublicNamespaceOID {
			return d
		}
	}
	t.Fatal("missing descriptor", name)
	return nil
}

func TestTransactionalRoutineDependencyPreflight(t *testing.T) {
	for _, family := range []string{"standard", "default", "aggregate", "operator", "state-type", "operator-type"} {
		t.Run(family, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE FUNCTION txr_provider(integer,integer) RETURNS integer RETURN $1+$2")
			enginetest.Run(t, e, "CREATE DOMAIN txr_type AS integer")
			enginetest.Run(t, e, "CREATE FUNCTION txr_domain_step(txr_type,integer) RETURNS txr_type RETURN ($1+$2)::txr_type")
			provider := transactionalRoutineDescriptor(t, e, "txr_provider")
			if family == "state-type" || family == "operator-type" {
				provider = transactionalRoutineDescriptor(t, e, "txr_type")
			}
			base := e.CatalogRead().State()
			q := map[string]string{
				"standard":      "CREATE FUNCTION txr_child() RETURNS integer RETURN txr_provider(2,3)",
				"default":       "CREATE FUNCTION txr_child(x integer DEFAULT txr_provider(2,3)) RETURNS integer RETURN x",
				"aggregate":     "CREATE AGGREGATE txr_child(integer) (SFUNC=txr_provider,STYPE=integer,INITCOND='0')",
				"operator":      "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=txr_provider)",
				"state-type":    "CREATE AGGREGATE txr_child(integer) (SFUNC=txr_domain_step,STYPE=txr_type,INITCOND='0')",
				"operator-type": "CREATE OPERATOR public.#~# (LEFTARG=txr_type,RIGHTARG=int4,FUNCTION=txr_domain_step)",
			}[family]
			enginetest.Run(t, e, q)
			childName := "txr_child"
			if family == "operator" || family == "operator-type" {
				childName = "#~#"
			}
			child := transactionalRoutineDescriptor(t, e, childName)
			currentRead := e.CatalogRead()
			current := currentRead.State()
			candidate := catalog.CloneState(current)
			candidate.RemoveDescriptor(provider.Address())
			observations := []session.DependencyObservation{{Base: base, Targets: []ids.ObjectAddress{provider.Address()}}}
			if err := core.CatalogDependencyPreflight(currentRead, currentRead.WithState(candidate), observations, nil); err == nil {
				t.Fatal("new surviving hard edge escaped retirement")
			}
			// Expression observations also protect retained-message admission
			// before its removals are overlaid; type edges require absence.
			existingErr := core.CatalogDependencyPreflight(currentRead, currentRead, observations, nil)
			if provider.Address().ClassID == ids.TypeClassID {
				if existingErr != nil {
					t.Fatal("type edge fenced a still-present target", existingErr)
				}
			} else if existingErr == nil {
				t.Fatal("new expression edge escaped pre-overlay observation")
			}
			removed := catalog.CloneState(candidate)
			removed.RemoveDescriptor(child.Address())
			if err := core.CatalogDependencyPreflight(currentRead, currentRead.WithState(removed), observations, nil); err != nil {
				t.Fatal("removed owner still fenced", err)
			}
			if err := core.CatalogDependencyPreflight(currentRead, currentRead.WithState(candidate), []session.DependencyObservation{{Base: current, Targets: []ids.ObjectAddress{provider.Address()}}}, nil); err != nil {
				t.Fatal("unchanged committed survivor treated as late", err)
			}
			unrelated := []session.DependencyObservation{{Base: base, Targets: []ids.ObjectAddress{{ClassID: ids.ProcedureClassID, ObjectID: 999999}}}}
			if err := core.CatalogDependencyPreflight(currentRead, currentRead.WithState(candidate), unrelated, nil); err != nil {
				t.Fatal("unrelated change fenced", err)
			}
			// Rewire the candidate owner to a descriptor with no provider edge;
			// the current survivor must not override the owner's legitimate edit.
			rewired := catalog.CloneState(candidate)
			switch d := child.(type) {
			case *catalog.Procedure:
				n := *d
				n.SQLBody, n.SQLDefaults, n.Aggregate, n.SQL = nil, nil, nil, nil
				n.Signature, n.Result = "", 23
				rewired.SetDescriptor(n.Address(), &n)
			case *catalog.Operator:
				n := *d
				n.Function, n.Left, n.Right, n.Result = 0, 23, 23, 23
				rewired.SetDescriptor(n.Address(), &n)
			}
			if err := core.CatalogDependencyPreflight(currentRead, currentRead.WithState(rewired), observations, nil); err != nil {
				t.Fatal("legitimate candidate rewire rejected", err)
			}
		})
	}
	t.Run("reciprocal-links-are-not-hard-edges", func(t *testing.T) {
		read := engine.New().CatalogRead()
		ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		target := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 99001}, ObjectNamespace: ns, ObjectName: "#~#", Left: 23, Right: 23}
		base := enginetest.CatalogStateFixture(1, map[ids.ObjectAddress]catalog.Descriptor{target.Address(): target}, nil)
		child := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 99002}, ObjectNamespace: ns, ObjectName: "#?#", Left: 23, Right: 23, Commutator: target.Address().ObjectID, Negator: target.Address().ObjectID}
		current := catalog.CloneState(base)
		current.SetDescriptor(child.Address(), child)
		candidate := catalog.CloneState(current)
		candidate.RemoveDescriptor(target.Address())
		if err := core.CatalogDependencyPreflight(read.WithState(current), read.WithState(candidate), []session.DependencyObservation{{Base: base, Targets: []ids.ObjectAddress{target.Address()}}}, nil); err != nil {
			t.Fatal("reciprocal metadata became dependency", err)
		}
	})
}

func TestGroupDependencyRoutineRetirementPreflight(t *testing.T) {
	for _, body := range []struct{ name, sql string }{
		{"return", "RETURN (SELECT s FROM txrg_parent GROUP BY id)"},
		{"atomic", "BEGIN ATOMIC SELECT s FROM txrg_parent GROUP BY id; END"},
	} {
		t.Run(body.name, func(t *testing.T) {
			e := engine.New()
			for _, sql := range []string{
				"CREATE TABLE txrg_parent(id int PRIMARY KEY,s text)",
				"CREATE TABLE txrg_other(id int PRIMARY KEY,s text)",
				"CREATE FUNCTION txrg_provider(integer,integer) RETURNS integer RETURN $1+$2",
			} {
				enginetest.Run(t, e, sql)
			}
			parent := enginetest.PrimaryKeyUnitRelation(t, e, "txrg_parent")
			other := enginetest.PrimaryKeyUnitRelation(t, e, "txrg_other")
			provider := transactionalRoutineDescriptor(t, e, "txrg_provider")
			base := e.CatalogRead().State()
			enginetest.Run(t, e, "CREATE FUNCTION txrg_child(x integer DEFAULT txrg_provider(2,3)) RETURNS text LANGUAGE SQL "+body.sql)
			read := e.CatalogRead()
			current := read.State()
			child := transactionalRoutineDescriptor(t, e, "txrg_child").(*catalog.Procedure)
			program, err := stored.RoutineProgramOf(child)
			if err != nil {
				t.Fatal("missing accepted SQL-standard body", err)
			}
			if err := program.Validate(read); err != nil {
				t.Fatal("invalid accepted grouped body", err)
			}
			constraint := ids.ObjectAddress{ClassID: 2606, ObjectID: parent.Constraints.Unique[0].OID}
			if current.Get(constraint) != nil {
				t.Fatal("supporting PK must be attached metadata, not a standalone descriptor")
			}
			var grouping []ids.ObjectAddress
			if !program.VisitGroupingConstraintDependencies(read, func(a ids.ObjectAddress) bool {
				grouping = append(grouping, a)
				return true
			}) || !slices.Equal(grouping, []ids.ObjectAddress{constraint}) {
				t.Fatal("accepted body lacks the genuine attached PK edge", grouping)
			}
			for _, dependency := range program.Dependencies() {
				if dependency.Ref.Address == provider.Address() {
					t.Fatal("helper must be a default-only dependency")
				}
			}
			if found, complete := catalog.ProcedureDefinitionDependsOn(read, read, child, provider.Address()); child.SQLDefaults == nil || !found || complete {
				t.Fatal("accepted routine lacks its independent default dependency")
			}
			visited := 0
			if !catalog.VisitProcedureDefinitionDependencies(read, read, child, func(ids.ObjectAddress) bool {
				visited++
				return true
			}) || visited == 0 {
				t.Fatal("accepted definition projection did not complete", visited)
			}
			visited = 0
			if catalog.VisitProcedureDefinitionDependencies(read, read, child, func(ids.ObjectAddress) bool {
				visited++
				return false
			}) || visited != 1 {
				t.Fatal("definition visitor lost callback termination", visited)
			}
			bodyDependencies := slices.Clone(program.Dependencies())
			defaultDependencies := slices.Clone(child.SQLDefaults.Dependencies())
			bodyBacking, defaultsBacking := child.SQLBody, child.SQLDefaults
			primaryKey := parent.Constraints.Unique[0]
			keyAttributes := slices.Clone(primaryKey.Attributes)
			t.Run("dependency_helper_completion", func(t *testing.T) {
				absent := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ^ids.OID(0)}
				if found, complete := catalog.ProcedureDefinitionDependsOn(read, read, child, absent); found || !complete {
					t.Fatal("complete absence lost helper completion", found, complete)
				}
				if found, complete := catalog.ProcedureDefinitionDependsOn(read, read, child, constraint); !found || complete {
					t.Fatal("matching grouped edge must stop early", found, complete)
				}
				forged := catalog.CloneState(current)
				changed := *parent
				changed.Constraints.Unique = slices.Clone(parent.Constraints.Unique)
				changed.Constraints.Unique[0].OID++
				forged.SetDescriptor(parent.Address(), &changed)
				for _, bodyView := range []catalog.Read{{}, read.WithState(forged)} {
					if found, complete := catalog.ProcedureDefinitionDependsOn(read, bodyView, child, absent); found || complete {
						t.Fatal("helper hid missing or forged body authority", found, complete)
					}
					if found, complete := catalog.ProcedureDefinitionDependsOn(read, bodyView, child, ids.ObjectAddress{}); found || !complete {
						t.Fatal("zero target must complete without projection", found, complete)
					}
				}
			})

			t.Run("definition_provider_completion", func(t *testing.T) {
				for _, authority := range []string{"unchanged", "removed", "same_name_new_oid"} {
					t.Run(authority, func(t *testing.T) {
						op, done := enginetest.RoleOperation(t, e, "postgres")
						defer done()
						scope, err := e.StartStatement(op)
						if err != nil {
							t.Fatal(err)
						}
						defer scope.Finish(nil)
						if err := scope.Gate(enginetest.Statement(t, "CREATE OR REPLACE FUNCTION txrg_child(x integer DEFAULT txrg_provider(2,3)) RETURNS text LANGUAGE SQL "+body.sql)); err != nil {
							t.Fatal(err)
						}
						e.Mu.Lock()
						auth, err := e.AuthorizationLocked(op)
						if err != nil {
							e.Mu.Unlock()
							t.Fatal(err)
						}
						stage := e.BeginCatalogStage(auth.Catalog)
						stage.AuthorizeLocked(auth, op.Session)
						e.Mu.Unlock()
						defer e.DiscardCatalogStage(stage)
						// A newly retained real body owner makes this a definition
						// publication, not an incidental descriptor edit skipped by
						// the provider collector.
						actions := make([]stored.RuleAction, program.Len())
						for i := range actions {
							actions[i] = program.Action(i)
						}
						published := *child
						published.SQLBody = stored.NewRoutineProgram(actions)
						if err := stage.Replace(&published); err != nil {
							t.Fatal(err)
						}
						if authority != "unchanged" {
							changed := *parent
							changed.Constraints.Unique = slices.Clone(parent.Constraints.Unique)
							if authority == "removed" {
								changed.Constraints.Unique = nil
							} else {
								changed.Constraints.Unique[0].OID++
							}
							if err := stage.Replace(&changed); err != nil {
								t.Fatal(err)
							}
						}
						stageRead := stage.StageRead()
						complete := published.SQLBody.VisitGroupingConstraintDependencies(stageRead, func(ids.ObjectAddress) bool { return true })
						if complete != (authority == "unchanged") {
							t.Fatal("fixture does not distinguish genuine from corrupt PK authority")
						}
						err = scope.AcquireRoutineDefinitionProviders(stage)
						if authority == "unchanged" {
							if err != nil {
								t.Fatal("valid grouped body/default provider admission", err)
							}
						} else if err == nil {
							t.Error("incomplete grouped-body projection admitted definition providers")
						}
						if stage.Base.State() != current || stage.Base.Get(parent.Address()) != parent ||
							child.SQLBody != bodyBacking || child.SQLDefaults != defaultsBacking ||
							!slices.Equal(program.Dependencies(), bodyDependencies) || !slices.Equal(child.SQLDefaults.Dependencies(), defaultDependencies) {
							t.Error("provider admission mutated immutable retained authority")
						}
					})
				}
			})

			// Use a genuinely accepted replacement, not a descriptor with its
			// checked body/default fields cleared. Its identity is unchanged.
			enginetest.Run(t, e, "CREATE OR REPLACE FUNCTION txrg_child(x integer DEFAULT 0) RETURNS text LANGUAGE SQL RETURN 'rewired'::text")
			replacement := transactionalRoutineDescriptor(t, e, "txrg_child").(*catalog.Procedure)
			rewiredRead := e.CatalogRead()
			if replacement.Address() != child.Address() || replacement == child {
				t.Fatal("replacement did not retain identity with distinct backing")
			}
			// Bind a replacement against a genuinely new PK in the same catalog
			// incarnation. It cannot authenticate through the older current read.
			enginetest.Run(t, e, "ALTER TABLE txrg_parent DROP CONSTRAINT "+catalog.QuoteIdentifier(primaryKey.Name))
			enginetest.Run(t, e, "ALTER TABLE txrg_parent ADD CONSTRAINT txrg_replacement_pk PRIMARY KEY(id)")
			enginetest.Run(t, e, "CREATE OR REPLACE FUNCTION txrg_child(x integer DEFAULT 0) RETURNS text LANGUAGE SQL "+body.sql)
			candidateRead := e.CatalogRead()
			published := candidateRead.State()
			groupedReplacement := transactionalRoutineDescriptor(t, e, "txrg_child").(*catalog.Procedure)
			candidateParent := enginetest.PrimaryKeyUnitRelation(t, e, "txrg_parent")
			candidateKey := candidateParent.Constraints.Unique[0]
			candidateConstraint := ids.ObjectAddress{ClassID: 2606, ObjectID: candidateKey.OID}
			candidateProgram, err := stored.RoutineProgramOf(groupedReplacement)
			if err != nil {
				t.Fatal("missing candidate-authored grouped replacement", err)
			}
			if groupedReplacement.Address() != child.Address() || groupedReplacement.SQLBody == child.SQLBody || candidateKey.OID == primaryKey.OID {
				t.Fatal("grouped replacement did not acquire new body and PK backing")
			}
			if err := candidateProgram.Validate(candidateRead); err != nil {
				t.Fatal("new candidate grouping proof is not accepted", err)
			}
			if err := candidateProgram.Validate(read); err == nil {
				t.Fatal("new candidate proof unexpectedly authenticated through old PK metadata")
			}
			candidateBody, candidateDefaults := groupedReplacement.SQLBody, groupedReplacement.SQLDefaults
			candidateDependencies := slices.Clone(candidateProgram.Dependencies())
			candidateAttributes := slices.Clone(candidateKey.Attributes)
			t.Cleanup(func() {
				if base.Get(child.Address()) != nil || base.Get(parent.Address()) != parent || base.Get(provider.Address()) != provider ||
					current.Get(child.Address()) != child || current.Get(parent.Address()) != parent || current.Get(provider.Address()) != provider ||
					rewiredRead.Get(child.Address()) != replacement || rewiredRead.Get(parent.Address()) != parent ||
					e.CatalogRead().State() != published || published.Get(child.Address()) != groupedReplacement || published.Get(parent.Address()) != candidateParent {
					t.Error("preflight mutated a retained catalog predecessor or published root")
				}
				if child.SQLBody != bodyBacking || child.SQLDefaults != defaultsBacking ||
					!slices.Equal(program.Dependencies(), bodyDependencies) || !slices.Equal(child.SQLDefaults.Dependencies(), defaultDependencies) ||
					len(parent.Constraints.Unique) != 1 || parent.Constraints.Unique[0].OID != primaryKey.OID ||
					!parent.Constraints.Unique[0].Primary || !slices.Equal(parent.Constraints.Unique[0].Attributes, keyAttributes) {
					t.Error("candidate edits changed immutable routine/default/PK backing")
				}
				var retained []ids.ObjectAddress
				if !program.VisitGroupingConstraintDependencies(read, func(a ids.ObjectAddress) bool {
					retained = append(retained, a)
					return true
				}) || !slices.Equal(retained, grouping) {
					t.Error("preflight changed the retained grouping proof", retained)
				}
				if err := program.Validate(read); err != nil {
					t.Error("retained grouped body lost its original authority", err)
				}
				if groupedReplacement.SQLBody != candidateBody || groupedReplacement.SQLDefaults != candidateDefaults ||
					!slices.Equal(candidateProgram.Dependencies(), candidateDependencies) ||
					len(candidateParent.Constraints.Unique) != 1 || candidateParent.Constraints.Unique[0].OID != candidateKey.OID ||
					!slices.Equal(candidateParent.Constraints.Unique[0].Attributes, candidateAttributes) {
					t.Error("preflight changed candidate-authored immutable body or PK backing")
				}
				if err := candidateProgram.Validate(candidateRead); err != nil {
					t.Error("candidate-authored body lost its original authority", err)
				}
			})

			for _, target := range []struct {
				name    string
				address ids.ObjectAddress
			}{
				{"default_only", provider.Address()},
				{"supporting_pk", constraint},
			} {
				t.Run(target.name, func(t *testing.T) {
					retire := func() *catalog.State {
						candidate := catalog.CloneState(current)
						if target.address == constraint {
							changed := *parent
							changed.Constraints.Unique = nil
							candidate.SetDescriptor(parent.Address(), &changed)
							if candidate.Get(constraint) != nil || len(candidate.MustRelation(parent.Address()).Constraints.Unique) != 0 {
								t.Fatal("candidate did not actually retire attached PK metadata")
							}
						} else {
							candidate.RemoveDescriptor(target.address)
						}
						return candidate
					}
					for _, control := range []string{"late", "before_overlay", "removed_owner", "unchanged", "rewired", "renamed_owner", "candidate_grouped", "candidate_grouped_key", "unrelated", "malformed_owner"} {
						t.Run(control, func(t *testing.T) {
							candidate := retire()
							observations := []session.DependencyObservation{{Base: base, Targets: []ids.ObjectAddress{target.address}}}
							wantConflict := false
							switch control {
							case "late":
								// Authenticate the committed proof against current;
								// candidate has already removed its canonical PK.
								wantConflict = true
							case "before_overlay":
								candidate = catalog.CloneState(current)
								wantConflict = true
							case "removed_owner":
								candidate.RemoveDescriptor(child.Address())
							case "unchanged":
								observations[0].Base = current
							case "rewired":
								candidate.SetDescriptor(child.Address(), replacement)
							case "renamed_owner":
								changed := *child
								changed.ObjectName += "_renamed"
								changed.ObjectRevision++
								candidate.SetDescriptor(child.Address(), &changed)
								wantConflict = true
							case "candidate_grouped", "candidate_grouped_key":
								candidate = catalog.CloneState(published)
								if target.address == provider.Address() {
									candidate.RemoveDescriptor(target.address)
								}
								if control == "candidate_grouped_key" {
									observations[0].Targets = []ids.ObjectAddress{candidateConstraint}
									wantConflict = true
								}
							case "unrelated":
								observations[0].Targets = []ids.ObjectAddress{{ClassID: 2606, ObjectID: other.Constraints.Unique[0].OID}}
							case "malformed_owner":
								actions := make([]stored.RuleAction, program.Len())
								for i := range actions {
									actions[i] = program.Action(i)
								}
								actions[0] = actions[0].With(func(parts *stored.RuleActionParts) {
									parts.Presentation = nil
									// Keep authentic parent captures so an incomplete
									// projection cannot disappear with its manifest.
									parts.Binding.Dependencies = slices.Clone(program.Dependencies())
								})
								malformed := stored.NewRoutineProgram(actions)
								if err := malformed.Validate(read); err == nil {
									t.Fatal("malformed owner fixture remained valid")
								}
								if malformed.VisitGroupingConstraintDependencies(read, func(ids.ObjectAddress) bool { return true }) {
									t.Fatal("malformed owner must stop authenticated projection")
								}
								changed := *child
								changed.SQLBody = malformed
								if catalog.VisitProcedureDefinitionDependencies(read, read, &changed, func(ids.ObjectAddress) bool { return true }) {
									t.Fatal("shared visitor hid incomplete authenticated body projection")
								}
								candidate.SetDescriptor(child.Address(), &changed)
								wantConflict = true
							}
							err := core.CatalogDependencyPreflight(read, read.WithState(candidate), observations, nil)
							if wantConflict {
								var conflict catalog.Conflict
								if !errors.As(err, &conflict) {
									t.Fatal("late or unauthenticated surviving owner escaped retirement conflict", err)
								}
							} else if err != nil {
								t.Fatal("preservation control fenced", err)
							}
						})
					}
				})
			}
		})
	}
}

func TestTransactionalRoutineJournalIsolation(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE FUNCTION txr_provider(integer,integer) RETURNS integer RETURN $1+$2", "CREATE FUNCTION txr_child(x integer DEFAULT txr_provider(2,3)) RETURNS integer RETURN x", "CREATE AGGREGATE txr_sum(integer) (SFUNC=txr_provider,STYPE=integer,INITCOND='0')", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=txr_provider)"} {
		enginetest.Run(t, e, q)
	}
	base := e.CatalogRead()
	journal := session.NewJournal()
	for _, name := range []string{"txr_child", "txr_sum", "#~#"} {
		d := transactionalRoutineDescriptor(t, e, name)
		switch d := d.(type) {
		case *catalog.Procedure:
			n := *d
			n.ObjectName += "_new"
			journal.RecordDescriptor(base.State(), d.Address(), &n)
		case *catalog.Operator:
			n := *d
			n.ObjectName = "#?#"
			journal.RecordDescriptor(base.State(), d.Address(), &n)
		}
	}
	private := journal.PrivateRead(base.Owner(), base.State())
	if private.State() == base.State() {
		t.Fatal("journal mutated base")
	}
	for _, name := range []string{"txr_child", "txr_sum", "#~#"} {
		d := transactionalRoutineDescriptor(t, e, name)
		n := private.Get(d.Address())
		if n == d || base.Get(d.Address()) != d || private.NameAddress(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) || private.NameAddress(catalog.DescriptorNameKey(n)) != d.Address() {
			t.Fatal("private name tombstone/root isolation", name)
		}
		if d, ok := d.(*catalog.Procedure); ok {
			n := n.(*catalog.Procedure)
			if n.SQL != d.SQL || n.SQLBody != d.SQLBody || n.SQLDefaults != d.SQLDefaults || n.Aggregate != d.Aggregate || n.Implementation != d.Implementation {
				t.Fatal("COW detached immutable program/provider", name)
			}
			if len(private.State().ProceduresNamed(n.Namespace(), n.Name())) != 1 || len(private.State().ProceduresNamed(d.Namespace(), d.Name())) != 0 {
				t.Fatal("private procedure index", name)
			}
		}
		if _, ok := d.(*catalog.Operator); ok {
			oldKey := catalog.OperatorNameKey{Namespace: d.Namespace(), Name: d.Name()}
			newKey := catalog.OperatorNameKey{Namespace: n.Namespace(), Name: n.Name()}
			if len(base.State().OperatorNames().Buckets[oldKey]) != 1 || len(private.State().OperatorNames().Buckets[oldKey]) != 0 || len(private.State().OperatorNames().Buckets[newKey]) != 1 {
				t.Fatal("private operator index/root isolation")
			}
		}
	}
	mark := journal
	later := journal.Clone()
	for address := range journal.Descriptors {
		later.RecordDescriptor(base.State(), address, nil)
	}
	gone := later.PrivateRead(base.Owner(), base.State())
	for address := range journal.Descriptors {
		if gone.Get(address) != nil || mark.PrivateRead(base.Owner(), base.State()).Get(address) == nil {
			t.Fatal("savepoint journal/tombstone isolation")
		}
	}
	if later.CachedBase != base.State() || mark.PrivateRead(base.Owner(), base.State()) != private {
		t.Fatal("savepoint cache identity")
	}
}
