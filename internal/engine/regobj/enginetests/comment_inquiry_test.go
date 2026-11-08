package regobj_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func TestProcedureCommentInquiry(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	oid, err := sqltype.OIDValue(types[sqltype.Oid], 1346)
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1346},
		ObjectName:    "pow",
		Comment:       "exponentiation",
	}
	state := catalog.NewState(1)
	state.SetDescriptor(d.Address(), d)
	retained := catalog.NewRead(nil, state)
	inquire := func(view catalog.Read, kind functions.CommentInquiryKind, class, want string) {
		t.Helper()
		values := [2]sqltype.Value{oid, sqltype.String(types[sqltype.Name], class)}
		got, err := regobj.CommentText(view, authorization.Read{}, kind, values)
		if err != nil || got != want {
			t.Fatalf("comment kind %v class %q = %q, %v; want %q", kind, class, got, err, want)
		}
	}
	inquire(retained, functions.ObjectDescription, "pg_proc", "exponentiation")
	inquire(retained, functions.LegacyDescription, "", "exponentiation")
	inquire(retained, functions.ObjectDescription, "pg_class", "")
	inquire(retained, functions.SharedDescription, "pg_proc", "")

	// Current removal stays absent; a retained catalog still owns its old text.
	cleared := *d
	cleared.Comment = ""
	next := catalog.CloneState(state)
	next.SetDescriptor(d.Address(), &cleared)
	inquire(catalog.NewRead(nil, next), functions.ObjectDescription, "pg_proc", "")
	inquire(catalog.NewRead(nil, next), functions.LegacyDescription, "", "")
	inquire(retained, functions.ObjectDescription, "pg_proc", "exponentiation")
	next.RemoveDescriptor(d.Address())
	inquire(catalog.NewRead(nil, next), functions.ObjectDescription, "pg_proc", "")
}

// inquiryTriggerFacts is a minimal TriggersPayload for inquiry liveness.
type inquiryTriggerFacts struct{ oids []ids.OID }

func (p inquiryTriggerFacts) Validate(catalog.Read, *catalog.Relation) error { return nil }
func (p inquiryTriggerFacts) Len() int                                       { return len(p.oids) }
func (p inquiryTriggerFacts) OID(i int) ids.OID                              { return p.oids[i] }
func (inquiryTriggerFacts) ConstraintOID(int) ids.OID                        { return 0 }
func (inquiryTriggerFacts) ConstraintName(int) string                        { return "" }
func (p inquiryTriggerFacts) Visit(visit func(catalog.TriggerFact) error) error {
	for _, oid := range p.oids {
		if err := visit(catalog.TriggerFact{OID: oid, Name: "trg"}); err != nil {
			return err
		}
	}
	return nil
}

func TestTriggerCommentInquiry(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	const triggerOID ids.OID = 700
	oid, err := sqltype.OIDValue(types[sqltype.Oid], uint32(triggerOID))
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 500},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200},
		ObjectName:      "t",
		RelationKind:    catalog.TableRelation,
		Triggers:        inquiryTriggerFacts{oids: []ids.OID{triggerOID}},
		TriggerComments: map[ids.OID]string{triggerOID: "trigger text"},
	}
	state := catalog.NewState(1)
	state.SetDescriptor(relation.Address(), relation)
	retained := catalog.NewRead(nil, state)
	inquire := func(view catalog.Read, kind functions.CommentInquiryKind, class, want string) {
		t.Helper()
		values := [2]sqltype.Value{oid, sqltype.String(types[sqltype.Name], class)}
		got, err := regobj.CommentText(view, authorization.Read{}, kind, values)
		if err != nil || got != want {
			t.Fatalf("comment kind %v class %q = %q, %v; want %q", kind, class, got, err, want)
		}
	}
	inquire(retained, functions.ObjectDescription, "pg_trigger", "trigger text")
	inquire(retained, functions.ObjectDescription, "pg_class", "")
	inquire(retained, functions.ObjectDescription, "pg_index", "")
	inquire(retained, functions.LegacyDescription, "", "trigger text")

	// A cleared entry and a stale entry (payload trigger gone) both read back
	// as absent; the retained catalog keeps its text.
	cleared := *relation
	cleared.TriggerComments = map[ids.OID]string{triggerOID: ""}
	next := catalog.CloneState(state)
	next.SetDescriptor(relation.Address(), &cleared)
	inquire(catalog.NewRead(nil, next), functions.ObjectDescription, "pg_trigger", "")
	inquire(catalog.NewRead(nil, next), functions.LegacyDescription, "", "")

	stale := *relation
	stale.Triggers = nil
	staleNext := catalog.CloneState(state)
	staleNext.SetDescriptor(relation.Address(), &stale)
	inquire(catalog.NewRead(nil, staleNext), functions.ObjectDescription, "pg_trigger", "")
	inquire(catalog.NewRead(nil, staleNext), functions.LegacyDescription, "", "")
	inquire(retained, functions.ObjectDescription, "pg_trigger", "trigger text")
}
