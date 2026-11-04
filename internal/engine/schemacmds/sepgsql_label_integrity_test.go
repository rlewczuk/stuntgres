package schemacmds_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Each expected diagnostic is independent of the executor. Retain the entire
// catalog/role/label roots and physical savepoint, not just a count of labels.
func sepgsqlIntegrityReject(t *testing.T, e *core.Engine, op *session.Operation, stmt parser.Statement, address ids.ObjectAddress, locked bool, code, message, detail string) {
	t.Helper()
	before, roles := e.CatalogRead(), e.RoleStore.Published
	labels := before.State().SecurityLabelRoot()
	descriptor := before.Get(address)
	oid, revision, principal := e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
	identity, authorizationRevision := op.RoleState(), op.Session.Authorization.Revision
	subject, physical := op.Session.Sepgsql.Current(), op.Session.Settings.Savepoint()
	generators := make(map[ids.ObjectAddress]any, len(e.Storage.Sequences))
	for a, g := range e.Storage.Sequences {
		generators[a] = *g
	}
	description, err := e.Describe(op, stmt)
	if err != nil || description.Parameters.Count != 0 || len(description.Parameters.Types) != 0 || len(description.Results) != 0 {
		t.Fatal("utility description must succeed without parameters/results", description, err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("integrity assertion ended before Finish"))
	mark := scope.MarkSepgsqlObservations()
	if mark != (access.SepgsqlObservationMark{}) || reflect.ValueOf(op).Elem().FieldByName("sepgsqlAudit").Len() != 0 {
		t.Fatal("integrity fixture began with provider observations or queued audit")
	}
	label := stmt.(*parser.SecurityLabel)
	var searches []access.SepgsqlObservation
	switch label.Kind {
	case "role":
		// Role lookup never searches a namespace or enters the label provider.
	case "type", "domain", "column":
		// The real type/relation resolvers consult the default explicit public
		// path, then implicit pg_catalog. Qualified type lookup captures that
		// same path first; its explicit namespace check deduplicates the record.
		subject, generation, frame, depth := op.Session.Sepgsql.PhaseSnapshot()
		for _, namespace := range []ids.ObjectAddress{
			{ClassID: ids.NamespaceClassID, ObjectID: 2200},
			{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID},
		} {
			descriptor := before.Get(namespace)
			if descriptor == nil {
				t.Fatal("integrity fixture requires genuine lookup namespace", namespace)
			}
			key := catalog.SecurityLabelKey{Object: namespace, Provider: "selinux"}
			record := before.State().SecurityLabel(key)
			target := e.Sepgsql.Unlabeled()
			if record != nil {
				target = record.Label
			}
			searches = append(searches, access.SepgsqlObservation{
				Reference:    e.Catalog.Domain.Ref(before.Epoch(), namespace),
				ActualObject: descriptor, ExpectedObject: descriptor, Key: key,
				ActualLabel: record, ExpectedLabel: record, Policy: e.Sepgsql,
				Subject: subject, Target: target, Class: "db_schema", Mask: sepgsql.Permission("db_schema", "search"),
				Generation: generation, Frame: frame, Depth: depth,
			})
		}
	default:
		t.Fatal("integrity fixture has no independent resolver expectation", label.Kind)
	}
	result, executeErr := scope.Execute(stmt, 0, nil)
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	var got *sqlstate.Error
	if !errors.As(executeErr, &got) || *got != want {
		t.Fatalf("integrity diagnostic: got %#v (%v), want %#v", got, executeErr, want)
	}
	if locked {
		whole := address
		whole.SubID = 0
		if !e.Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: e.Catalog.Domain.Ref(before.Epoch(), whole)}, sepgsqlResolutionMode(whole)) {
			t.Fatal("generic integrity/provider rejection preceded genuine whole-object lock", address)
		}
	} else {
		// Role and wrong-domain-kind rejection must not fabricate a label target.
		for _, entry := range e.Locks.Inspect().Resources {
			if len(entry.Pending) != 0 {
				t.Fatal("early rejection queued a target lock")
			}
		}
	}
	if label.Kind == "role" || label.Kind == "domain" && code == "42809" {
		records, err := e.Locks.PGLocks(e.Catalog.Domain, before.Epoch())
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Kind == lock.ObjectRecord || record.Kind == lock.RelationRecord {
				t.Fatal("role/wrong-domain rejection fabricated a label target lock", record)
			}
		}
	}
	observations := reflect.ValueOf(scope).Elem().FieldByName("sepgsqlObservations")
	expectedMark := access.SepgsqlObservationMark{Observations: len(searches)}
	if scope.MarkSepgsqlObservations() != expectedMark || observations.Len() != len(searches) {
		t.Fatal("generic rejection produced observations beyond exact resolver searches", scope.MarkSepgsqlObservations(), expectedMark)
	}
	for i, expected := range searches {
		if !observations.Index(i).Equal(reflect.ValueOf(expected)) {
			t.Fatalf("generic rejection observation %d is not its genuine namespace search: got %v, want %#v", i, observations.Index(i), expected)
		}
	}
	if reflect.ValueOf(op).Elem().FieldByName("sepgsqlAudit").Len() != 0 {
		t.Fatal("generic rejection queued simulated provider audit")
	}
	if scope.Catalog != nil || scope.Completed || op.Session.Transactions.Catalog != nil || result.Rows != nil || result.Count != 0 || result.CommandTag != "" {
		t.Fatal("rejection staged state or returned a result", result)
	}
	if err := scope.Finish(executeErr); err != executeErr {
		t.Fatal("Finish replaced semantic diagnostic", err)
	}
	if e.CatalogRead().State() != before.State() || e.CatalogRead().Get(address) != descriptor || before.State().SecurityLabelRoot() != labels ||
		e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal ||
		op.RoleState() != identity || op.Session.Authorization.Revision != authorizationRevision || op.Session.Sepgsql.Current() != subject ||
		!reflect.DeepEqual(op.Session.Settings.Savepoint(), physical) || op.Session.Transactions.Catalog != nil {
		t.Fatal("generic rejection changed catalog, labels, roles, allocators, subject or private journal")
	}
	if len(e.Storage.Sequences) != len(generators) {
		t.Fatal("generic rejection changed sequence generator population")
	}
	for a, g := range e.Storage.Sequences {
		if !reflect.DeepEqual(generators[a], *g) {
			t.Fatal("generic rejection touched sequence generator", a)
		}
	}
	enginetest.LockEmpty(t, e)
}

