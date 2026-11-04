package tablecmds_test

import (
	"reflect"
	"slices"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
)

// Only setup actor A has administrative permission. R exercises independently
// declared schema, relation and sequence permissions, including explicit denies.
func sepgsqlSequenceEngine(t *testing.T, configure func(*sepgsql.SimulationConfig)) (*core.Engine, func() []sepgsql.AuditEvent) {
	t.Helper()
	var mu sync.Mutex
	var events []sepgsql.AuditEvent
	config := sepgsql.SimulationConfig{InstallSQL: true, AuditAllowed: true, Policy: sepgsql.Policy{
		ID: "sequence-create", Version: "1", Contexts: []string{"A", "R", "T", "P", "Q", "N", "U"}, InitialContext: "A", UnlabeledContext: "U",
		DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n",
		Rules: []sepgsql.Rule{
			{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "R", Target: "P", Class: "db_database", Allow: []string{"access", "getattr"}},
			{Source: "R", Target: "P", Class: "db_schema", Allow: []string{"search", "add_name", "remove_name", "getattr"}},
			{Source: "R", Target: "P", Class: "db_procedure", Allow: []string{"execute", "getattr"}},
			{Source: "R", Target: "P", Class: "db_table", Allow: []string{"create", "select", "insert", "update", "delete", "lock", "getattr", "setattr", "drop"}},
			{Source: "R", Target: "P", Class: "db_column", Allow: []string{"create", "select", "insert", "update", "getattr", "setattr", "drop"}},
			{Source: "R", Target: "P", Class: "db_sequence", Allow: []string{"create", "get_value", "next_value", "set_value", "getattr", "setattr", "drop"}},
			{Source: "R", Target: "Q", Class: "db_sequence", Allow: []string{"create", "get_value", "getattr"}},
			{Source: "R", Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}},
			{Source: "T", Target: "P", Class: "db_database", Allow: []string{"access", "getattr"}},
			{Source: "T", Target: "P", Class: "db_schema", Allow: []string{"search", "add_name"}},
			{Source: "T", Target: "P", Class: "db_procedure", Allow: []string{"execute"}},
		},
		CreateTransitions: []sepgsql.CreateTransition{{Source: "R", Parent: "P", Class: "db_sequence", Name: "selected", Result: "Q"}},
	}}
	if configure != nil {
		configure(&config)
	}
	config.Audit = func(event sepgsql.AuditEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	runtime, err := sepgsql.Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New()
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(runtime, true)
	if err == nil {
		e.InitializeSepgsqlSeedLabels()
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return e, func() []sepgsql.AuditEvent {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(events)
	}
}

func sepgsqlSequenceActor(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
	return s
}

func sepgsqlSequenceLabel(t *testing.T, read catalog.Read, address ids.ObjectAddress, want string) {
	t.Helper()
	d, ok := read.Relation(address)
	if !ok || d.RelationKind != catalog.SequenceRelation || d.Sequence == nil || len(d.Columns) != 3 {
		t.Fatal("not the genuine three-column sequence", d)
	}
	count := 0
	for key, record := range read.State().SecurityLabels() {
		if key.Object.ClassID != address.ClassID || key.Object.ObjectID != address.ObjectID {
			continue
		}
		count++
		if key.Object.SubID != 0 || key.Provider != "selinux" || !reflect.DeepEqual(record, &catalog.SecurityLabelRecord{Label: want, PolicyID: "sequence-create", PolicyVersion: "1"}) {
			t.Fatal("sequence association or invented column hook", key, record)
		}
	}
	if count != 1 {
		t.Fatal("whole sequence label count", count)
	}
}

func sepgsqlSequenceEvents(events []sepgsql.AuditEvent) []sepgsql.AuditEvent {
	var selected []sepgsql.AuditEvent
	for _, event := range events {
		if event.Class == "db_sequence" && slices.Contains(event.Required, "create") {
			selected = append(selected, event)
		}
	}
	return selected
}

// Sequence identities/revisions already reserved by real CREATE are monotonic,
// not rolled back. This checks publication, not allocator frontiers.
func sepgsqlSequenceAbsent(t *testing.T, e *core.Engine, namespace ids.ObjectAddress, name string) {
	t.Helper()
	read := e.CatalogRead()
	if read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name}) != (ids.ObjectAddress{}) {
		t.Fatal("failed sequence bound its name", name)
	}
	for address, generator := range e.Storage.Sequences {
		if generator == nil || read.Get(address) == nil {
			t.Fatal("orphan sequence generator", address)
		}
	}
	for key := range read.State().SecurityLabels() {
		whole := key.Object
		whole.SubID = 0
		if read.Get(whole) == nil {
			t.Fatal("orphan security label", key)
		}
	}
}
