package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
)

func TestRestoreSepgsqlRetiredColumns(t *testing.T) {
	for _, tc := range []struct {
		name, relation, label string
		attribute             int32
		duplicate, canceled   bool
		valid                 bool
	}{
		{name: "genuine", relation: "retired", attribute: 2, label: "S", valid: true},
		{name: "missing", relation: "missing", attribute: 2, label: "S"},
		{name: "live", relation: "retired", attribute: 1, label: "S"},
		{name: "absent", relation: "retired", attribute: 4, label: "S"},
		{name: "system", relation: "retired", attribute: -1, label: "S"},
		{name: "unsupported", relation: "retired_view", attribute: 2, label: "S"},
		{name: "unknown_context", relation: "retired", attribute: 2, label: "unknown"},
		{name: "duplicate", relation: "retired", attribute: 2, label: "S", duplicate: true},
		{name: "canceled", relation: "retired", attribute: 2, label: "S", canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := New()
			policy, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
				ID: "finish-retired", Version: "1", Contexts: []string{"A", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U",
				Rules:       []sepgsql.Rule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}},
				DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n",
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := source.InstallSepgsqlSimulation(policy, true); err != nil {
				t.Fatal(err)
			}
			e := source.RestoreStage()
			r := &restoreRun{stage: e}
			host := &restoreHost{target: source, run: r}
			op, done, err := host.Start(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			for _, sql := range []string{"CREATE TABLE public.retired(keep integer,gone text,tail integer)", "ALTER TABLE public.retired DROP COLUMN gone", "CREATE VIEW public.retired_view AS SELECT keep,tail FROM public.retired"} {
				if _, err := e.Execute(op, statement(t, sql), 0, nil); err != nil {
					t.Fatal(err)
				}
			}
			before := e.CatalogRead()
			address := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "retired"})
			relation := before.MustRelation(address)
			column := address
			column.SubID = 2
			key := catalog.SecurityLabelKey{Object: column, Provider: "selinux"}
			if before.State().SecurityLabel(key) != nil || relation.IndexForAttribute(2) >= 0 || !relation.ColumnMeta.Dropped[2].Dropped {
				t.Fatal("fixture did not produce a genuine unlabeled tombstone")
			}
			record := control.SepgsqlRetiredColumnLabel{Schema: "public", Relation: tc.relation, Attribute: tc.attribute, Label: tc.label}
			r.sepgsqlRequirement = &control.SepgsqlRequirement{ID: policy.ID(), Version: policy.Version(), Database: e.DatabaseName, RetiredColumns: []control.SepgsqlRetiredColumnLabel{record}}
			if tc.duplicate {
				r.sepgsqlRequirement.RetiredColumns = append(r.sepgsqlRequirement.RetiredColumns, record)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			err = finishRestoreSepgsql(r, ctx, op)
			if !tc.valid {
				if err == nil || e.CatalogRead().State() != before.State() || before.State().SecurityLabel(key) != nil {
					t.Fatal("rejected import published catalog state", err)
				}
				if tc.canceled && !errors.Is(err, context.Canceled) {
					t.Fatal("lost cancellation identity", err)
				}
				return
			}
			after := e.CatalogRead()
			label := after.State().SecurityLabel(key)
			if err != nil || label == nil || label.Label != "S" || label.PolicyID != policy.ID() || label.PolicyVersion != policy.Version() || after.Get(address) != relation || before.State().SecurityLabel(key) != nil {
				t.Fatal("private typed import lost immutable catalog identity", label, err)
			}
		})
	}
}