func TestSepgsqlSimulationGenericLabelRoleOwnership(t *testing.T) {
	e := sepgsqlResolutionEngine(t)
	for _, q := range []string{
		"CREATE ROLE integrity_plain LOGIN", "CREATE ROLE integrity_creator LOGIN CREATEROLE", "CREATE ROLE integrity_adminonly LOGIN", "CREATE ROLE integrity_member LOGIN CREATEROLE",
		"CREATE ROLE integrity_target", "CREATE ROLE integrity_super SUPERUSER", "CREATE ROLE integrity_bridge", "CREATE ROLE integrity_indirect LOGIN CREATEROLE",
		"GRANT integrity_target TO integrity_adminonly WITH ADMIN OPTION", "GRANT integrity_target TO integrity_member",
		"GRANT integrity_target TO integrity_bridge WITH ADMIN OPTION", "GRANT integrity_bridge TO integrity_indirect",
	} {
		enginetest.Run(t, e, q)
	}
	const create = "The current user must have the CREATEROLE attribute."
	const super = "The current user must have the SUPERUSER attribute."
	admin := func(name string) string {
		return fmt.Sprintf(`The current user must have the ADMIN option on role "%s".`, name)
	}
	const unsupported = "SELinux: security labels are not supported for this type of object"
	for _, tc := range []struct{ actor, target, code, message, detail string }{
		{"integrity_plain", "integrity_plain", "42501", "permission denied", create},
		{"integrity_creator", "integrity_creator", "42501", "permission denied", admin("integrity_creator")},
		{"integrity_creator", "integrity_target", "42501", "permission denied", admin("integrity_target")},
		{"integrity_adminonly", "integrity_target", "42501", "permission denied", create},
		{"integrity_member", "integrity_target", "42501", "permission denied", admin("integrity_target")},
		{"integrity_indirect", "integrity_target", "0A000", unsupported, ""},
		{"integrity_plain", "integrity_super", "42501", "permission denied", super},
		{"integrity_creator", "integrity_super", "42501", "permission denied", super},
		{"postgres", "integrity_super", "0A000", unsupported, ""},
		{"postgres", "integrity_target", "0A000", unsupported, ""},
		{"integrity_plain", "integrity_absent", "42704", `role "integrity_absent" does not exist`, ""},
	} {
		t.Run(tc.actor+"/"+tc.target, func(t *testing.T) {
			op, done := enginetest.RoleOperation(t, e, tc.actor)
			defer done()
			for _, provider := range []string{"FOR selinux ", ""} {
				for _, label := range []string{"NULL", "''", "'invalid'", "'P'"} {
					stmt := enginetest.Statement(t, "SECURITY LABEL "+provider+"ON ROLE "+tc.target+" IS "+label)
					sepgsqlIntegrityReject(t, e, op, stmt, ids.ObjectAddress{}, false, tc.code, tc.message, tc.detail)
				}
			}
		})
	}
	// One syntax object survives actual role-store publications. No new
	// principal, role revision or permission may be retained by Describe.
	op, done := enginetest.RoleOperation(t, e, "integrity_creator")
	defer done()
	stmt := enginetest.Statement(t, "SECURITY LABEL FOR selinux ON ROLE integrity_target IS 'invalid'")
	for _, tc := range []struct{ setup, code, message, detail string }{
		{"GRANT integrity_target TO integrity_creator WITH ADMIN OPTION", "0A000", unsupported, ""},
		{"ALTER ROLE integrity_creator NOCREATEROLE", "42501", "permission denied", create},
		{"ALTER ROLE integrity_creator CREATEROLE", "0A000", unsupported, ""},
		{"REVOKE ADMIN OPTION FOR integrity_target FROM integrity_creator", "42501", "permission denied", admin("integrity_target")},
		{"GRANT integrity_target TO integrity_creator WITH ADMIN OPTION", "0A000", unsupported, ""},
		{"ALTER ROLE integrity_target SUPERUSER", "42501", "permission denied", super},
		{"ALTER ROLE integrity_target NOSUPERUSER", "0A000", unsupported, ""},
	} {
		enginetest.Run(t, e, tc.setup)
		sepgsqlIntegrityReject(t, e, op, stmt, ids.ObjectAddress{}, false, tc.code, tc.message, tc.detail)
	}
}

