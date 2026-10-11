package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentityOwnCheck(t *testing.T) {
	for _, scenario := range []struct{ name, create, table, sequence string }{
		{"automatic", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CONSTRAINT c CHECK(nextval('i_id_seq')>0))`, "i", "i_id_seq"},
		{"explicit", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME owncheck),CONSTRAINT c CHECK(nextval('owncheck')>0))`, "i", "owncheck"},
		{"schema", `CREATE SCHEMA portable CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CONSTRAINT c CHECK(nextval('portable.i_id_seq')>0))`, "portable.i", "portable.i_id_seq"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			var empty bytes.Buffer
			for _, b := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec(scenario.create, 0))
				run(identitySequenceState(scenario.sequence, 1, false))
				if !b.PG {
					if err := stuntgres.Dump(p.Ctx, p.Native, &empty); err != nil {
						t.Fatal(err)
					}
					run(identitySequenceState(scenario.sequence, 1, false))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(empty.Bytes())); err != nil {
						t.Fatal(err)
					}
				} else {
					drop := `DROP TABLE i`
					if scenario.name == "schema" {
						drop = `DROP SCHEMA portable CASCADE`
					}
					run(fixture.Exec(drop, 0))
					if _, err := b.DB.ExecContext(p.Ctx, empty.String()); err != nil {
						t.Fatal("unchanged empty replay", err)
					}
				}
				run(identitySequenceState(scenario.sequence, 1, false))
				run(fixture.Exec(`INSERT INTO `+scenario.table+` DEFAULT VALUES`, 1))
				run(fixture.Select(`SELECT id FROM `+scenario.table, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(identitySequenceState(scenario.sequence, 2, true))
			}
			query := `INSERT INTO ` + scenario.table + ` DEFAULT VALUES`
			held := fixture.IdentityHeld(t, p.Ctx, p.Memory, query)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, identitySequenceState(scenario.sequence, 2, true))
			if p.Postgres != nil {
				drop := `DROP TABLE i`
				if scenario.name == "schema" {
					drop = `DROP SCHEMA portable CASCADE`
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(drop, 0))
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("unchanged populated replay", err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatalf("redump mismatch\n%s\n%s", dump.Bytes(), again.Bytes())
			}
			for _, b := range p.Backends() {
				var executor fixture.Executor = held
				if b.PG {
					executor = fixture.IdentityHeld(t, p.Ctx, b.DB, query)
				}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
				run(identitySequenceState(scenario.sequence, 2, true))
				run(fixture.Exec(query, 1))
				run(fixture.Select(`SELECT id FROM `+scenario.table+` ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
				run(identitySequenceState(scenario.sequence, 4, true))
				run(fixture.Exec(`DROP TABLE `+scenario.table, 0))
				s := fixture.ImplicitError(`SELECT * FROM `+scenario.sequence, fixture.Diagnostic{Code: "42P01", Message: `relation "` + scenario.sequence + `" does not exist`, Position: "15"})
				s.SelectRows = true
				run(s)
				if scenario.name == "schema" {
					run(fixture.Exec(`DROP SCHEMA portable CASCADE`, 0))
				}
			}
		})
	}
}

func TestReferenceIdentityControlsOwnerCollision(t *testing.T) {
	p := fixture.NewScenario(t)
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
	if err := stuntgres.Dump(p.Ctx, source, &initial); err != nil {
		t.Fatal(err)
	}
	kept, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	if _, err := kept.ExecContext(p.Ctx, `CREATE TEMP SEQUENCE kept START 30 CACHE 10`); err != nil {
		t.Fatal(err)
	}
	identityRefConnValue(t, p.Ctx, kept, `SELECT nextval('kept')`, 30)
	var tempOID int64
	if err := kept.QueryRowContext(p.Ctx, `SELECT oid FROM pg_catalog.pg_class WHERE relname='kept'`).Scan(&tempOID); err != nil || tempOID != sourceOwner {
		t.Fatal("fixture lacks owner-table collision", tempOID, sourceOwner, err)
	}
	// Advance with actual relations until replacement must place the collided
	// owner after both preserved dependents; embedded definition OIDs need not
	// consume a fixed number of allocator slots.
	for n := 0; ; n++ {
		name := "displaced_" + strconv.Itoa(n)
		identityRefExec(t, p.Memory, `CREATE TABLE `+name+`(v INT)`, 0)
		var allocated int64
		if err := p.Memory.QueryRow(`SELECT oid FROM pg_catalog.pg_class WHERE relname=$1`, name).Scan(&allocated); err != nil {
			t.Fatal(err)
		}
		if allocated > sourceView && allocated > sourceCheck {
			break
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(initial.Bytes())); err != nil {
		t.Fatal(err)
	}
	var owner, view, check int64
	for _, object := range []struct {
		name string
		oid  *int64
	}{{"i", &owner}, {"v", &view}, {"c", &check}} {
		if err := p.Memory.QueryRow(`SELECT oid FROM pg_catalog.pg_class WHERE relname=$1`, object.name).Scan(object.oid); err != nil {
			t.Fatal(err)
		}
	}
	if owner <= view || owner <= check || view != sourceView || check != sourceCheck {
		t.Fatal("fixture lacks preserved-dependent predecessor inversion", owner, view, check, sourceView, sourceCheck)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("unchanged remapped native SQL replay", err, dump.String())
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(context.Background(), p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(`SELECT id FROM portable.i`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, identitySequenceState("portable.i_id_seq", 1, true))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`DROP SCHEMA portable CASCADE`, 0))
		if _, err := p.Postgres.ExecContext(p.Ctx, again.String()); err != nil {
			t.Fatal("unchanged second dump replay", err, again.String())
		}
	}
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Select(`SELECT last_value FROM portable.v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(identitySequenceState("portable.i_id_seq", 1, true))
		run(fixture.Exec(`INSERT INTO portable.c VALUES(7)`, 1))
		run(fixture.Select(`SELECT last_value FROM portable.v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Exec(`INSERT INTO portable.i DEFAULT VALUES`, 1))
		run(fixture.Select(`SELECT id FROM portable.i ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
		run(fixture.Select(`SELECT last_value FROM portable.v`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}))
		// Native restored visibility uses unqualified names; PostgreSQL's public
		// search path requires portable qualification. Assert both full records.
		nativeDrop := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop sequence i_id_seq because other objects depend on it", Detail: "view v depends on sequence i_id_seq\nconstraint c_check on table c depends on sequence i_id_seq", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
		postgresDrop := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop sequence portable.i_id_seq because other objects depend on it")
		postgresDrop.Detail = "view portable.v depends on sequence portable.i_id_seq\nconstraint c_check on table portable.c depends on sequence portable.i_id_seq"
		postgresDrop.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		drop := fixture.ImplicitError(`ALTER TABLE portable.i ALTER id DROP IDENTITY`, nativeDrop)
		drop.PostgresDiagnostic = &postgresDrop
		run(drop)
		run(fixture.Exec(`DROP TABLE portable.i CASCADE`, 0))
		run(fixture.Exec(`INSERT INTO portable.c VALUES(9)`, 1))
		run(fixture.Select(`SELECT p FROM portable.c ORDER BY p`, []string{"p"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
		run(fixture.Exec(`DROP SCHEMA portable CASCADE`, 0))
	}
	identityRefConnValue(t, p.Ctx, kept, `SELECT nextval('kept')`, 31)
}

func TestReferenceIdentityOwnCheckConstructionFailure(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{
		`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0),CHECK(missing>0))`,
		`CREATE SCHEMA failed CREATE TABLE prior(id INT GENERATED ALWAYS AS IDENTITY) CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('failed.bad_id_seq')>0),CHECK(missing>0))`,
	} {
		p.Step(t, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: strconv.Itoa(strings.Index(q, "missing") + 1)}))
	}
	p.Step(t, fixture.Exec(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0))`, 0))
	p.Step(t, identitySequenceState("bad_id_seq", 1, false))
	p.Step(t, fixture.Exec(`CREATE SCHEMA failed CREATE TABLE prior(id INT GENERATED ALWAYS AS IDENTITY) CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('failed.bad_id_seq')>0))`, 0))
	p.Step(t, identitySequenceState("failed.prior_id_seq", 1, false))
	p.Step(t, identitySequenceState("failed.bad_id_seq", 1, false))
	p.Step(t, fixture.Exec(`DROP TABLE bad`, 0))
	p.Step(t, fixture.Exec(`DROP SCHEMA failed CASCADE`, 0))
}
