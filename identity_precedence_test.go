package stuntgres_test

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestIdentityDeclarationSequenceBeforeDefaults(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(CACHE 0))`, `CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY(CACHE 0),p INT DEFAULT 'oops')`} {
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "CACHE (0) must be greater than zero"})
		_, err = db.Query(`SELECT * FROM bad`)
		identityState(t, err, "42P01")
		_, err = db.Query(`SELECT * FROM bad_id_seq`)
		identityState(t, err, "42P01")
	}
	q := `CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(AS bigint))`
	_, err := db.Exec(q)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: "conflicting or redundant options", Position: strconv.Itoa(strings.Index(q, "AS bigint") + 1)})
	tableCoreExec(t, db, false, `CREATE SEQUENCE side`, 0)
	_, err = db.Exec(`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME public.side))`)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P07", Message: `relation "side" already exists`})
}

func TestIdentityDeclarationPersistenceControls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := context.Background()
	tableCoreExec(t, db, false, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(UNLOGGED START 7))`, 0)
	for _, option := range []string{"LOGGED", "UNLOGGED"} {
		_, err := db.Exec(`ALTER TABLE i ALTER id SET ` + option)
		message := `option "logged" not recognized`
		if option == "UNLOGGED" {
			message = `option "unlogged" not recognized`
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "XX000", Message: message})
	}
	_, err := db.Exec(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY(LOGGED UNLOGGED))`)
	identityState(t, err, "42601")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	tableCoreExec(t, db, false, `INSERT INTO i DEFAULT VALUES`, 1)
	wantTuples(t, db, `SELECT * FROM i`, [][]any{{int64(7)}})
}