func TestSepgsqlSimulationGenericLabelDomainKind(t *testing.T) {
	e := sepgsqlResolutionEngine(t)
	for _, q := range []string{"CREATE ROLE integrity_reader LOGIN", `CREATE TYPE "MixedType" AS ENUM ('v')`, "CREATE DOMAIN integrity_array_domain AS integer[]"} {
		enginetest.Run(t, e, q)
	}
	for _, actor := range []string{"postgres", "integrity_reader"} {
		op, done := enginetest.RoleOperation(t, e, actor)
		for _, tc := range []struct{ syntax, spelling string }{
			{"resolution_type", "resolution_type"}, {"public.resolution_type", "public.resolution_type"}, {`public."MixedType"`, "public.MixedType"},
			{"INTEGER", "pg_catalog.int4"}, {"varchar(7)", "pg_catalog.varchar"}, {"resolution_type[]", "resolution_type[]"},
			{"resolution_domain[][]", "resolution_domain[]"},
		} {
			for _, provider := range []string{"FOR selinux ", ""} {
				stmt := enginetest.Statement(t, "SECURITY LABEL "+provider+"ON DOMAIN "+tc.syntax+" IS 'invalid'")
				sepgsqlIntegrityReject(t, e, op, stmt, ids.ObjectAddress{}, false, "42809", `"`+tc.spelling+`" is not a domain`, "")
			}
		}
		for _, name := range []string{"resolution_domain", "integrity_array_domain"} {
			address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, Name: name})
			code, message, locked := "0A000", "SELinux: security labels are not supported for this type of object", true
			if actor != "postgres" {
				code, message, locked = "42501", "must be owner of type "+name, false
			}
			sepgsqlIntegrityReject(t, e, op, enginetest.Statement(t, "SECURITY LABEL FOR selinux ON DOMAIN "+name+" IS NULL"), address, locked, code, message, "")
		}
		sepgsqlIntegrityReject(t, e, op, enginetest.Statement(t, "SECURITY LABEL FOR selinux ON DOMAIN integrity_absent IS NULL"), ids.ObjectAddress{}, false, "42704", `type "integrity_absent" does not exist`, "")
		for _, name := range []string{"resolution_type", "resolution_domain"} {
			code, message := "0A000", "SELinux: security labels are not supported for this type of object"
			if actor != "postgres" {
				code, message = "42501", "must be owner of type "+name
			}
			sepgsqlIntegrityReject(t, e, op, enginetest.Statement(t, "SECURITY LABEL FOR selinux ON TYPE "+name+" IS NULL"), ids.ObjectAddress{}, false, code, message, "")
		}
		done()
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.Run(t, e, "CREATE DOMAIN integrity_replaced AS integer")
	stmt := enginetest.Statement(t, "SECURITY LABEL FOR selinux ON DOMAIN integrity_replaced IS NULL")
	sepgsqlIntegrityReject(t, e, op, stmt, ids.ObjectAddress{}, false, "0A000", "SELinux: security labels are not supported for this type of object", "")
	enginetest.Run(t, e, "DROP DOMAIN integrity_replaced")
	enginetest.Run(t, e, "CREATE TYPE integrity_replaced AS ENUM ('v')")
	sepgsqlIntegrityReject(t, e, op, stmt, ids.ObjectAddress{}, false, "42809", `"integrity_replaced" is not a domain`, "")
}

