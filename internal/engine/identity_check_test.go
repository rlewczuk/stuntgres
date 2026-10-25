package engine

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIdentityOwnCheckAtomicConstruction(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	before := e.Catalog.State
	op, done := roleOperation(t, e, "postgres")
	const failed = `CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0),CHECK(missing>0))`
	roleErrorPosition(t, e, op, failed, "42703", `column "missing" does not exist`, "", strconv.Itoa(strings.Index(failed, "missing")+1))
	if e.Catalog.State != before || len(e.Storage.Sequences) != 0 {
		t.Fatal("failed CHECK construction published descriptor/generator")
	}
	const failedSchema = `CREATE SCHEMA failed CREATE TABLE prior(id INT GENERATED ALWAYS AS IDENTITY) CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('failed.bad_id_seq')>0),CHECK(missing>0))`
	roleErrorPosition(t, e, op, failedSchema, "42703", `column "missing" does not exist`, "", strconv.Itoa(strings.Index(failedSchema, "missing")+1))
	done()
	if e.Catalog.State != before || len(e.Storage.Sequences) != 0 {
		t.Fatal("failed CHECK construction published descriptor/generator")
	}
	sessionSQL(t, s, `CREATE SCHEMA portable CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CHECK(pg_catalog.nextval('i_id_seq')>0)) CREATE TABLE c(p INT CHECK(nextval('portable.i_id_seq')>0))`)
	table := e.Catalog.State.MustRelation(roleTableRef(t, e, "portable.i").Address)
	identity := table.ColumnMeta.Identities[table.Attributes[0]]
	provider := e.Catalog.State.MustRelation(identity.Sequence)
	found := false
	for _, dependency := range table.Constraints.Check[0].Dependencies {
		if dependency.Ref.Address == identity.Sequence {
			found = true
			if dependency.Ref.Domain() != e.Catalog.Domain || dependency.Ref.Epoch != e.Catalog.State.Epoch() || dependency.Revision != provider.Revision() {
				t.Fatal("CHECK did not capture exact candidate provider", dependency)
			}
		}
	}
	if !found {
		t.Fatal("CHECK lost its identity sequence dependency")
	}
	g := e.Storage.Sequences[identity.Sequence]
	beforeGenerator := *g
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if *g != beforeGenerator {
		t.Fatal("Dump evaluated CHECK")
	}
	sessionSQL(t, s, `INSERT INTO portable.i DEFAULT VALUES`)
	sequenceScalar(t, s, `SELECT id FROM portable.i`, 1)
	sequenceScalar(t, s, `SELECT last_value FROM portable.i_id_seq`, 2)
	sessionSQL(t, s, `INSERT INTO portable.c VALUES(10)`)
	sequenceScalar(t, s, `SELECT last_value FROM portable.i_id_seq`, 3)
	sessionSQL(t, s, `DROP TABLE portable.c`)
	sessionSQL(t, s, `DROP TABLE portable.i`)
	if e.Catalog.State.Get(identity.Sequence) != nil || e.Storage.Sequences[identity.Sequence] != nil {
		t.Fatal("own CHECK blocked reciprocal cleanup")
	}
}

