package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func discardUnitScope(t *testing.T, session *Session, ctx context.Context, category parser.DiscardCategory) (*Operation, *StatementScope) {
	t.Helper()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := session.Engine().StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err := scope.gate(&parser.Discard{Category: category}); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	return op, scope
}

func discardUnitApply(t *testing.T, session *Session, category parser.DiscardCategory) {
	t.Helper()
	op, scope := discardUnitScope(t, session, context.Background(), category)
	defer op.Done()
	_, err := scope.executeDiscard(&parser.Discard{Category: category}, 0)
	if err := scope.Finish(err); err != nil {
		t.Fatal(err)
	}
}

func TestDiscardOwnerPrecision(t *testing.T) {
	for _, category := range []parser.DiscardCategory{parser.DiscardPlans, parser.DiscardSequences, parser.DiscardAll} {
		t.Run(map[parser.DiscardCategory]string{parser.DiscardPlans: "plans", parser.DiscardSequences: "sequences", parser.DiscardAll: "all"}[category], func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}
			memory := &sequenceMemory{valid: true, value: 7}
			a.sequences, b.sequences = make(map[ids.ObjectAddress]*sequenceMemory), make(map[ids.ObjectAddress]*sequenceMemory)
			a.sequences[address], b.sequences[address] = memory, &sequenceMemory{valid: true, value: 8}
			a.lastSequence, b.lastSequence = address, address
			entry := &sqlPreparedStatement{body: &parser.Select{}}
			a.preparedSQL.entries["p"], b.preparedSQL.entries["p"] = entry, entry
			var tx ids.TxRef
			if category != parser.DiscardAll {
				tx = transactionBegin(t, a)
			}
			cursor := &sqlCursor{root: &projectNode{}, authority: &queryAuthority{}}
			cursor.tx = tx
			a.cursors.entries["c"] = cursor
			resources, serial, ticket, catalog, roles := a.resources, a.serial, a.advisory.ticket, e.catalog.state, e.roles
			discardUnitApply(t, a, category)
			if a.resources != resources || a.serial != serial || a.advisory.ticket != ticket || !a.IsLive() {
				t.Fatal("DISCARD detached or retired physical owners")
			}
			if e.catalog.state != catalog || e.roles != roles || b.preparedSQL.entries["p"] != entry || b.sequences[address].value != 8 || b.lastSequence != address || b.discardPlansGeneration != 0 {
				t.Fatal("DISCARD changed shared/peer state")
			}
			if category == parser.DiscardAll {
				if len(a.preparedSQL.entries) != 0 || len(a.cursors.entries) != 0 || cursor.root != nil || cursor.authority.scope != nil {
					t.Fatal("ALL did not dispose actual registries")
				}
			} else if a.preparedSQL.entries["p"] != entry || a.cursors.entries["c"] != cursor || cursor.root == nil {
				t.Fatal("subset deallocated definitions/cursors")
			}
			if category == parser.DiscardPlans {
				if a.sequences[address] != memory || a.lastSequence != address {
					t.Fatal("PLANS lost sequence memory")
				}
			} else if len(a.sequences) != 0 || a.lastSequence != (ids.ObjectAddress{}) {
				t.Fatal("sequence memories retained")
			}
			if category != parser.DiscardAll {
				transactionEnd(t, a, tx, false)
			}
		})
	}
}

