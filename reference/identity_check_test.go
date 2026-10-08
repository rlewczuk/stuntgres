//go:build reference

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
			p := newReferencePair(t)
			var empty bytes.Buffer
			for _, db := range []*sql.DB{p.memory, p.postgres} {
				identityRefExec(t, db, scenario.create, 0)
				identityCheckRefState(t, db, scenario.sequence, 1, false)
				if db == p.memory {
					if err := stuntgres.Dump(p.ctx, p.native, &empty); err != nil {
						t.Fatal(err)
					}
					identityCheckRefState(t, db, scenario.sequence, 1, false)
					if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(empty.Bytes())); err != nil {
						t.Fatal(err)
					}
				} else {
					if scenario.name == "schema" {
						identityRefExec(t, db, `DROP SCHEMA portable CASCADE`, 0)
					} else {
						identityRefExec(t, db, `DROP TABLE i`, 0)
					}
					if _, err := db.ExecContext(p.ctx, empty.String()); err != nil {
						t.Fatal("unchanged empty native SQL replay", err, empty.String())
					}
				}
				identityCheckRefState(t, db, scenario.sequence, 1, false)
				identityRefExec(t, db, `INSERT INTO `+scenario.table+` DEFAULT VALUES`, 1)
				identityRefRows(t, db, `SELECT id FROM `+scenario.table, [][]int64{{1}})
				identityCheckRefState(t, db, scenario.sequence, 2, true)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			identityCheckRefState(t, p.memory, scenario.sequence, 2, true)
			if scenario.name == "schema" {
				identityRefExec(t, p.postgres, `DROP SCHEMA portable CASCADE`, 0)
			} else {
				identityRefExec(t, p.postgres, `DROP TABLE i`, 0)
			}
			if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal("unchanged native SQL replay", err, dump.String())
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, db := range []*sql.DB{p.memory, p.postgres} {
				identityCheckRefState(t, db, scenario.sequence, 2, true)
				held, err := db.Prepare(`INSERT INTO ` + scenario.table + ` DEFAULT VALUES`)
				if err != nil {
					t.Fatal(err)
				}
				result, err := held.Exec()
				held.Close()
				if err != nil {
					t.Fatal(err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
				identityRefRows(t, db, `SELECT id FROM `+scenario.table+` ORDER BY id`, [][]int64{{1}, {3}})
				identityCheckRefState(t, db, scenario.sequence, 4, true)
				identityRefExec(t, db, `DROP TABLE `+scenario.table, 0)
			}
			if scenario.name == "schema" {
				identityRefExec(t, p.postgres, `DROP SCHEMA portable CASCADE`, 0)
			}
		})
	}
}

func identityCheckRefState(t *testing.T, db *sql.DB, sequence string, want int64, called bool) {
	t.Helper()
	var value int64
	var gotCalled bool
	if err := db.QueryRow(`SELECT last_value,is_called FROM `+sequence).Scan(&value, &gotCalled); err != nil || value != want || gotCalled != called {
		t.Fatal(sequence, value, gotCalled, err)
	}
}

func TestReferenceIdentityControlsOwnerCollision(t *testing.T) {
	p := newReferencePair(t)
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
	sequenceProbeValue(t, p.ctx, kept, `SELECT nextval('kept')`, 30)
	var tempOID int64
	if err := kept.QueryRowContext(p.ctx, `SELECT oid FROM pg_catalog.pg_class WHERE relname='kept'`).Scan(&tempOID); err != nil || tempOID != sourceOwner {
		t.Fatal("fixture lacks owner-table collision", tempOID, sourceOwner, err)
	}
	identityRefExec(t, p.memory, `CREATE TABLE displaced(v INT)`, 0)
	identityRefExec(t, p.memory, `CREATE TABLE displaced2(v INT)`, 0)
	identityRefExec(t, p.memory, `CREATE TABLE displaced3(v INT)`, 0)
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
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("unchanged remapped native SQL replay", err, dump.String())
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(context.Background(), p.native, &again); err != nil {
		t.Fatal(err)
	}
	identityRefRows(t, p.postgres, `SELECT id FROM portable.i`, [][]int64{{1}})
	identityCheckRefState(t, p.postgres, "portable.i_id_seq", 1, true)
	identityRefExec(t, p.postgres, `DROP SCHEMA portable CASCADE`, 0)
	if _, err := p.postgres.ExecContext(p.ctx, again.String()); err != nil {
		t.Fatal("unchanged second dump replay", err, again.String())
	}
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		identityRefRows(t, db, `SELECT last_value FROM portable.v`, [][]int64{{1}})
		identityCheckRefState(t, db, "portable.i_id_seq", 1, true)
		identityRefExec(t, db, `INSERT INTO portable.c VALUES(7)`, 1)
		identityRefRows(t, db, `SELECT last_value FROM portable.v`, [][]int64{{2}})
		identityRefExec(t, db, `INSERT INTO portable.i DEFAULT VALUES`, 1)
		identityRefRows(t, db, `SELECT id FROM portable.i ORDER BY id`, [][]int64{{1}, {3}})
		identityRefRows(t, db, `SELECT last_value FROM portable.v`, [][]int64{{3}})
		_, err := db.Exec(`ALTER TABLE portable.i ALTER id DROP IDENTITY`)
		identityRefState(t, err, "2BP01")
		identityRefExec(t, db, `DROP TABLE portable.i CASCADE`, 0)
		identityRefExec(t, db, `INSERT INTO portable.c VALUES(9)`, 1)
		identityRefRows(t, db, `SELECT p FROM portable.c ORDER BY p`, [][]int64{{7}, {9}})
	}
	sequenceProbeValue(t, p.ctx, kept, `SELECT nextval('kept')`, 31)
	identityRefExec(t, p.postgres, `DROP SCHEMA portable CASCADE`, 0)
}

func TestReferenceIdentityOwnCheckConstructionFailure(t *testing.T) {
	identityReferenceCase(t, func(t *testing.T, db *sql.DB) {
		for _, q := range []string{
			`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0),CHECK(missing>0))`,
			`CREATE SCHEMA failed CREATE TABLE prior(id INT GENERATED ALWAYS AS IDENTITY) CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('failed.bad_id_seq')>0),CHECK(missing>0))`,
		} {
			_, err := db.Exec(q)
			want := controlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			want.Position = strconv.Itoa(strings.Index(q, "missing") + 1)
			referenceAssertDiagnostic(t, err, &want)
		}
		identityRefExec(t, db, `CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0))`, 0)
		identityCheckRefState(t, db, "bad_id_seq", 1, false)
		identityRefExec(t, db, `CREATE SCHEMA failed CREATE TABLE prior(id INT GENERATED ALWAYS AS IDENTITY) CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('failed.bad_id_seq')>0))`, 0)
		identityCheckRefState(t, db, "failed.prior_id_seq", 1, false)
		identityCheckRefState(t, db, "failed.bad_id_seq", 1, false)
		identityRefExec(t, db, `DROP TABLE bad`, 0)
		identityRefExec(t, db, `DROP SCHEMA failed CASCADE`, 0)
	})
}
