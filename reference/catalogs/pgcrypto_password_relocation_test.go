package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPasswordRelocation(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("CREATE TABLE pgcrypto_password_relocated(id integer,p text DEFAULT 'foox',s text DEFAULT pgcrypto_part.gen_salt('bf',4),g text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,s)) STORED,d text DEFAULT pgcrypto_part.crypt('foox','NB'),x text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,'_J9..j2zz')) STORED,m text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,'$1$Szzz0yzz')) STORED,ms text DEFAULT pgcrypto_part.gen_salt('md5'),sha256 text GENERATED ALWAYS AS(pgcrypto_part.crypt('Hello world!','$5$saltstring')) STORED,sha512 text GENERATED ALWAYS AS(pgcrypto_part.crypt('Hello world!','$6$saltstring')) STORED)", 0))
		run(fixture.Exec("CREATE VIEW pgcrypto_password_relocated_view AS SELECT id,pgcrypto_part.crypt(p,s)=g AS valid FROM pgcrypto_password_relocated", 0))
		run(fixture.Exec("INSERT INTO pgcrypto_password_relocated(id) VALUES(1)", 1))
		check := fixture.Select("SELECT id,valid FROM pgcrypto_password_relocated_view ORDER BY id", []string{"id", "valid"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
		run(check)
		var salt, hash string
		if err := c.QueryRowContext(p.Ctx, "SELECT s,g FROM pgcrypto_password_relocated WHERE id=1").Scan(&salt, &hash); err != nil {
			t.Fatal(err)
		}
		pgcryptoPasswordSaltShape(t, salt, 4)
		stored := fixture.Select("SELECT s,g FROM pgcrypto_password_relocated WHERE id=1", []string{"s", "g"}, []string{"TEXT", "TEXT"}, [][]any{{salt, hash}})
		newValues := fixture.Select("SELECT d,x,m,sha256,sha512 FROM pgcrypto_password_relocated WHERE id=1", []string{"d", "x", "m", "sha256", "sha512"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"NB53EGGqrrb5E", "_J9..j2zzAYKMvO2BYRY", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1", pgcryptoSHA256Hello, pgcryptoSHA512Hello}})
		run(newValues)
		var md5Salt string
		if err := c.QueryRowContext(p.Ctx, "SELECT ms FROM pgcrypto_password_relocated WHERE id=1").Scan(&md5Salt); err != nil {
			t.Fatal(err)
		}
		pgcryptoPasswordAlgorithmSaltShape(t, md5Salt, "md5", 1000)
		newStored := fixture.Select("SELECT ms FROM pgcrypto_password_relocated WHERE id=1", []string{"ms"}, []string{"TEXT"}, [][]any{{md5Salt}})
		sourceIDs := pgcryptoProcedureIDs(t, p, c)
		dependencies := func(conn *sql.Conn, record *fixture.Recorder) {
			ids := pgcryptoProcedureIDs(t, p, conn)
			for _, v := range []struct {
				query, signature string
				class            int64
			}{
				{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_password_relocated'::regclass::oid AND adnum=3", "pgcrypto_part.gen_salt(text,integer)", 2604},
				{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_password_relocated'::regclass::oid AND adnum=4", "pgcrypto_part.crypt(text,text)", 2604},
				{"SELECT oid FROM pg_rewrite WHERE ev_class='pgcrypto_password_relocated_view'::regclass::oid AND rulename='_RETURN'", "pgcrypto_part.crypt(text,text)", 2618},
			} {
				var dependent int64
				if err := conn.QueryRowContext(p.Ctx, v.query).Scan(&dependent); err != nil || dependent <= 0 {
					t.Fatal(dependent, err)
				}
				fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid,refclassid::bigint AS refclassid,refobjid::bigint AS refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=1255 AND deptype='n'", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, [][]any{fixture.PgDependEdge(v.class, dependent, 0, 1255, ids[v.signature], 0, "n")}, v.class, dependent))
			}
		}
		dependencies(c, r)
		if pg {
			return
		}
		script := extensionDump(t, p.Ctx, p.Native)
		baseline, base, baseRecord := pgcryptoControlDestination(t, p, "pgcrypto_password_relocation_baseline")
		if err := stuntgres.Restore(p.Ctx, baseline, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		candidate := pgcryptoProcedureIDs(t, p, base)
		target, conn, record := pgcryptoControlDestination(t, p, "pgcrypto_password_relocation_collision")
		var maxID int64
		for _, id := range candidate {
			if id > maxID {
				maxID = id
			}
		}
		collisions := map[int64]string{}
		for i := range 128 {
			name := fmt.Sprintf("pgcrypto_password_collision_%d", i)
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE TEMP SEQUENCE "+name+" START 73", 0))
			var id int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&id); err != nil {
				t.Fatal(err)
			}
			collisions[id] = name
			if id >= maxID {
				break
			}
		}
		for signature, id := range candidate {
			if collisions[id] == "" {
				t.Fatal("missing actual procedure collision", signature, id)
			}
		}
		if err := stuntgres.Restore(p.Ctx, target, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		destination := pgcryptoProcedureIDs(t, p, conn)
		for signature, id := range candidate {
			if destination[signature] == id || destination[signature] == sourceIDs[signature] {
				t.Fatal("procedure was not relocated", signature, id, destination[signature])
			}
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT oid::bigint AS oid FROM pg_class WHERE relname=$1", []string{"oid"}, []string{"INT8"}, [][]any{{id}}, collisions[id]))
		}
		held := pgcryptoHoldMembers(t, p, conn)
		post := func() {
			fixture.RunStep(t, p.Ctx, conn, record, check)
			fixture.RunStep(t, p.Ctx, conn, record, stored)
			fixture.RunStep(t, p.Ctx, conn, record, newValues)
			fixture.RunStep(t, p.Ctx, conn, record, newStored)
			for _, call := range held {
				pgcryptoRunHeldCall(t, p, conn, record, call)
			}
			pgcryptoPasswordText(t, p, conn, record, "SELECT pgcrypto_part.crypt('Hello world!','$5$saltstring') AS value", pgcryptoSHA256Hello)
			pgcryptoPasswordText(t, p, conn, record, "SELECT pgcrypto_part.crypt('Hello world!','$6$saltstring') AS value", pgcryptoSHA512Hello)
			if !reflect.DeepEqual(destination, pgcryptoProcedureIDs(t, p, conn)) {
				t.Fatal("password procedure identities changed")
			}
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("INSERT INTO pgcrypto_password_relocated(id) VALUES(2)", 1))
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT valid FROM pgcrypto_password_relocated_view WHERE id=2", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}))
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT d,x,m,sha256,sha512 FROM pgcrypto_password_relocated WHERE id=2", []string{"d", "x", "m", "sha256", "sha512"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"NB53EGGqrrb5E", "_J9..j2zzAYKMvO2BYRY", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1", pgcryptoSHA256Hello, pgcryptoSHA512Hello}}))
			dependencies(conn, record)
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("ROLLBACK", 0))
		}
		post()
		before := extensionDump(t, p.Ctx, target)
		for _, bad := range []string{script + "\nINVALID SQL;", strings.Replace(script, "VERSION '1.4'", "VERSION 'missing'", 1)} {
			if err := stuntgres.Restore(p.Ctx, target, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid relocated replay accepted")
			}
			if extensionDump(t, p.Ctx, target) != before {
				t.Fatal("refused replay changed password destination")
			}
			post()
		}
		if !reflect.DeepEqual(sourceIDs, pgcryptoProcedureIDs(t, p, c)) || !reflect.DeepEqual(candidate, pgcryptoProcedureIDs(t, p, base)) {
			t.Fatal("destination changed source or independent baseline")
		}
		fixture.RunStep(t, p.Ctx, c, r, stored)
		fixture.RunStep(t, p.Ctx, base, baseRecord, stored)
	})
}
