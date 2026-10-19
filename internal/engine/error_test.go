package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSQLDiagnosticSeverityAndRowDetail(t *testing.T) {
	err := sqlError("22003", "out of range")
	var got *sqlstate.Error
	if !errors.As(err, &got) || got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" || got.Schema != "" {
		t.Fatalf("SQL error fields = %#v", got)
	}
	e := New()
	text := testUsage(e, sqltype.Text).Type
	big := testUsage(e, sqltype.Int8).Type
	boolean := testUsage(e, sqltype.Bool).Type
	minimum, _ := sqltype.Integer(big, -9223372036854775808)
	for _, tc := range []struct {
		row  []sqltype.Value
		want string
	}{
		{[]sqltype.Value{sqltype.Null(text), minimum, sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false), sqltype.String(text, "a,b\nc")}, "Failing row contains (null, -9223372036854775808, t, f, a,b\nc)."},
		{[]sqltype.Value{sqltype.String(text, strings.Repeat("x", 64))}, "Failing row contains (" + strings.Repeat("x", 64) + ")."},
		{[]sqltype.Value{sqltype.String(text, strings.Repeat("x", 65))}, "Failing row contains (" + strings.Repeat("x", 64) + "...)."},
		{[]sqltype.Value{sqltype.String(text, strings.Repeat("x", 63)+"é")}, "Failing row contains (" + strings.Repeat("x", 63) + "...)."},
	} {
		if detail, err := failingRowDetail(tc.row, sqltype.OutputEnvironment{}, nil, nil, nil); err != nil || detail != tc.want {
			t.Errorf("detail = %q, want %q", detail, tc.want)
		}
	}
}

type publicationConflictProvider struct {
	catalog.Provider
	publishWinner func(catalog.Descriptor)
}

func (p *publicationConflictProvider) Validate(view catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(view, d); err != nil {
		return err
	}
	if publish := p.publishWinner; publish != nil {
		p.publishWinner = nil
		publish(d)
	}
	return nil
}

func TestErrorFieldsExecutePublicationConflict(t *testing.T) {
	e := New()
	var loser ids.ObjectAddress
	var winnerView catalog.Read
	e.catalog.Providers[ids.RelationClassID] = &publicationConflictProvider{
		Provider: e.catalog.Providers[ids.RelationClassID],
		publishWinner: func(d catalog.Descriptor) {
			loser = d.Address()
			// Validation runs outside the engine lock. Publish a competing real
			// CREATE and INSERT before the outer Execute attempts publication.
			run(t, e, "CREATE TABLE conflict_rows (kept TEXT NOT NULL)")
			run(t, e, "INSERT INTO conflict_rows VALUES ('winner')")
			winnerView = e.CatalogRead()
		},
	}
	result, err := testExecute(e, context.Background(), statement(t, "CREATE TABLE conflict_rows (lost INT)"), nil)
	var got *sqlstate.Error
	want := sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07",
		Message: `relation "conflict_rows" already exists`,
	}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("losing Execute diagnostic = %#v (%v), want %#v", got, err, want)
	}
	if result.Count != 0 || result.Rows != nil {
		t.Fatalf("losing Execute exposed result: %#v", result)
	}
	after := e.CatalogRead()
	if winnerView.State() == nil || after.State() != winnerView.State() {
		t.Fatal("losing Execute replaced the winning catalog")
	}
	if _, ok := after.Descriptor(loser); ok || e.storage.relations[loser] != nil {
		t.Fatal("losing Execute installed its descriptor or storage")
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "conflict_rows"}
	refs := after.Lookup(key)
	if len(refs) != 1 || refs[0] == loser {
		t.Fatalf("winning name binding = %#v", refs)
	}
	d, ok := after.Descriptor(refs[0])
	if !ok {
		t.Fatal("winning descriptor is absent")
	}
	relation := d.(*catalog.Relation)
	if len(relation.Columns) != 1 || relation.Columns[0].Name != "kept" ||
		relation.Columns[0].Usage.Type.ID() != sqltype.Text || relation.Columns[0].Nullable {
		t.Fatalf("winning definition changed: %#v", relation.Columns)
	}
	rows := tuples(run(t, e, "SELECT kept FROM conflict_rows"))
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != "winner" {
		t.Fatalf("winning storage changed: %#v", rows)
	}
}

func TestBooleanDiagnosticsBinding(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE b(id INT,flag BOOL)")
	for _, populated := range []bool{false, true} {
		if populated {
			run(t, e, "INSERT INTO b VALUES (99,TRUE)")
		}
		for _, tc := range []struct{ query, position string }{
			{"SELECT id FROM b WHERE flag='o'", "29"},
			{"/*é*/ SELECT id FROM b WHERE flag='o'", "35"},
		} {
			for _, execute := range []bool{false, true} {
				stmt := statement(t, tc.query)
				var err error
				if execute {
					_, err = testExecute(e, context.Background(), stmt, nil)
				} else {
					_, err = testDescribe(e, stmt)
				}
				var got *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
					Message: `invalid input syntax for type boolean: "o"`, Position: tc.position}
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("binding %q = %#v (%v), want %#v", tc.query, got, err, want)
				}
			}
		}
	}
	_, err := testDescribe(e, statement(t, "SELECT id FROM b WHERE id='bad'"))
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
		Message: `invalid input syntax for type integer: "bad"`}
	if !errors.As(err, &got) || got.Severity != want.Severity || got.SeverityUnlocalized != want.SeverityUnlocalized || got.Code != want.Code || got.Message != want.Message {
		t.Fatalf("integer seam changed: %#v (%v)", got, err)
	}
	_, err = testDescribe(e, statement(t, "SELECT id FROM b WHERE flag=1"))
	if !errors.As(err, &got) || got.Code != "42883" {
		t.Fatalf("operator mismatch changed: %#v (%v)", got, err)
	}
}
