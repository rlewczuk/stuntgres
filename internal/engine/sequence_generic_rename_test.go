package engine

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSequenceGenericRelationRenameAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE SEQUENCE s START 14 CACHE 1")
			ast, description := sequencePreparedDescription(t, e, s, "SELECT nextval('s')")
			ref := description.sequences.constants[0].ref
			before := e.catalog.state.MustRelation(ref.Address)
			generator := e.sequences[ref.Address]
			state := *generator
			utility := statement(t, "ALTER TABLE s RENAME TO identity")
			var expected *StatementDescription
			if prepared {
				_, d := sequencePreparedDescription(t, e, s, "ALTER TABLE s RENAME TO identity")
				expected = &d
			}
			r, err := sequencePreparedExecute(e, s, utility, expected)
			if err != nil {
				t.Fatal(err)
			}
			if r.Rows != nil {
				t.Fatal("utility unexpectedly returned rows")
			}
			after := e.catalog.state.MustRelation(ref.Address)
			if after.address != before.address || after.Owner != before.Owner || after.namespace != before.namespace || !reflect.DeepEqual(after.Sequence, before.Sequence) || !reflect.DeepEqual(after.Columns, before.Columns) {
				t.Fatal("rename changed identity/owner/options/shape")
			}
			if e.sequences[ref.Address] != generator || *generator != state {
				t.Fatal("rename replaced or changed generator")
			}
			_, err = sequencePreparedExecute(e, s, ast, &description)
			var got *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "s" does not exist`, Position: "16"}
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("held error=%#v (%v), want=%#v", got, err, want)
			}
			if *generator != state {
				t.Fatal("failed retained source allocated")
			}
			if after.SequencePlanStamp == before.SequencePlanStamp {
				t.Fatal("generic rename did not invalidate sequence stamp")
			}
			sequenceScalar(t, s, "SELECT nextval('identity')", 14)
		})
	}
}

func TestSequenceGenericColumnRenameAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, keyword := range []string{"COLUMN ", ""} {
			for _, source := range []string{"last_value", "missing", "ctid"} {
				q := "ALTER TABLE s RENAME " + keyword + source + " TO renamed"
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+q, func(t *testing.T) {
					e := New()
					op, done := roleOperation(t, e, "postgres")
					defer done()
					roleExecute(t, e, op, "CREATE SEQUENCE s START 14 CACHE 1")
					ref := roleTableRef(t, e, "s")
					before := e.catalog.state.Get(ref.Address)
					generator := e.sequences[ref.Address]
					state := *generator
					err := sequenceOwnerCommand(t, e, op, q, prepared)
					sequenceOwnerDiagnostic(t, err, "42809", `cannot rename columns of relation "s"`, "This operation is not supported for sequences.")
					if e.catalog.state.Get(ref.Address) != before || e.sequences[ref.Address] != generator || *generator != state {
						t.Fatal("rejected rename changed descriptor or generator")
					}
					r := roleExecute(t, e, op, "SELECT nextval('s')")
					values := tuples(r)
					r.Rows.Close()
					if !reflect.DeepEqual(values, [][]driver.Value{{int64(14)}}) {
						t.Fatal("rejected rename consumed progression", values)
					}
				})
			}
		}
	}
}

func TestSequenceGenericRenamePreservesCacheAndDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE TABLE owner_table(id INT)", "CREATE SEQUENCE s START 14 CACHE 7 OWNED BY owner_table.id", "SELECT nextval('s')"} {
				r := roleExecute(t, e, op, q)
				if r.Rows != nil {
					r.Rows.Close()
				}
			}
			ref := roleTableRef(t, e, "s")
			before := e.catalog.state.MustRelation(ref.Address)
			dependencies := descriptorReferences(before)
			generator := e.sequences[ref.Address]
			state := *generator
			if err := sequenceOwnerCommand(t, e, op, "ALTER TABLE s RENAME TO identity", prepared); err != nil {
				t.Fatal(err)
			}
			after := e.catalog.state.MustRelation(ref.Address)
			if after.address != before.address || after.Owner != before.Owner || !reflect.DeepEqual(after.Sequence, before.Sequence) || !reflect.DeepEqual(descriptorReferences(after), dependencies) || e.sequences[ref.Address] != generator || *generator != state {
				t.Fatal("rename changed ownership/dependencies/generator")
			}
			for _, q := range []string{"SELECT currval('identity')", "SELECT lastval()", "SELECT nextval('identity')"} {
				r := roleExecute(t, e, op, q)
				values := tuples(r)
				r.Rows.Close()
				want := int64(14)
				if q == "SELECT nextval('identity')" {
					want = 15
				}
				if !reflect.DeepEqual(values, [][]driver.Value{{want}}) {
					t.Fatal(q, values, want)
				}
			}
			if *generator != state {
				t.Fatal("rename discarded local cache tail")
			}
		})
	}
}

func TestSequenceGenericRenameStampExhaustion(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE SEQUENCE s START 14 CACHE 1")
			ref := roleTableRef(t, e, "s")
			before := e.catalog.state.Get(ref.Address)
			generator := e.sequences[ref.Address]
			state := *generator
			e.catalog.nextRevision = ids.DefinitionRevision(^uint64(0))
			err := sequenceOwnerCommand(t, e, op, "ALTER TABLE s RENAME TO identity", prepared)
			if err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" {
				t.Fatalf("stamp exhaustion error=%v", err)
			}
			if e.catalog.state.Get(ref.Address) != before || e.sequences[ref.Address] != generator || *generator != state {
				t.Fatal("exhausted rename published descriptor or generator")
			}
			if got := roleTableRef(t, e, "s"); got != ref {
				t.Fatal("exhausted rename changed source identity")
			}
		})
	}
}
