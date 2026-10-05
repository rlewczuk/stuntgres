package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestSQLDiagnosticSeverityAndRowDetail(t *testing.T) {
	err := sqlError("22003", "out of range")
	var got *sqlstate.Error
	if !errors.As(err, &got) || got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" || got.Schema != "" {
		t.Fatalf("SQL error fields = %#v", got)
	}
	for _, tc := range []struct {
		row  []driver.Value
		want string
	}{
		{[]driver.Value{nil, int64(-9223372036854775808), true, false, "a,b\nc"}, "Failing row contains (null, -9223372036854775808, t, f, a,b\nc)."},
		{[]driver.Value{strings.Repeat("x", 64)}, "Failing row contains (" + strings.Repeat("x", 64) + ")."},
		{[]driver.Value{strings.Repeat("x", 65)}, "Failing row contains (" + strings.Repeat("x", 64) + "...)."},
		{[]driver.Value{strings.Repeat("x", 63) + "é"}, "Failing row contains (" + strings.Repeat("x", 63) + "...)."},
	} {
		if detail := failingRowDetail(tc.row); detail != tc.want {
			t.Errorf("detail = %q, want %q", detail, tc.want)
		}
	}
}

type publicationConflictProvider struct {
	CatalogProvider
	publishWinner func(Descriptor)
}

func (p *publicationConflictProvider) Validate(view CatalogRead, d Descriptor) error {
	if err := p.CatalogProvider.Validate(view, d); err != nil {
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
	var loser ObjectAddress
	var winnerView CatalogRead
	e.providers[RelationClassID] = &publicationConflictProvider{
		CatalogProvider: e.providers[RelationClassID],
		publishWinner: func(d Descriptor) {
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
	if result.Count != 0 || result.Rows != nil || len(result.Columns) != 0 {
		t.Fatalf("losing Execute exposed result: %#v", result)
	}
	after := e.CatalogRead()
	if winnerView.state == nil || after.state != winnerView.state {
		t.Fatal("losing Execute replaced the winning catalog")
	}
	if _, ok := after.Descriptor(loser); ok || e.storage[loser] != nil {
		t.Fatal("losing Execute installed its descriptor or storage")
	}
	key := NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "conflict_rows"}
	refs := after.Lookup(key)
	if len(refs) != 1 || refs[0] == loser {
		t.Fatalf("winning name binding = %#v", refs)
	}
	d, ok := after.Descriptor(refs[0])
	if !ok {
		t.Fatal("winning descriptor is absent")
	}
	relation := d.(*relationDescriptor)
	if len(relation.columns) != 1 || relation.columns[0].Name != "kept" ||
		relation.columns[0].Type != parser.Text || relation.columns[0].Nullable {
		t.Fatalf("winning definition changed: %#v", relation.columns)
	}
	rows := tuples(run(t, e, "SELECT kept FROM conflict_rows"))
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != "winner" {
		t.Fatalf("winning storage changed: %#v", rows)
	}
}