func TestIdentityControlsOwnerCollisionPredecessors(t *testing.T) {
	for _, mode := range []string{"ReplaceCatalog", "Restore"} {
		t.Run(mode, func(t *testing.T) {
			source, dest := New(), New()
			a, b := sessionStart(t, source, nil), sessionStart(t, dest, nil)
			sessionSQL(t, a, `CREATE SCHEMA collision_padding`)
			for _, q := range []string{`CREATE SCHEMA portable`, `CREATE TABLE portable.i(id INT GENERATED ALWAYS AS IDENTITY)`, `CREATE VIEW portable.v AS SELECT last_value FROM portable.i_id_seq`, `CREATE TABLE portable.c(p INT CHECK(nextval('portable.i_id_seq')>0))`} {
				sessionSQL(t, a, q)
			}
			sessionSQL(t, a, `INSERT INTO portable.i DEFAULT VALUES`)
			// Advance the imported candidate allocator beyond both dependents;
			// replacement retains its allocation frontier, not destination padding.
			sessionSQL(t, a, `CREATE TABLE portable.padding(v INT)`)
			// The real table/toast namespace pair consumes two schema addresses;
			// kept then consumes the padded source owner's table address.
			sessionSQL(t, b, `CREATE TEMP SEQUENCE kept START 30 CACHE 10`)
			sequenceScalar(t, b, `SELECT nextval('kept')`, 30)
			_, description := sequencePreparedDescription(t, dest, b, `SELECT nextval('kept')`)
			temp := description.Sequences.Constants[0].Ref.Address
			owner := roleTableRef(t, source, "portable.i").Address
			originalView, originalCheck := roleTableRef(t, source, "portable.v").Address, roleTableRef(t, source, "portable.c").Address
			if temp != owner {
				t.Fatal("fixture lacks owner-table collision", temp, owner)
			}
			definition, generator, memory := dest.Catalog.State.Get(temp), dest.Storage.Sequences[temp], PayloadOf(b).Sequences.Memory(temp)
			generatorState, memoryState := *generator, *memory
			// Force the required relationship using observed object identities,
			// independently of how many embedded definitions consume OIDs.
			for n := 0; ; n++ {
				name := "displaced_" + strconv.Itoa(n)
				sessionSQL(t, b, `CREATE TABLE `+name+`(v INT)`)
				address := roleTableRef(t, dest, "public."+name).Address
				if address.ObjectID > originalView.ObjectID && address.ObjectID > originalCheck.ObjectID {
					break
				}
			}
			var initial bytes.Buffer
			if err := source.Dump(context.Background(), &initial); err != nil {
				t.Fatal(err)
			}
			if mode == "Restore" {
				if err := dest.Restore(context.Background(), bytes.NewReader(initial.Bytes())); err != nil {
					t.Fatal(err)
				}
			} else if _, err := dest.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			owner = roleTableRef(t, dest, "portable.i").Address
			view, check := roleTableRef(t, dest, "portable.v").Address, roleTableRef(t, dest, "portable.c").Address
			if view != originalView || check != originalCheck {
				t.Fatal("unrelated dependent addresses changed", view, check)
			}
			if owner.ObjectID <= view.ObjectID || owner.ObjectID <= check.ObjectID {
				t.Fatal("fixture lacks predecessor inversion", owner, view, check)
			}
			table := dest.Catalog.State.MustRelation(owner)
			identity := table.ColumnMeta.Identities[table.Attributes[0]]
			sequence := dest.Catalog.State.MustRelation(identity.Sequence)
			owned := owner
			owned.SubID = table.Attributes[0]
			if !sequence.Sequence.Identity || sequence.Sequence.Owned != owned {
				t.Fatal("reciprocal owner remapping failed")
			}
			var dump bytes.Buffer
			if err := dest.Dump(context.Background(), &dump); err != nil {
				t.Fatal(err)
			}
			if err := dest.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal("remapped dump cannot restore", err, dump.String())
			}
			var again bytes.Buffer
			if err := dest.Dump(context.Background(), &again); err != nil {
				t.Fatal(err)
			}
			if err := dest.Restore(context.Background(), bytes.NewReader(again.Bytes())); err != nil {
				t.Fatal("second remapped dump cannot restore", err)
			}
			if dest.Catalog.State.Get(temp) != definition || dest.Storage.Sequences[temp] != generator || PayloadOf(b).Sequences.Memory(temp) != memory || *generator != generatorState || *memory != memoryState {
				t.Fatal("retained TEMP state changed")
			}
			sequenceScalar(t, b, `SELECT last_value FROM portable.v`, 1)
			sessionSQL(t, b, `INSERT INTO portable.c VALUES(7)`)
			sequenceScalar(t, b, `SELECT last_value FROM portable.v`, 2)
			sessionSQL(t, b, `INSERT INTO portable.i DEFAULT VALUES`)
			sequenceScalar(t, b, `SELECT COUNT(*) FROM portable.i`, 2)
			sequenceScalar(t, b, `SELECT last_value FROM portable.v`, 3)
			sessionSQL(t, b, `DROP TABLE portable.i CASCADE`)
			sequenceScalar(t, b, `SELECT COUNT(*) FROM portable.c`, 1)
			sessionSQL(t, b, `INSERT INTO portable.c VALUES(9)`)
			if dest.Catalog.State.Get(identity.Sequence) != nil || dest.Storage.Sequences[identity.Sequence] != nil {
				t.Fatal("linked sequence survived owner cleanup")
			}
			sequenceScalar(t, b, `SELECT nextval('kept')`, 31)
		})
	}
}

func TestIdentityOwnCheckIncumbentAdmission(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, `CREATE SEQUENCE incumbent`)
	ref := catalogRef(t, e, e.CatalogRead(), "incumbent")
	tx := transactionBegin(t, a)
	sessionSQL(t, a, `DROP SEQUENCE incumbent CASCADE`)
	const create = `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('i_id_seq')>0 AND nextval('incumbent')>0))`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := lockAsync(e, b, ctx, create, false)
	lockPending(t, e, 1)
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare
	if !queued {
		t.Fatal("mixed staged/incumbent CHECK did not admit exact incumbent")
	}
	cancel()
	lockAwait(t, waiting, context.Canceled)
	e.Mu.Lock()
	read := e.Catalog.Current()
	leaked := len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}), Name: "i"})) != 0 || len(e.Storage.Sequences) != 1
	e.Mu.Unlock()
	if leaked {
		t.Fatal("canceled provider admission published staged identity")
	}
	transactionEnd(t, a, tx, false)
	sessionSQL(t, b, create)
	sessionSQL(t, b, `INSERT INTO i DEFAULT VALUES`)
	sequenceScalar(t, b, `SELECT id FROM i`, 1)
	sequenceScalar(t, b, `SELECT last_value FROM i_id_seq`, 2)
	sequenceScalar(t, b, `SELECT last_value FROM incumbent`, 1)
	checkReviewDependencyValid(t, e)
	lockEmpty(t, e)
}

