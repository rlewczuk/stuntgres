package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
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
			e := New()
			run(t, e, "CREATE FUNCTION txr_provider(integer,integer) RETURNS integer RETURN $1+$2")
			run(t, e, "CREATE DOMAIN txr_type AS integer")
			run(t, e, "CREATE FUNCTION txr_domain_step(txr_type,integer) RETURNS txr_type RETURN ($1+$2)::txr_type")
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
			run(t, e, q)
			childName := "txr_child"
			if family == "operator" || family == "operator-type" {
				childName = "#~#"
			}
			child := transactionalRoutineDescriptor(t, e, childName)
			current := e.CatalogRead().State()
			candidate := catalog.CloneState(current)
			candidate.RemoveDescriptor(provider.Address())
			observations := []session.DependencyObservation{{Base: base, Targets: []ids.ObjectAddress{provider.Address()}}}
			if err := core.CatalogDependencyPreflight(current, candidate, observations, nil); err == nil {
				t.Fatal("new surviving hard edge escaped retirement")
			}
			// Expression observations also protect retained-message admission
			// before its removals are overlaid; type edges require absence.
			existingErr := core.CatalogDependencyPreflight(current, current, observations, nil)
			if provider.Address().ClassID == ids.TypeClassID {
				if existingErr != nil {
					t.Fatal("type edge fenced a still-present target", existingErr)
				}
			} else if existingErr == nil {
				t.Fatal("new expression edge escaped pre-overlay observation")
			}
			removed := catalog.CloneState(candidate)
			removed.RemoveDescriptor(child.Address())
			if err := core.CatalogDependencyPreflight(current, removed, observations, nil); err != nil {
				t.Fatal("removed owner still fenced", err)
			}
			if err := core.CatalogDependencyPreflight(current, candidate, []session.DependencyObservation{{Base: current, Targets: []ids.ObjectAddress{provider.Address()}}}, nil); err != nil {
				t.Fatal("unchanged committed survivor treated as late", err)
			}
			unrelated := []session.DependencyObservation{{Base: base, Targets: []ids.ObjectAddress{{ClassID: ids.ProcedureClassID, ObjectID: 999999}}}}
			if err := core.CatalogDependencyPreflight(current, candidate, unrelated, nil); err != nil {
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
			if err := core.CatalogDependencyPreflight(current, rewired, observations, nil); err != nil {
				t.Fatal("legitimate candidate rewire rejected", err)
			}
		})
	}
	t.Run("reciprocal-links-are-not-hard-edges", func(t *testing.T) {
		ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		target := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 99001}, ObjectNamespace: ns, ObjectName: "#~#", Left: 23, Right: 23}
		base := catalogStateFixture(1, map[ids.ObjectAddress]catalog.Descriptor{target.Address(): target}, nil)
		child := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 99002}, ObjectNamespace: ns, ObjectName: "#?#", Left: 23, Right: 23, Commutator: target.Address().ObjectID, Negator: target.Address().ObjectID}
		current := catalog.CloneState(base)
		current.SetDescriptor(child.Address(), child)
		candidate := catalog.CloneState(current)
		candidate.RemoveDescriptor(target.Address())
		if err := core.CatalogDependencyPreflight(current, candidate, []session.DependencyObservation{{Base: base, Targets: []ids.ObjectAddress{target.Address()}}}, nil); err != nil {
			t.Fatal("reciprocal metadata became dependency", err)
		}
	})
}

func TestTransactionalRoutineJournalIsolation(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE FUNCTION txr_provider(integer,integer) RETURNS integer RETURN $1+$2", "CREATE FUNCTION txr_child(x integer DEFAULT txr_provider(2,3)) RETURNS integer RETURN x", "CREATE AGGREGATE txr_sum(integer) (SFUNC=txr_provider,STYPE=integer,INITCOND='0')", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=txr_provider)"} {
		run(t, e, q)
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