func TestSepgsqlSimulationGenericLabelColumnIntegrity(t *testing.T) {
	e := sepgsqlResolutionEngine(t)
	for _, q := range []string{"CREATE ROLE integrity_reader LOGIN", "CREATE ROLE integrity_owner LOGIN", "CREATE SEQUENCE integrity_sequence START 17 INCREMENT 3 CACHE 4", "ALTER SEQUENCE integrity_sequence OWNER TO integrity_owner", "CREATE INDEX integrity_index ON resolution_table(v)",
		"CREATE TABLE integrity_partition(v text) PARTITION BY RANGE(v)", "CREATE INDEX integrity_partition_index ON integrity_partition USING spgist(v text_ops)",
		"CREATE VIEW integrity_view AS SELECT v FROM resolution_table", "CREATE MATERIALIZED VIEW integrity_mat AS SELECT v FROM resolution_table", "CREATE TYPE integrity_composite AS (v integer)",
		"CREATE FOREIGN DATA WRAPPER integrity_fdw NO HANDLER", "CREATE SERVER integrity_server FOREIGN DATA WRAPPER integrity_fdw", "CREATE FOREIGN TABLE integrity_foreign(v integer) SERVER integrity_server"} {
		enginetest.Run(t, e, q)
	}
	for _, actor := range []string{"postgres", "integrity_owner", "integrity_reader"} {
		op, done := enginetest.RoleOperation(t, e, actor)
		for _, column := range []string{"last_value", "log_cnt", "is_called", "absent"} {
			for _, label := range []string{"NULL", "''", "'invalid'", "'P'"} {
				code, message, detail, locked := "42809", `cannot set security label on relation "integrity_sequence"`, "This operation is not supported for sequences.", true
				if actor == "integrity_reader" {
					code, message, detail, locked = "42501", "must be owner of sequence integrity_sequence", "", false
				}
				if column == "absent" {
					code, message, detail, locked = "42703", `column "absent" of relation "integrity_sequence" does not exist`, "", false
				}
				address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, Name: "integrity_sequence"})
				for _, provider := range []string{"FOR selinux ", ""} {
					sepgsqlIntegrityReject(t, e, op, enginetest.Statement(t, "SECURITY LABEL "+provider+"ON COLUMN integrity_sequence."+column+" IS "+label), address, locked, code, message, detail)
				}
			}
		}
		done()
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, name := range []string{"integrity_index", "integrity_partition_index"} {
		detail := "This operation is not supported for indexes."
		if name == "integrity_partition_index" {
			detail = "This operation is not supported for partitioned indexes."
		}
		view := e.CatalogRead()
		address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, Name: name})
		index := view.MustRelation(address)
		if index.RelationKind != catalog.IndexRelation || index.SQLIndex == nil ||
			(view.MustRelation(index.SQLIndex.Parent).Partition != nil) != (name == "integrity_partition_index") {
			t.Fatal("integrity fixture requires genuine ordinary/partitioned index", name)
		}
		sepgsqlIntegrityReject(t, e, op, enginetest.Statement(t, "SECURITY LABEL FOR selinux ON COLUMN "+name+".v IS 'invalid'"), address, true, "42809", `cannot set security label on relation "`+name+`"`, detail)
	}
	for _, name := range []string{"integrity_view", "integrity_mat", "integrity_composite", "integrity_foreign"} {
		sepgsqlIntegrityReject(t, e, op, enginetest.Statement(t, "SECURITY LABEL FOR selinux ON COLUMN "+name+".v IS 'invalid'"), ids.ObjectAddress{}, false, "0A000", "SELinux: security labels are not supported for this type of object", "")
	}
	// Ordinary and partitioned table columns still reach the existing provider.
	enginetest.Run(t, e, "SECURITY LABEL FOR selinux ON COLUMN resolution_table.v IS 'P'")
	enginetest.Run(t, e, "SECURITY LABEL FOR selinux ON COLUMN integrity_partition.v IS 'P'")
}