func TestIdentityDumpProviderMapping(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('i_id_seq')>0))`)
	sessionSQL(t, s, `CREATE TABLE c(p INT CHECK(nextval('i_id_seq')>0))`)
	sessionSQL(t, s, `CREATE VIEW v AS SELECT nextval('i_id_seq') AS n`)
	sessionSQL(t, s, `CREATE SEQUENCE ordinary`)
	read := e.CatalogRead()
	owner := roleTableRef(t, e, "i").Address
	consumer := roleTableRef(t, e, "c").Address
	view := roleTableRef(t, e, "v").Address
	table := read.MustRelation(owner)
	sequence := table.ColumnMeta.Identities[table.Attributes[0]].Sequence
	ordinary := catalogRef(t, e, read, "ordinary").Address
	for _, tc := range []struct {
		provider, consumer ids.ObjectAddress
		want               ids.OID
	}{
		{sequence, consumer, owner.ObjectID}, {sequence, view, owner.ObjectID},
		{sequence, owner, 0}, {ordinary, consumer, 0},
	} {
		got, err := control.Resolver{}.Predecessor(read, tc.provider, tc.consumer)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	descriptor := read.MustRelation(sequence)
	bad := *descriptor
	options := *descriptor.Sequence
	options.Owned.SubID = 99
	bad.Sequence = &options
	stage := e.BeginCatalogStage(read)
	stage.UpdateDescriptor(&bad)
	if _, err := (control.Resolver{}).Predecessor(e.StageRead(stage), sequence, consumer); err == nil {
		t.Fatal("invalid reciprocal identity silently lost predecessor")
	}
	var dump bytes.Buffer
	state := *e.Storage.Sequences[sequence]
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if *e.Storage.Sequences[sequence] != state {
		t.Fatal("scalar view/CHECK Dump evaluated provider")
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	sequenceScalar(t, s, `SELECT n FROM v`, 1)
}

func TestIdentityOwnCheckBindingAuthority(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`)
	read := e.CatalogRead()
	table := read.MustRelation(roleTableRef(t, e, "i").Address)
	address := table.ColumnMeta.Identities[table.Attributes[0]].Sequence
	original := read.MustRelation(address)
	stage := e.BeginCatalogStage(read)
	candidate := *original
	candidate.ObjectName = "candidate_only"
	candidate.SequencePlanStamp++
	if err := stage.Replace(&candidate); err != nil {
		t.Fatal(err)
	}
	namespace := *read.MustNamespace(candidate.Namespace())
	namespace.ObjectName = "candidate_schema"
	namespace.ObjectRevision++
	if err := stage.Replace(&namespace); err != nil {
		t.Fatal(err)
	}
	ast := statement(t, `SELECT pg_catalog.nextval('candidate_only')`).(*parser.Select)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	// Search paths resolve lexical schema names against the candidate too.
	// The real public namespace now has only its private candidate_schema name.
	path, err := e.Settings.Definitions()["search_path"].Parse("candidate_schema")
	if err != nil {
		t.Fatal(err)
	}
	auth.settings.Mu.Lock()
	auth.settings.Typed["search_path"] = path
	auth.settings.Values["search_path"] = "candidate_schema"
	auth.settings.Mu.Unlock()
	auth.Catalog = e.StageRead(stage)
	auth.Recapture(auth.Catalog)
	binder := NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	binder.Query.Auth = &auth
	if _, err := functions.BindSequenceCall(binder, ast.Targets[0].(parser.CallExpr)); err != nil {
		t.Fatal(err)
	}
	if len(binder.Prepared.SequenceConstants) != 1 {
		t.Fatal(binder.Prepared.SequenceConstants)
	}
	constant := binder.Prepared.SequenceConstants[0]
	if constant.Ref.Address != address || constant.Ref.Domain() != e.Catalog.Domain || constant.Ref.Epoch != read.Epoch() || constant.Stamp != candidate.SequencePlanStamp {
		t.Fatal("regclass resolution/stamp mixed candidate and published authority", constant)
	}
	qualified := statement(t, `SELECT candidate_schema.nextval('candidate_only')`).(*parser.Select).Targets[0].(parser.CallExpr)
	if visible, err := binder.BuiltinVisible(qualified.Name); err != nil || visible {
		t.Fatal("builtin namespace lookup ignored candidate authority", visible, err)
	}
	if e.Catalog.State != read.State() || e.Catalog.State.Get(address) != original || e.Storage.Sequences[address].Called {
		t.Fatal("binding published or evaluated the candidate")
	}
}
