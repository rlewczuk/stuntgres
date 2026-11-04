package core_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
)

func sepgsqlRestoreconPolicy(t *testing.T, audit func(sepgsql.AuditEvent), denyPeer bool) *sepgsql.Runtime {
	t.Helper()
	rules := []sepgsql.Rule{
		{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
	}
	if denyPeer {
		rules = append(rules, sepgsql.Rule{Source: "A", Target: "U", Class: "db_database", Deny: []string{"setattr", "relabelfrom"}})
	}
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, AuditAllowed: true, Audit: audit, Policy: sepgsql.Policy{
		ID: "restorecon-owner", Version: "1", Contexts: []string{"A", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U",
		DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n",
		Rules:       rules,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func sepgsqlRestoreconInstall(t *testing.T, e *core.Engine, policy *sepgsql.Runtime) {
	t.Helper()
	e.Mu.Lock()
	err := e.InstallSepgsqlSimulation(policy, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSepgsqlSimulationRestoreconScopeAndOrder(t *testing.T) {
	e := engine.NewWithSettings(nil, "restorecon_owner", false)
	var events []sepgsql.AuditEvent
	var auditOp *session.Operation
	deliveredWhileLeased := false
	sepgsqlRestoreconInstall(t, e, sepgsqlRestoreconPolicy(t, func(event sepgsql.AuditEvent) {
		if auditOp != nil && !auditOp.Released {
			deliveredWhileLeased = true
		}
		events = append(events, event)
	}, false))
	enginetest.Run(t, e, "CREATE DATABASE restorecon_peer")
	enginetest.Run(t, e, "CREATE TABLE restorecon_order(v integer)")
	enginetest.Run(t, e, "SECURITY LABEL ON DATABASE restorecon_owner IS 'S'")
	enginetest.Run(t, e, "SECURITY LABEL ON DATABASE restorecon_peer IS 'U'")
	enginetest.Run(t, e, "SECURITY LABEL ON TABLE restorecon_order IS 'S'")
	before := e.CatalogRead()
	peer, err := e.SelectDatabase(t.Context(), "restorecon_peer")
	if err != nil {
		t.Fatal(err)
	}
	peerBefore := peer.CatalogRead()
	beforeLabels, peerBeforeLabels := before.State().SecurityLabelRoot(), peerBefore.State().SecurityLabelRoot()
	var databases []ids.ObjectAddress
	for address, descriptor := range before.Descriptors() {
		if _, ok := descriptor.(*catalog.Database); ok {
			databases = append(databases, address)
		}
	}
	if len(databases) < 2 {
		t.Fatal("missing real shared peer descriptor")
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	auditOp = op
	events = nil
	sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_restorecon(NULL)", [][]driver.Value{{true}})
	// Session.Operation owns detached audit delivery, not the SQL row cursor.
	// Closing the real result finishes the statement but retains this lease.
	if len(events) != 0 {
		t.Fatal("restorecon audit escaped its operation lease", events)
	}
	op.Done()
	if !op.Released || deliveredWhileLeased {
		t.Fatal("restorecon audit was not dispatched by operation release")
	}
	var visited []string
	localReached := false
	for _, event := range events {
		if !slices.Contains(event.Required, "relabelfrom") {
			continue
		}
		if event.Class == "db_database" {
			if localReached {
				t.Fatal("shared database visited after local relabel", events)
			}
			visited = append(visited, event.Name)
		} else {
			localReached = true
		}
	}
	slices.Sort(visited)
	if !reflect.DeepEqual(visited, []string{"restorecon_owner", "restorecon_peer"}) || !localReached {
		t.Fatal("actual all-database-first permission traversal", visited, localReached)
	}
	for _, address := range databases {
		key := catalog.SecurityLabelKey{Object: address, Provider: "selinux"}
		for _, root := range []catalog.Read{e.CatalogRead(), peer.CatalogRead()} {
			label := root.State().SecurityLabel(key)
			if label == nil || label.Label != "P" || label.PolicyID != "restorecon-owner" || label.PolicyVersion != "1" ||
				root.Get(address) != before.Get(address) {
				t.Fatal("shared restorecon association/descriptor", address, label)
			}
		}
		old := before.State().SecurityLabel(key)
		if old == nil || old.Label == "P" || peerBefore.State().SecurityLabel(key) != old {
			t.Fatal("retained real predecessor was mutated", address, old)
		}
	}
	if before.State().SecurityLabelRoot() != beforeLabels || peerBefore.State().SecurityLabelRoot() != peerBeforeLabels {
		t.Fatal("restorecon mutated retained local/shared label roots")
	}
}

func TestSepgsqlSimulationRestoreconSeedAndControlScope(t *testing.T) {
	t.Run("private_seed", func(t *testing.T) {
		// Compose real peer metadata before installing the private startup
		// policy. Seed reconstruction must not label that actual peer.
		e := engine.NewWithSettings(nil, "restorecon_seed", false)
		enginetest.Run(t, e, "CREATE DATABASE restorecon_seed_peer")
		before := e.CatalogRead()
		peer := before.NameAddress(ids.NameKey{ClassID: ids.DatabaseClassID, Name: "restorecon_seed_peer"})
		if peer == (ids.ObjectAddress{}) {
			t.Fatal("missing genuine peer database")
		}
		peerRoot := e.Cluster.Targets["restorecon_seed_peer"].CatalogRead().State()
		labels := before.State().SecurityLabelRoot()
		peerLabels := peerRoot.SecurityLabelRoot()
		sepgsqlRestoreconInstall(t, e, sepgsqlRestoreconPolicy(t, nil, false))
		peerKey := catalog.SecurityLabelKey{Object: peer, Provider: "selinux"}
		currentKey := catalog.SecurityLabelKey{Object: e.Catalog.DatabaseAddress(), Provider: "selinux"}
		seeded := e.CatalogRead()
		if seeded.State().SecurityLabel(peerKey) != nil || before.State().SecurityLabel(currentKey) != nil ||
			before.State().SecurityLabel(peerKey) != nil || before.State().SecurityLabelRoot() != labels ||
			seeded.Get(peer) != before.Get(peer) || peerRoot.SecurityLabelRoot() != peerLabels ||
			peerRoot.SecurityLabel(peerKey) != nil || e.Cluster.Targets["restorecon_seed_peer"].CatalogRead().State() != peerRoot {
			t.Fatal("private startup broadened to peer or mutated retained catalog")
		}
		if label := seeded.State().SecurityLabel(currentKey); label == nil || label.Label != "P" {
			t.Fatal("private startup lost its own database label", label)
		}
	})
	t.Run("control_union", func(t *testing.T) {
		e := engine.NewWithSettings(nil, "restorecon_control", false)
		sepgsqlRestoreconInstall(t, e, sepgsqlRestoreconPolicy(t, nil, true))
		enginetest.Run(t, e, "CREATE DATABASE restorecon_control_peer")
		enginetest.Run(t, e, "SECURITY LABEL ON DATABASE restorecon_control_peer IS 'U'")
		enginetest.Run(t, e, "CREATE TABLE restorecon_control_local(v integer)")
		before := e.CatalogRead()
		peerEngine, err := e.SelectDatabase(t.Context(), "restorecon_control_peer")
		if err != nil {
			t.Fatal(err)
		}
		peerBefore := peerEngine.CatalogRead()
		beforeLabels, peerBeforeLabels := before.State().SecurityLabelRoot(), peerBefore.State().SecurityLabelRoot()
		peer := before.NameAddress(ids.NameKey{ClassID: ids.DatabaseClassID, Name: "restorecon_control_peer"})
		peerKey := catalog.SecurityLabelKey{Object: peer, Provider: "selinux"}
		peerDescriptor := before.Get(peer)
		peerLabel := before.State().SecurityLabel(peerKey)
		if peerDescriptor == nil || peerLabel == nil || peerLabel.Label != "U" ||
			peerBefore.Get(peer) != peerDescriptor || peerBefore.State().SecurityLabel(peerKey) != peerLabel {
			t.Fatal("missing genuine shared U predecessor")
		}
		physical := enginetest.StartSession(t, e, nil)
		authority, err := e.CaptureControlSepgsql(t.Context(), physical, nil)
		if err != nil {
			t.Fatal(err)
		}
		// U denies admin database relabel consent, but that real peer is
		// outside BOTH sides of the current replacement target union.
		if err := engine.ResetWithAuthority(t.Context(), e, authority); err != nil {
			t.Fatal("control consent incorrectly included peer database", err)
		}
		after := e.CatalogRead()
		peerAfter := peerEngine.CatalogRead()
		local := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "restorecon_control_local"}
		if after.Epoch() != before.Epoch()+1 || before.NameAddress(local) == (ids.ObjectAddress{}) ||
			after.NameAddress(local) != (ids.ObjectAddress{}) {
			t.Fatal("selected reset did not replace its actual local catalog")
		}
		if label := after.State().SecurityLabel(peerKey); label == nil || label != peerLabel || label.Label != "U" ||
			after.Get(peer) != peerDescriptor || peerAfter.Get(peer) != peerDescriptor || peerAfter.State().SecurityLabel(peerKey) != peerLabel {
			t.Fatal("selected replacement changed peer descriptor/label", label)
		}
		for _, root := range []*core.Engine{e, peerEngine} {
			func() {
				op, done := enginetest.RoleOperation(t, root, "postgres")
				defer done()
				sepgsqlPhaseRows(t, root, op, fmt.Sprintf("SELECT label FROM pg_shseclabel WHERE classoid='pg_database'::regclass AND objoid=%d AND provider='selinux'", peer.ObjectID), [][]driver.Value{{"U"}})
				sepgsqlPhaseRows(t, root, op, fmt.Sprintf("SELECT objtype,objname,label FROM pg_seclabels WHERE classoid='pg_database'::regclass AND objoid=%d AND provider='selinux'", peer.ObjectID), [][]driver.Value{{"database", "restorecon_control_peer", "U"}})
			}()
		}
		if before.State().SecurityLabelRoot() != beforeLabels || peerBefore.State().SecurityLabelRoot() != peerBeforeLabels ||
			before.Get(peer) != peerDescriptor || peerBefore.Get(peer) != peerDescriptor ||
			before.State().SecurityLabel(peerKey) != peerLabel || peerBefore.State().SecurityLabel(peerKey) != peerLabel ||
			peerLabel.Label != "U" {
			t.Fatal("selected reset mutated genuine retained predecessors")
		}
	})
}

func TestSepgsqlDumpSelectedDatabaseLabels(t *testing.T) {
	e := engine.NewWithSettings(nil, "dump_selected", false)
	sepgsqlRestoreconInstall(t, e, sepgsqlRestoreconPolicy(t, nil, false))
	physical := enginetest.StartSession(t, e, nil)
	for _, statement := range []string{
		"CREATE DATABASE dump_peer",
		"CREATE SCHEMA dump_labels",
		"CREATE TABLE dump_labels.items(v integer)",
		"INSERT INTO dump_labels.items VALUES(7)",
		"CREATE SEQUENCE dump_labels.counter",
		"CREATE VIEW dump_labels.visible AS SELECT v FROM dump_labels.items",
		"CREATE FUNCTION dump_labels.echo(v integer) RETURNS integer LANGUAGE SQL AS 'SELECT v'",
		"CREATE TEMP TABLE dump_temporary(v integer)",
		"SECURITY LABEL ON DATABASE dump_selected IS 'S'",
		"SECURITY LABEL ON DATABASE dump_peer IS 'U'",
		"SECURITY LABEL ON SCHEMA dump_labels IS 'S'",
		"SECURITY LABEL ON TABLE dump_labels.items IS 'S'",
		"SECURITY LABEL ON COLUMN dump_labels.items.v IS 'S'",
		"SECURITY LABEL ON COLUMN dump_labels.items.ctid IS 'U'",
		"SECURITY LABEL ON SEQUENCE dump_labels.counter IS 'S'",
		"SECURITY LABEL ON VIEW dump_labels.visible IS 'S'",
		"SECURITY LABEL ON FUNCTION dump_labels.echo(integer) IS 'S'",
		"SECURITY LABEL ON TABLE dump_temporary IS 'U'",
	} {
		enginetest.SessionSQL(t, physical, statement)
	}
	peer, err := e.SelectDatabase(t.Context(), "dump_peer")
	if err != nil {
		t.Fatal(err)
	}
	before, peerBefore := e.CatalogRead(), peer.CatalogRead()
	labels, peerLabels := before.State().SecurityLabelRoot(), peerBefore.State().SecurityLabelRoot()
	selectedKey := catalog.SecurityLabelKey{Object: e.Catalog.DatabaseAddress(), Provider: "selinux"}
	peerKey := catalog.SecurityLabelKey{Object: peer.Catalog.DatabaseAddress(), Provider: "selinux"}
	if selected := before.State().SecurityLabel(selectedKey); selected == nil || selected.Label != "S" {
		t.Fatal("missing actual selected source S", selected)
	}
	if other := before.State().SecurityLabel(peerKey); other == nil || other.Label != "U" ||
		peerBefore.State().SecurityLabel(peerKey) != other {
		t.Fatal("missing actual shared peer source U", other)
	}
	temporary := before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: physical.TemporaryNamespace, Name: "dump_temporary"})
	if record := before.State().SecurityLabel(catalog.SecurityLabelKey{Object: temporary, Provider: "selinux"}); record == nil || record.Label != "U" {
		t.Fatal("missing genuine live TEMP label", record)
	}
	durableLabels := 0
	sourceRecords := make(map[catalog.SecurityLabelKey]catalog.SecurityLabelRecord)
	for key, record := range before.State().SecurityLabels() {
		sourceRecords[key] = *record
		if key.Object == peerKey.Object {
			continue
		}
		object := key.Object
		object.SubID = 0
		descriptor := before.Get(object)
		namespace, isNamespace := descriptor.(*catalog.Namespace)
		if !isNamespace {
			namespace, _ = before.Namespace(descriptor.Namespace())
		}
		if namespace != nil && namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		durableLabels++
	}
	var first, second bytes.Buffer
	if err := engine.Dump(t.Context(), e, &first); err != nil {
		t.Fatal("actual selected dump", err)
	}
	if err := engine.Dump(t.Context(), e, &second); err != nil || !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("selected-label serialization is not deterministic", err)
	}
	script := first.String()
	// The native serializer uses explicit E literals for label text so replay
	// is independent of standard_conforming_strings (dump.CommentLiteral).
	for _, statement := range []string{
		`SECURITY LABEL FOR selinux ON DATABASE "dump_selected" IS E'S';`,
		`SECURITY LABEL FOR selinux ON SCHEMA "dump_labels" IS E'S';`,
		`SECURITY LABEL FOR selinux ON TABLE "dump_labels"."items" IS E'S';`,
		`SECURITY LABEL FOR selinux ON COLUMN "dump_labels"."items"."v" IS E'S';`,
		`SECURITY LABEL FOR selinux ON COLUMN "dump_labels"."items"."ctid" IS E'U';`,
		`SECURITY LABEL FOR selinux ON COLUMN "dump_labels"."items"."xmin" IS E'P';`,
		`SECURITY LABEL FOR selinux ON COLUMN "dump_labels"."items"."cmin" IS E'P';`,
		`SECURITY LABEL FOR selinux ON COLUMN "dump_labels"."items"."xmax" IS E'P';`,
		`SECURITY LABEL FOR selinux ON COLUMN "dump_labels"."items"."cmax" IS E'P';`,
		`SECURITY LABEL FOR selinux ON COLUMN "dump_labels"."items"."tableoid" IS E'P';`,
		`SECURITY LABEL FOR selinux ON SEQUENCE "dump_labels"."counter" IS E'S';`,
		`SECURITY LABEL FOR selinux ON VIEW "dump_labels"."visible" IS E'S';`,
		`SECURITY LABEL FOR selinux ON FUNCTION "dump_labels"."echo"(integer) IS E'S';`,
	} {
		if strings.Count(script, statement) != 1 {
			t.Fatal("missing or repeated durable selected label", statement)
		}
	}
	if !strings.Contains(script, "stuntgres-sepgsql-simulation-v1") ||
		strings.Contains(script, `SECURITY LABEL FOR selinux ON DATABASE "dump_peer" IS `) ||
		strings.Contains(script, "dump_temporary") {
		t.Fatal("selected dump lost installation requirement or serialized peer/TEMP state")
	}
	if strings.Count(script, "SECURITY LABEL FOR selinux ON ") != durableLabels {
		t.Fatal("dump omitted or duplicated durable association inventory", durableLabels)
	}
	requirements := 0
	for line := range strings.SplitSeq(script, "\n") {
		if !strings.HasPrefix(line, "-- stuntgres-sepgsql-") {
			continue
		}
		requirement, recognized, err := control.DecodeSepgsqlRequirement(strings.TrimPrefix(line, "-- "))
		if err != nil || !recognized || requirement == nil ||
			requirement.ID != before.Owner().Sepgsql.ID() || requirement.Version != before.Owner().Sepgsql.Version() ||
			requirement.Database != "dump_selected" {
			t.Fatal("dump changed actual installation requirement identity", requirement, err)
		}
		requirements++
	}
	if requirements != 1 {
		t.Fatal("dump omitted or duplicated installation requirement", requirements)
	}
	var peerDump bytes.Buffer
	if err := engine.Dump(t.Context(), peer, &peerDump); err != nil {
		t.Fatal("actual peer-as-selected dump", err)
	}
	if strings.Count(peerDump.String(), `SECURITY LABEL FOR selinux ON DATABASE "dump_peer" IS E'U';`) != 1 ||
		strings.Contains(peerDump.String(), `SECURITY LABEL FOR selinux ON DATABASE "dump_selected" IS `) {
		t.Fatal("peer's own selected label is not portable")
	}
	assertUnchanged := func() {
		t.Helper()
		if e.CatalogRead().State() != before.State() || peer.CatalogRead().State() != peerBefore.State() ||
			before.State().SecurityLabelRoot() != labels || peerBefore.State().SecurityLabelRoot() != peerLabels {
			t.Fatal("dump changed current or retained source roots")
		}
		for key, value := range sourceRecords {
			if record := before.State().SecurityLabel(key); record == nil || *record != value {
				t.Fatal("dump mutated immutable source record", key, record)
			}
		}
	}
	assertUnchanged()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var canceled bytes.Buffer
	if err := engine.Dump(ctx, e, &canceled); !errors.Is(err, context.Canceled) || canceled.Len() != 0 {
		t.Fatal("canceled dump accepted or wrote a prefix", err, canceled.String())
	}
	assertUnchanged()
	for _, key := range []catalog.SecurityLabelKey{selectedKey, peerKey} {
		// Install only a cloned invalid fixture root in this quiescent test
		// engine, then restore it immediately. Neither immutable predecessor
		// nor peer projection is mutated. Even excluded peers must validate.
		func() {
			invalid := catalog.CloneState(before.State())
			record := *invalid.SecurityLabel(key)
			record.PolicyVersion = "incompatible"
			invalid.SetSecurityLabel(key, &record)
			e.Mu.Lock()
			e.Catalog.State = invalid
			e.Mu.Unlock()
			defer func() {
				e.Mu.Lock()
				e.Catalog.State = before.State()
				e.Mu.Unlock()
			}()
			var rejected bytes.Buffer
			if err := engine.Dump(t.Context(), e, &rejected); err == nil || err.Error() != "stuntgres: invalid security label authority" {
				t.Fatal("dump skipped full label authority validation", key, err)
			}
		}()
		assertUnchanged()
	}
}

func TestSepgsqlSimulationRestoreconPublicationFences(t *testing.T) {
	for _, change := range []string{"publish", "label", "descriptor", "role", "cancel", "replacement"} {
		t.Run(change, func(t *testing.T) {
			e := engine.NewWithSettings(nil, "restorecon_fences", false)
			sepgsqlRestoreconInstall(t, e, sepgsqlRestoreconPolicy(t, nil, false))
			enginetest.Run(t, e, "CREATE ROLE restorecon_actor LOGIN SUPERUSER")
			enginetest.Run(t, e, "CREATE TABLE restorecon_stage(v integer)")
			enginetest.Run(t, e, "INSERT INTO restorecon_stage VALUES(7)")
			enginetest.Run(t, e, "SECURITY LABEL ON TABLE restorecon_stage IS 'S'")
			enginetest.Run(t, e, "SECURITY LABEL ON DATABASE restorecon_fences IS 'U'")
			op, done := enginetest.RoleOperation(t, e, "restorecon_actor")
			defer done()
			if change == "replacement" {
				// Explicit transactions retain their original epoch without an
				// implicit statement's replacement reservation. Reset can then
				// genuinely publish while the old transaction is still active.
				enginetest.RoleExecute(t, e, op, "BEGIN")
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			if err := scope.Gate(enginetest.Statement(t, "SELECT sepgsql_restorecon(NULL)")); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			var stage *core.CatalogStage
			var table ids.ObjectAddress
			if err == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.AuthorizeLocked(auth, op.Session)
				table = auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "restorecon_stage"})
				for _, address := range []ids.ObjectAddress{e.Catalog.DatabaseAddress(), table} {
					if err = core.StageSepgsqlRelabel(auth, stage, address, "P", false); err != nil {
						break
					}
				}
			}
			e.Mu.Unlock()
			if stage != nil {
				defer e.DiscardCatalogStage(stage)
			}
			if err != nil {
				t.Fatal("actual restorecon relabel staging", err)
			}
			before := stage.Base
			labelRoot := before.State().SecurityLabelRoot()
			if stage.SecurityLabelChangeCount() != 2 || e.CatalogRead().State() != before.State() {
				t.Fatal("private relabel stage published early or lost targets")
			}
			prepared, err := e.ValidateCatalogStage(op.Context(), stage)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "label":
				enginetest.Run(t, e, "SECURITY LABEL ON TABLE restorecon_stage IS 'U'")
			case "descriptor":
				enginetest.Run(t, e, "COMMENT ON TABLE restorecon_stage IS 'peer successor'")
			case "role":
				enginetest.Run(t, e, "ALTER ROLE restorecon_actor NOSUPERUSER")
			case "cancel":
				ctx, cancel := context.WithCancel(op.Context())
				op.Ctx = ctx
				cancel()
			case "replacement":
				if scope.ReplacementRead || op.Session.TransactionState() != session.TxActive {
					t.Fatal("replacement witness lacks genuine explicit transaction admission")
				}
				if err := engine.Reset(t.Context(), e); err != nil {
					t.Fatal(err)
				}
				if current := e.CatalogRead(); current.Epoch() != before.Epoch()+1 || current.Get(table) != nil ||
					op.Session.TransactionState() != session.TxActive {
					t.Fatal("real replacement did not retire the staged root under its active transaction")
				}
			}
			current := e.CatalogRead()
			currentLabels := current.State().SecurityLabelRoot()
			_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: before.Epoch()})
			if change == "publish" {
				if err != nil {
					t.Fatal("genuine atomic relabel publication", err)
				}
				for _, address := range []ids.ObjectAddress{e.Catalog.DatabaseAddress(), table} {
					label := e.CatalogRead().State().SecurityLabel(catalog.SecurityLabelKey{Object: address, Provider: "selinux"})
					if label == nil || label.Label != "P" {
						t.Fatal("atomic relabel missing association", address, label)
					}
				}
				scope.CompleteUtility()
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			} else {
				switch change {
				case "label", "descriptor":
					var conflict catalog.Conflict
					if !errors.As(err, &conflict) {
						t.Fatal("actual predecessor fence", err)
					}
				case "role":
					if err == nil || err.Error() != "stuntgres: authorization changed before publication" {
						t.Fatal("actual current-role fence", err)
					}
				case "cancel":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("actual cancellation fence", err)
					}
				case "replacement":
					if !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal("actual root fence", err)
					}
				}
				if e.CatalogRead().State() != current.State() || e.CatalogRead().State().SecurityLabelRoot() != currentLabels {
					t.Fatal("failed stage changed current root")
				}
			}
			if before.State().SecurityLabelRoot() != labelRoot || before.State().SecurityLabel(catalog.SecurityLabelKey{Object: table, Provider: "selinux"}).Label != "S" {
				t.Fatal("relabel publication mutated retained predecessor")
			}
		})
	}
}