func sepgsqlResolutionLockTargets(e *core.Engine) []struct {
	sql     string
	address ids.ObjectAddress
} {
	targets := sepgsqlResolutionTargets(e)
	return append(targets, struct {
		sql     string
		address ids.ObjectAddress
	}{
		sql: "COLUMN resolution_sequence.last_value",
		address: e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID,
			Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, Name: "resolution_sequence"}),
	})
}

func sepgsqlIntegrityRelationCapture(t *testing.T, e *core.Engine, address ids.ObjectAddress, holder, waiter *lock.Ticket, mode lock.Mode) {
	t.Helper()
	records, err := e.Locks.PGLocks(e.Catalog.Domain, e.CatalogRead().Epoch())
	if err != nil {
		t.Fatal(err)
	}
	grants, waits := 0, 0
	for _, record := range records {
		if record.Kind != lock.RelationRecord || record.Object != uint32(address.ObjectID) {
			continue
		}
		owner := lock.RecordOwner{Session: holder.Ref().Session().ID(), Local: holder.Ref().ID()}
		if record.Waiting {
			if waiter == nil {
				t.Fatal("unexpected sequence waiter", record)
			}
			waits++
			owner = lock.RecordOwner{Session: waiter.Ref().Session().ID(), Local: waiter.Ref().ID()}
		} else {
			grants++
		}
		if record.Owner != owner || record.Mode != mode || record.Shared || record.Subobject != 0 || record.Class != 0 {
			t.Fatal("sequence column did not lock its genuine whole relation", record)
		}
		source := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.LocksOID})
		rows, err := pgcatalog.LockRows(t.Context(), source, uint32(e.CatalogRead().Owner().DatabaseAddress().ObjectID), []lock.Record{record}, nil)
		if err != nil || len(rows) != 1 {
			t.Fatal("sequence lock projection", rows, err)
		}
		row := rows[0]
		kind, _ := row[0].Text()
		relation, _ := row[2].OID()
		publicMode, _ := row[12].Text()
		granted, _ := row[13].Boolean()
		if kind != "relation" || relation != uint32(address.ObjectID) || publicMode != "ShareUpdateExclusiveLock" || granted == record.Waiting ||
			!row[7].IsNull() || !row[8].IsNull() || !row[9].IsNull() || row[15].IsNull() == record.Waiting {
			t.Fatal("sequence pg_locks identity/mode/null matrix", row)
		}
	}
	wantWaits := 0
	if waiter != nil {
		wantWaits = 1
	}
	if grants != 1 || waits != wantWaits {
		t.Fatal("sequence lock projection lost actual grant/wait", grants, waits)
	}
}
