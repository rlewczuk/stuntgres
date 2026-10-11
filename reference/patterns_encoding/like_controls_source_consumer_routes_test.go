package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeControlsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(id INTEGER,v TEXT,c CHAR(3),b BYTEA,p TEXT COLLATE \"C\",CONSTRAINT starts_a CHECK(v ILIKE 'a%' ESCAPE '!'))")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES(1,'Ab','a','\\xc3a9','a%'),(2,NULL,NULL,NULL,NULL)")
			fixture.StringCoreControlExec(t, db, "CREATE VIEW matched AS SELECT id,v ILIKE p ESCAPE '!' AS matched,c OPERATOR(pg_catalog.~~) 'a__' AS padded,b LIKE '\\x5f5f' AS octets FROM t")
			q := "SELECT matched,padded,octets FROM matched ORDER BY id"
			types := []string{"BOOL", "BOOL", "BOOL"}
			want := [][]any{{true, true, true}, {nil, nil, nil}}
			rows, err := db.Query(q)
			fixture.StringCoreRetainedRows(t, rows, err, nil, types, want, false)
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.StringCoreControlExec(t, db, "ALTER TABLE t RENAME COLUMN v TO value")
			rows, err = stmt.Query()
			fixture.StringCoreRetainedRows(t, rows, err, nil, types, want, false)
			for _, failed := range []string{"INSERT INTO t VALUES(3,'ab','a','\\x61','a%'),(4,'b','b','\\x62','b%')", "UPDATE t SET value='b'"} {
				if _, err := db.Exec(failed); err == nil {
					t.Fatal("CHECK failure accepted", failed)
				}
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM t").Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			old, err := db.Query(q)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if !backend.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			fixture.StringCoreRetainedRows(t, old, nil, nil, types, want, false)
			rows, err = db.Query(q)
			fixture.StringCoreRetainedRows(t, rows, err, nil, types, want, false)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if !backend.PG {
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
					t.Fatal("unstable replay", err)
				}
				bad := dump.String() + "INSERT INTO t VALUES(3,'b','b','\\x62','b%');"
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
					t.Fatal("failed replay accepted")
				}
				if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(redump.Bytes(), after.Bytes()) {
					t.Fatal("failed replay published", err)
				}
			}
			rows, err = db.QueryContext(canceled, "SELECT value LIKE p FROM t")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(canceled, "UPDATE t SET value='ab'"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
