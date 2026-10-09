package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceIdentityOwnCheck(t *testing.T) {
	for _, scenario := range []struct{ name, create, table, sequence string }{
		{"automatic", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CONSTRAINT c CHECK(nextval('i_id_seq')>0))`, "i", "i_id_seq"},
		{"explicit", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME owncheck),CONSTRAINT c CHECK(nextval('owncheck')>0))`, "i", "owncheck"},
		{"schema", `CREATE SCHEMA portable CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CONSTRAINT c CHECK(nextval('portable.i_id_seq')>0))`, "portable.i", "portable.i_id_seq"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			var empty bytes.Buffer
			for _, b := range p.backends() {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec(scenario.create, 0))
				run(identitySequenceState(scenario.sequence, 1, false))
				if !b.pg {
					if err := stuntgres.Dump(p.ctx, p.native, &empty); err != nil {
						t.Fatal(err)
					}
					run(identitySequenceState(scenario.sequence, 1, false))
					if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(empty.Bytes())); err != nil {
						t.Fatal(err)
					}
				} else {
					drop := `DROP TABLE i`
					if scenario.name == "schema" {
						drop = `DROP SCHEMA portable CASCADE`
					}
					run(referenceExec(drop, 0))
					if _, err := b.db.ExecContext(p.ctx, empty.String()); err != nil {
						t.Fatal("unchanged empty replay", err)
					}
				}
				run(identitySequenceState(scenario.sequence, 1, false))
				run(referenceExec(`INSERT INTO `+scenario.table+` DEFAULT VALUES`, 1))
				run(referenceSelect(`SELECT id FROM `+scenario.table, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(identitySequenceState(scenario.sequence, 2, true))
			}
			query := `INSERT INTO ` + scenario.table + ` DEFAULT VALUES`
			held := identityHeld(t, p.ctx, p.memory, query)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, identitySequenceState(scenario.sequence, 2, true))
			if p.postgres != nil {
				drop := `DROP TABLE i`
				if scenario.name == "schema" {
					drop = `DROP SCHEMA portable CASCADE`
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(drop, 0))
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("unchanged populated replay", err)
				}
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var again bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatalf("redump mismatch\n%s\n%s", dump.Bytes(), again.Bytes())
			}
			for _, b := range p.backends() {
				var executor referenceExecutor = held
				if b.pg {
					executor = identityHeld(t, p.ctx, b.db, query)
				}
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, executor, b.recorder, s) }
				run(identitySequenceState(scenario.sequence, 2, true))
				run(referenceExec(query, 1))
				run(referenceSelect(`SELECT id FROM `+scenario.table+` ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
				run(identitySequenceState(scenario.sequence, 4, true))
				run(referenceExec(`DROP TABLE `+scenario.table, 0))
				s := referenceImplicitError(`SELECT * FROM `+scenario.sequence, referenceDiagnostic{Code: "42P01", Message: `relation "` + scenario.sequence + `" does not exist`, Position: "15"})
				s.selectRows = true
				run(s)
				if scenario.name == "schema" {
					run(referenceExec(`DROP SCHEMA portable CASCADE`, 0))
				}
			}
		})
	}
}

func TestReferenceIdentityControlsOwnerCollision(t *testing.T) {
	p := newReferenceScenario(t)
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	source := sql.OpenDB(connector)
	defer source.Close()
	for _, q := range []string{`CREATE SCHEMA portable`, `CREATE TABLE portable.i(id INT GENERATED ALWAYS AS IDENTITY)`, `CREATE VIEW portable.v AS SELECT last_value FROM portable.i_id_seq`, `CREATE TABLE portable.c(p INT CHECK(nextval('portable.i_id_seq')>0))`} {
		identityRefExec(t, source, q, 0)
	}
	identityRefExec(t, source, `INSERT INTO portable.i DEFAULT VALUES`, 1)
	identityRefExec(t, source, `CREATE TABLE portable.padding(v INT)`, 0)
	var sourceOwner, sourceView, sourceCheck int64
	for _, object := range []struct {
		name string
		oid  *int64
	}{{"i", &sourceOwner}, {"v", &sourceView}, {"c", &sourceCheck}} {
		if err := source.QueryRow(`SELECT oid FROM pg_catalog.pg_class WHERE relname=$1`, object.name).Scan(object.oid); err != nil {
			t.Fatal(err)
		}
	}
	var initial bytes.Buffer
	if err := stuntgres.Dump(p.ctx, source, &initial); err != nil {
		t.Fatal(err)
	}
	kept, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	if _, err := kept.ExecContext(p.ctx, `CREATE TEMP SEQUENCE kept START 30 CACHE 10`); err != nil {
		t.Fatal(err)
	}
	identityRefConnValue(t, p.ctx, kept, `SELECT nextval('kept')`, 30)
	var tempOID int64
	if err := kept.QueryRowContext(p.ctx, `SELECT oid FROM pg_catalog.pg_class WHERE relname='kept'`).Scan(&tempOID); err != nil || tempOID != sourceOwner {
		t.Fatal("fixture lacks owner-table collision", tempOID, sourceOwner, err)
	}
	// Advance with actual relations until replacement must place the collided
	// owner after both preserved dependents; embedded definition OIDs need not
	// consume a fixed number of allocator slots.
	for n := 0; ; n++ {
		name := "displaced_" + strconv.Itoa(n)
		identityRefExec(t, p.memory, `CREATE TABLE `+name+`(v INT)`, 0)
		var allocated int64
		if err := p.memory.QueryRow(`SELECT oid FROM pg_catalog.pg_class WHERE relname=$1`, name).Scan(&allocated); err != nil {
			t.Fatal(err)
		}
		if allocated > sourceView && allocated > sourceCheck {
			break
		}
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(initial.Bytes())); err != nil {
		t.Fatal(err)
	}
	var owner, view, check int64
	for _, object := range []struct {
		name string
		oid  *int64
	}{{"i", &owner}, {"v", &view}, {"c", &check}} {
		if err := p.memory.QueryRow(`SELECT oid FROM pg_catalog.pg_class WHERE relname=$1`, object.name).Scan(object.oid); err != nil {
			t.Fatal(err)
		}
	}
	if owner <= view || owner <= check || view != sourceView || check != sourceCheck {
		t.Fatal("fixture lacks preserved-dependent predecessor inversion", owner, view, check, sourceView, sourceCheck)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("unchanged remapped native SQL replay", err, dump.String())
		}
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(context.Background(), p.native, &again); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect(`SELECT id FROM portable.i`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, identitySequenceState("portable.i_id_seq", 1, true))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(`DROP SCHEMA portable CASCADE`, 0))
		if _, err := p.postgres.ExecContext(p.ctx, again.String()); err != nil {
			t.Fatal("unchanged second dump replay", err, again.String())
		}
	}
	for _, b := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceSelect(`SELECT last_value FROM portable.v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(identitySequenceState("portable.i_id_seq", 1, true))
		run(referenceExec(`INSERT INTO portable.c VALUES(7)`, 1))
		run(referenceSelect(`SELECT last_value FROM portable.v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(referenceExec(`INSERT INTO portable.i DEFAULT VALUES`, 1))
		run(referenceSelect(`SELECT id FROM portable.i ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
		run(referenceSelect(`SELECT last_value FROM portable.v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}))
		// Native restored visibility uses unqualified names; PostgreSQL's public
		// search path requires portable qualification. Assert both full records.
		nativeDrop := referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence i_id_seq because other objects depend on it", Detail: "view v depends on sequence i_id_seq\nconstraint c_check on table c depends on sequence i_id_seq", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
		postgresDrop := controlsReferenceDiagnostic("2BP01", "cannot drop sequence portable.i_id_seq because other objects depend on it")
		postgresDrop.Detail = "view portable.v depends on sequence portable.i_id_seq\nconstraint c_check on table portable.c depends on sequence portable.i_id_seq"
		postgresDrop.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		drop := referenceImplicitError(`ALTER TABLE portable.i ALTER id DROP IDENTITY`, nativeDrop)
		drop.postgresDiagnostic = &postgresDrop
		run(drop)
		run(referenceExec(`DROP TABLE portable.i CASCADE`, 0))
		run(referenceExec(`INSERT INTO portable.c VALUES(9)`, 1))
		run(referenceSelect(`SELECT p FROM portable.c ORDER BY p`, []string{"p"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
		run(referenceExec(`DROP SCHEMA portable CASCADE`, 0))
	}
	identityRefConnValue(t, p.ctx, kept, `SELECT nextval('kept')`, 31)
}

func TestReferenceIdentityOwnCheckConstructionFailure(t *testing.T) {
	p := newReferenceScenario(t)
	for _, q := range []string{
		`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0),CHECK(missing>0))`,
		`CREATE SCHEMA failed CREATE TABLE prior(id INT GENERATED ALWAYS AS IDENTITY) CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('failed.bad_id_seq')>0),CHECK(missing>0))`,
	} {
		p.step(t, referenceImplicitError(q, referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: strconv.Itoa(strings.Index(q, "missing") + 1)}))
	}
	p.step(t, referenceExec(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0))`, 0))
	p.step(t, identitySequenceState("bad_id_seq", 1, false))
	p.step(t, referenceExec(`CREATE SCHEMA failed CREATE TABLE prior(id INT GENERATED ALWAYS AS IDENTITY) CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('failed.bad_id_seq')>0))`, 0))
	p.step(t, identitySequenceState("failed.prior_id_seq", 1, false))
	p.step(t, identitySequenceState("failed.bad_id_seq", 1, false))
	p.step(t, referenceExec(`DROP TABLE bad`, 0))
	p.step(t, referenceExec(`DROP SCHEMA failed CASCADE`, 0))
}