func TestDiscardPublicationBoundary(t *testing.T) {
	for _, category := range []parser.DiscardCategory{parser.DiscardPlans, parser.DiscardSequences, parser.DiscardAll} {
		for _, before := range []bool{false, true} {
			t.Run(map[bool]string{true: "cancel-before", false: "cancel-after"}[before], func(t *testing.T) {
				s := sessionStart(t, New(), nil)
				address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}
				s.sequences = make(map[ids.ObjectAddress]*sequenceMemory)
				s.sequences[address] = &sequenceMemory{valid: true, value: 7}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, scope := discardUnitScope(t, s, ctx, category)
				defer op.Done()
				if before {
					cancel()
				}
				result, err := scope.executeDiscard(&parser.Discard{Category: category}, 0)
				if before {
					if !errors.Is(err, context.Canceled) || scope.completed || s.discardPlansGeneration != 0 || s.sequences[address] == nil {
						t.Fatal("canceled preflight installed effects", err)
					}
					if err := scope.Finish(err); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else {
					if err != nil || result.Count != 0 || result.Rows != nil || !scope.completed || !scope.sessionCompleted {
						t.Fatal(result, err)
					}
					cancel()
					if err := scope.Finish(nil); err != nil {
						t.Fatal("late cancellation retracted completed session effect", err)
					}
					if err := scope.Finish(nil); err != nil {
						t.Fatal("second Finish changed success", err)
					}
				}
			})
		}
	}
	t.Run("TemporaryCancelBeforeFinish", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TEMP TABLE discard_cancel(v int)")
		catalog := e.catalog.state
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, scope := discardUnitScope(t, s, ctx, parser.DiscardTemp)
		defer op.Done()
		_, err := scope.executeDiscard(&parser.Discard{Category: parser.DiscardTemp}, 0)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		cancel()
		if err := scope.Finish(nil); !errors.Is(err, context.Canceled) || e.catalog.state != catalog {
			t.Fatal("canceled TEMP partially published", err)
		}
	})
	t.Run("SubsetPreservesPrivateRoots", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TEMP TABLE discard_pending(v int)")
		sessionSQL(t, s, "BEGIN")
		sessionSQL(t, s, "INSERT INTO discard_pending VALUES(7)")
		sessionSQL(t, s, "SAVEPOINT mark")
		catalog, marks := s.transactions.catalog, len(s.transactions.savepoints)
		var address ids.ObjectAddress
		for a, rows := range s.transactions.writes {
			if len(rows) != 0 {
				address = a
			}
		}
		rows := s.transactions.writes[address]
		if len(rows) == 0 {
			t.Fatal("missing genuine private write")
		}
		for _, category := range []parser.DiscardCategory{parser.DiscardPlans, parser.DiscardSequences} {
			discardUnitApply(t, s, category)
			if s.transactions.catalog != catalog || len(s.transactions.savepoints) != marks || &s.transactions.writes[address][0] != &rows[0] {
				t.Fatal("stage-free subset merged or replaced private roots")
			}
		}
		sessionSQL(t, s, "ROLLBACK")
	})
}

func TestDiscardCaptureGeneration(t *testing.T) {
	t.Run("ChangedSignatureDoesNotPublishCaptures", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE discard_signature_capture(v int)")
		sessionSQL(t, s, "CREATE SEQUENCE discard_signature_seq")
		ast, d := sequencePreparedDescription(t, e, s, "SELECT *,TIMESTAMP 'now',nextval('discard_signature_seq'),'integer'::regtype FROM discard_signature_capture")
		if len(d.temporals) == 0 || d.sequences == nil || d.regtypes == nil {
			t.Fatal("missing real capture groups")
		}
		before := d
		sequenceBefore, regtypeBefore := *d.sequences, *d.regtypes
		sessionSQL(t, s, "ALTER TABLE discard_signature_capture ADD COLUMN other text")
		discardUnitApply(t, s, parser.DiscardPlans)
		for range 2 {
			r, err := sequencePreparedExecute(e, s, ast, &d)
			if r.Rows != nil {
				r.Rows.Close()
				t.Fatal("rejected signature returned rows")
			}
			var diagnostic interface{ SQLState() string }
			if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "0A000" || err.Error() != "stuntgres: cached plan must not change result type" {
				t.Fatal("changed signature admitted", err)
			}
			if d.planOwner != before.planOwner || d.planGeneration != before.planGeneration || &d.temporals[0] != &before.temporals[0] || !reflect.DeepEqual(d.Results, before.Results) || !reflect.DeepEqual(*d.sequences, sequenceBefore) || !reflect.DeepEqual(*d.regtypes, regtypeBefore) {
				t.Fatal("signature rejection published capture group or stamp")
			}
		}
	})
	t.Run("ReplacementDoesNotPublishCaptures", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE SEQUENCE discard_retired_seq")
		ast, d := sequencePreparedDescription(t, e, s, "SELECT TIMESTAMP 'now',nextval('discard_retired_seq'),'integer'::regtype")
		before := d
		sequenceBefore, regtypeBefore := *d.sequences, *d.regtypes
		if err := e.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		sessionSQL(t, s, "CREATE SEQUENCE discard_retired_seq")
		discardUnitApply(t, s, parser.DiscardPlans)
		for range 2 {
			r, err := sequencePreparedExecute(e, s, ast, &d)
			if r.Rows != nil {
				r.Rows.Close()
				t.Fatal("retired authority returned rows")
			}
			if !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("DISCARD rescued retired sequence provenance", err)
			}
			if d.planOwner != before.planOwner || d.planGeneration != before.planGeneration || &d.temporals[0] != &before.temporals[0] || !reflect.DeepEqual(*d.sequences, sequenceBefore) || !reflect.DeepEqual(*d.regtypes, regtypeBefore) {
				t.Fatal("replacement rejection published capture group or stamp")
			}
		}
	})
	e := New()
	s := sessionStart(t, e, nil)
	// Genuine analyzer groups, retained descriptions and catalog fences; SQL is
	// only fixture construction, not a duplicate public response witness.
	sessionSQL(t, s, "CREATE SEQUENCE discard_capture_seq")
	ast, d := sequencePreparedDescription(t, e, s, "SELECT TIMESTAMP 'now','{now}'::timestamp[],nextval('discard_capture_seq'),'integer'::regtype")
	if len(d.temporals) < 2 || d.sequences == nil || d.regtypes == nil || d.planOwner != s.token {
		t.Fatal("missing complete private capture fixture")
	}
	original := d
	sequenceCell, regtypeCell := d.sequences, d.regtypes
	discardUnitApply(t, s, parser.DiscardPlans)
	if d.planGeneration != original.planGeneration || &d.temporals[0] != &original.temporals[0] {
		t.Fatal("discard eagerly mutated descriptions")
	}
	result, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if d.planGeneration != s.discardPlansGeneration || d.planOwner != s.token || d.sequences != sequenceCell || d.regtypes != regtypeCell || &d.temporals[0] == &original.temporals[0] {
		t.Fatal("capture groups not lazily refreshed together")
	}
	if err := checkStatementSignature(original, d); err != nil {
		t.Fatal("DISCARD changed fixed signature", err)
	}
	stable := d
	result, err = sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if &d.temporals[0] != &stable.temporals[0] || d.sequences != stable.sequences || d.regtypes != stable.regtypes {
		t.Fatal("unchanged generation allocated capture state")
	}
	// A genuine missing dependency fails reanalysis before publishing any stamp
	// or capture group. The retained definition remains available to its owner.
	sessionSQL(t, s, "DROP SEQUENCE discard_capture_seq")
	discardUnitApply(t, s, parser.DiscardPlans)
	before := d
	sequenceBefore, regtypeBefore := *d.sequences, *d.regtypes
	_, err = sequencePreparedExecute(e, s, ast, &d)
	if err == nil || d.planGeneration != before.planGeneration || &d.temporals[0] != &before.temporals[0] || !reflect.DeepEqual(*d.sequences, sequenceBefore) || !reflect.DeepEqual(*d.regtypes, regtypeBefore) {
		t.Fatal("failed group published captures", err)
	}
	s.discardPlansGeneration = ^uint64(0)
	s.preparedSQL.entries["p"] = &sqlPreparedStatement{body: ast, description: d}
	op, scope := discardUnitScope(t, s, context.Background(), parser.DiscardAll)
	_, err = scope.executeDiscard(&parser.Discard{Category: parser.DiscardAll}, 0)
	if err == nil || s.preparedSQL.entries["p"] == nil || scope.completed {
		t.Fatal("generation exhaustion installed effects", err)
	}
	scope.Finish(err)
	op.Done()
}

func TestDiscardTemporaryOverlay(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP TABLE discard_overlay(v int)")
	sessionSQL(t, s, "INSERT INTO discard_overlay VALUES(7)")
	namespace := s.temporaryNamespace
	var relation *relationDescriptor
	for _, d := range e.catalog.state.Descriptors() {
		if r, ok := d.(*relationDescriptor); ok && r.Name() == "discard_overlay" {
			relation = r
		}
	}
	if relation == nil {
		t.Fatal("missing actual relation fixture")
	}
	storage := e.storage[relation.Address()]
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "CREATE TEMP TABLE discard_private(v int)")
	private := e.privateCatalogLocked(s.transactions.catalog)
	var privateAddress ids.ObjectAddress
	for a, d := range private.Descriptors() {
		if d.Name() == "discard_private" {
			privateAddress = a
		}
	}
	op, scope := discardUnitScope(t, s, context.Background(), parser.DiscardTemp)
	_, err := scope.executeDiscard(&parser.Discard{Category: parser.DiscardTemp}, 0)
	if err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	if scope.completed || scope.sessionCompleted || scope.catalog == nil || !scope.projectedRoots {
		t.Fatal("TEMP bypasses private publication")
	}
	if scope.catalog.originals[relation.Address()] != relation || scope.catalog.descriptors[relation.Address()] != nil || e.storage[relation.Address()] != storage {
		t.Fatal("TEMP uses live undo instead of catalog original fence")
	}
	if original, present := scope.catalog.originals[privateAddress]; !present || original != nil {
		t.Fatal("new private TEMP target lost committed absence fence")
	}
	if err := scope.Finish(nil); err != nil {
		op.Done()
		t.Fatal(err)
	}
	op.Done()
	if e.catalog.state.Get(relation.Address()) != relation || s.transactions.catalog == nil || s.temporaryNamespace != namespace {
		t.Fatal("private deletion leaked to shared catalog or retired namespace")
	}
	sessionSQL(t, s, "ROLLBACK")
	if e.catalog.state.Get(relation.Address()) != relation || e.storage[relation.Address()] != storage || s.transactions.catalog != nil {
		t.Fatal("rollback failed to release private deletion")
	}
	t.Run("CollationDependentProjection", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, `CREATE COLLATION pg_temp.discard_collation FROM pg_catalog."C"`)
		sessionSQL(t, s, `CREATE TABLE discard_survivor(id int,v text COLLATE pg_temp.discard_collation)`)
		sessionSQL(t, s, `INSERT INTO discard_survivor VALUES(7,'a')`)
		namespace := s.temporaryNamespace
		var survivor *relationDescriptor
		for _, d := range e.catalog.state.Descriptors() {
			if r, ok := d.(*relationDescriptor); ok && r.Name() == "discard_survivor" {
				survivor = r
			}
		}
		if survivor == nil {
			t.Fatal("missing durable dependent fixture")
		}
		durableRows := e.storage[survivor.Address()].versions
		discardUnitApply(t, s, parser.DiscardTemp)
		next := e.catalog.state.MustRelation(survivor.Address())
		if next == survivor || len(next.Columns) != 1 || len(e.storage[survivor.Address()].versions[0].values) != 1 || len(durableRows[0].values) != 2 {
			t.Fatal("CASCADE failed provider-owned durable projection or mutated old rows")
		}
		if e.catalog.state.Get(namespace) == nil || s.temporaryNamespace != namespace {
			t.Fatal("SQL TEMP retired its namespace")
		}
		for _, d := range e.catalog.state.Descriptors() {
			if d.Namespace() == namespace {
				t.Fatal("owned TEMP collation child survived", d.Name())
			}
		}
	})
	t.Run("CollationTransactionalProjection", func(t *testing.T) {
		for _, boundary := range []string{"rollback", "savepoint", "commit"} {
			t.Run(boundary, func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				sessionSQL(t, s, `CREATE COLLATION pg_temp.discard_private_coll FROM pg_catalog."C"`)
				sessionSQL(t, s, "CREATE TABLE discard_private_survivor(id int,v text COLLATE pg_temp.discard_private_coll)")
				sessionSQL(t, s, "INSERT INTO discard_private_survivor VALUES(7,'a')")
				sessionSQL(t, s, "CREATE INDEX discard_private_index ON discard_private_survivor(v)")
				var survivor *relationDescriptor
				var providerAddress, indexAddress ids.ObjectAddress
				for a, d := range e.catalog.state.Descriptors() {
					switch d.Name() {
					case "discard_private_survivor":
						// The associated composite type has the same name; it must
						// not overwrite the actual relation selected earlier.
						if r, ok := d.(*relationDescriptor); ok {
							survivor = r
						}
					case "discard_private_coll":
						providerAddress = a
					case "discard_private_index":
						indexAddress = a
					}
				}
				provider, index := e.catalog.state.Get(providerAddress), e.catalog.state.Get(indexAddress)
				if survivor == nil || provider == nil || index == nil {
					t.Fatal("missing real dependent closure fixture")
				}
				namespace, storage := s.temporaryNamespace, e.storage[survivor.Address()]
				sessionSQL(t, s, "BEGIN")
				if boundary == "savepoint" {
					sessionSQL(t, s, "SAVEPOINT original")
				}
				op, scope := discardUnitScope(t, s, context.Background(), parser.DiscardTemp)
				_, err := scope.executeDiscard(&parser.Discard{Category: parser.DiscardTemp}, 0)
				if err != nil {
					scope.Finish(err)
					op.Done()
					t.Fatal(err)
				}
				if scope.completed || scope.sessionCompleted || !scope.projectedRoots || scope.catalog.originals[survivor.Address()] != survivor || scope.catalog.originals[providerAddress] != provider || scope.catalog.originals[indexAddress] != index {
					scope.Finish(errors.New("invalid projection"))
					op.Done()
					t.Fatal("private projection lost original descriptor fences")
				}
				if err := scope.Finish(nil); err != nil {
					op.Done()
					t.Fatal(err)
				}
				op.Done()
				private := e.privateCatalogLocked(s.transactions.catalog)
				projected := private.MustRelation(survivor.Address())
				if projected == survivor || len(projected.Columns) != 1 || private.Get(providerAddress) != nil || private.Get(indexAddress) != nil || e.catalog.state.Get(survivor.Address()) != survivor || e.storage[survivor.Address()] != storage || len(storage.versions[0].values) != 2 {
					t.Fatal("private projection leaked into committed descriptor/data or missed closure")
				}
				switch boundary {
				case "rollback":
					sessionSQL(t, s, "ROLLBACK")
				case "savepoint":
					sessionSQL(t, s, "ROLLBACK TO original")
					sessionSQL(t, s, "COMMIT")
				case "commit":
					sessionSQL(t, s, "COMMIT")
				}
				if boundary == "commit" {
					if len(e.catalog.state.MustRelation(survivor.Address()).Columns) != 1 || len(e.storage[survivor.Address()].versions[0].values) != 1 || e.catalog.state.Get(providerAddress) != nil || e.catalog.state.Get(indexAddress) != nil {
						t.Fatal("commit lost projected durable consumer or retained provider/index")
					}
				} else if e.catalog.state.Get(survivor.Address()) != survivor || e.catalog.state.Get(providerAddress) != provider || e.catalog.state.Get(indexAddress) != index || e.storage[survivor.Address()] != storage || len(storage.versions[0].values) != 2 {
					t.Fatal("rollback did not restore exact durable descriptor/data/provider/index")
				}
				if s.temporaryNamespace != namespace || e.catalog.state.Get(namespace) == nil {
					t.Fatal("projection retired TEMP namespace")
				}
			})
		}
	})
	t.Run("LiveCursorClosurePreflight", func(t *testing.T) {
		for _, exhausted := range []bool{false, true} {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TEMP TABLE discard_guard(v int)")
			sessionSQL(t, s, "INSERT INTO discard_guard VALUES(7)")
			sessionSQL(t, s, "BEGIN")
			sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT v FROM discard_guard")
			if exhausted {
				rows := sessionSQL(t, s, "FETCH ALL FROM c")
				rows.Rows.Close()
			}
			cursor := s.cursors.entries["c"]
			if cursor.root == nil || len(cursor.leaves) == 0 {
				t.Fatal("missing actual retained source manifest")
			}
			catalog, namespace := e.catalog.state, s.temporaryNamespace
			op, scope := discardUnitScope(t, s, context.Background(), parser.DiscardTemp)
			_, err := scope.executeDiscard(&parser.Discard{Category: parser.DiscardTemp}, 0)
			if err == nil || err.Error() != `stuntgres: cannot DROP TABLE "discard_guard" because it is being used by active queries in this session` || scope.catalog != nil || scope.projectedRoots || e.catalog.state != catalog || s.temporaryNamespace != namespace || cursor.root == nil {
				scope.Finish(err)
				op.Done()
				t.Fatal("live cursor preflight installed effects or lost source", err)
			}
			// The concrete ALL composition owns CLOSE at its eventual successful
			// frontier, so its staged closure must not reject the same source.
			if err := scope.stageDiscardTemporary(true); err != nil {
				scope.Finish(err)
				op.Done()
				t.Fatal("ALL staging failed its CLOSE exemption", err)
			}
			scope.Finish(errors.New("discard unit candidate discarded"))
			op.Done()
			sessionSQL(t, s, "ROLLBACK")
		}
	})
}

type discardDefaultProvider struct{ calls int }

func (p *discardDefaultProvider) SessionDefault(_ context.Context, d SettingDefinition) (string, bool, error) {
	p.calls++
	switch d.Name {
	case "application_name":
		return "discard-configured", true, nil
	case "role":
		return "discard_reset_role", true, nil
	}
	return "", false, nil
}

func TestDiscardConfiguredReset(t *testing.T) {
	e := New()
	run(t, e, "CREATE ROLE discard_reset_role")
	provider := &discardDefaultProvider{}
	s := sessionStart(t, e, provider)
	calls := provider.calls
	reset, authenticated, counted := s.authorization.state.ResetRole, s.authorization.state.Authenticated, s.countedAuthenticated
	// Alter genuine owner overrides without inventing connector options.
	s.settings.values["application_name"] = "changed"
	s.settings.typed["application_name"] = settingValue{text: "changed"}
	s.settings.placeholders["discard.placeholder"] = true
	s.settings.values["discard.placeholder"] = "changed"
	s.settings.typed["discard.placeholder"] = settingValue{text: "changed"}
	s.authorization.state.CurrentUser = authenticated
	discardUnitApply(t, s, parser.DiscardAll)
	if provider.calls != calls || s.settings.value("application_name") != "discard-configured" || !s.settings.placeholders["discard.placeholder"] || s.authorization.state.CurrentUser != reset || s.authorization.state.SessionUser != authenticated || s.countedAuthenticated != counted {
		t.Fatal("ALL lost configured defaults/recognition/identity")
	}
	before := s.authorization.state
	s.authorization.revision = ^uint64(0)
	op, scope := discardUnitScope(t, s, context.Background(), parser.DiscardAll)
	_, err := scope.executeDiscard(&parser.Discard{Category: parser.DiscardAll}, 0)
	if err == nil || !reflect.DeepEqual(before, s.authorization.state) || scope.completed {
		t.Fatal("revision exhaustion installed effects", err)
	}
	scope.Finish(err)
	op.Done()
}
