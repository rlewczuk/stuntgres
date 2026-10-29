package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseCreateControls(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		c.run(fixture.Exec("CREATE ROLE dc_owner", 0))
		c.create("dc_control_a", "OWNER dc_owner CONNECTION LIMIT 1", false)
		c.create("dc_control_b", "", false)
		c.run(fixture.Exec("CREATE ROLE dc_reader", 0))
		c.run(fixture.Exec("GRANT CREATE ON DATABASE dc_control_a TO dc_reader", 0))
		c.run(fixture.Exec("CREATE TABLE dc_control_value(v integer)", 0))
		c.run(fixture.Exec("INSERT INTO dc_control_value VALUES (7)", 1))
		a, raw, ar := c.pool("dc_control_a", "")
		b, braw, br := c.pool("dc_control_b", "")
		for _, target := range []struct {
			dbName string
			value  int64
		}{{"dc_control_a", 42}, {"dc_control_b", 99}} {
			db, _, rec := c.pool(target.dbName, "")
			fixture.RunStep(t, c.p.Ctx, db, rec, fixture.Exec("CREATE TABLE dc_control_value(v integer)", 0))
			fixture.RunStep(t, c.p.Ctx, db, rec, fixture.Exec("INSERT INTO dc_control_value VALUES ($1)", 1, target.value))
			fixture.RunStep(t, c.p.Ctx, db, rec, fixture.Exec("CREATE INDEX dc_control_idx ON dc_control_value(v)", 0))
		}
		query := "SELECT v FROM dc_control_value"
		stmt, err := a.PrepareContext(c.p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: query}
		want := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		fixture.RunStep(t, c.p.Ctx, held, ar, want)
		metadata := "SELECT d.datname,r.rolname,d.datacl IS NOT NULL AS acl_present,d.datallowconn,d.datconnlimit FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname='dc_control_a'"
		metaWant := fixture.Select(metadata, []string{"datname", "rolname", "acl_present", "datallowconn", "datconnlimit"}, []string{"NAME", "NAME", "BOOL", "BOOL", "INT4"}, [][]any{{"dc_control_a", "dc_owner", true, true, int64(1)}})
		c.run(metaWant)
		var originalOID int64
		if err := a.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&originalOID); err != nil {
			t.Fatal(err)
		}
		// PG has no native replacement API. It still exercises all shared SQL,
		// metadata, held-statement and isolation observations in this scenario.
		if !c.backend.PG {
			for _, target := range []*sql.DB{raw, braw} {
				state, err := stuntgres.InspectIndex(c.p.Ctx, target, "public", "dc_control_idx")
				if err != nil || !state.Ready || !state.Valid || !state.Live {
					t.Fatal("selected index state", state, err)
				}
			}
			old, err := raw.QueryContext(c.p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, raw, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "CREATE DATABASE") || strings.Contains(dump.String(), "CREATE ROLE") {
				t.Fatal("dump serialized cluster authority")
			}
			if err := stuntgres.Reset(c.p.Ctx, raw); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, c.p.Ctx, a, ar, fixture.Select("SELECT relname FROM pg_class WHERE relname='dc_control_value'", []string{"relname"}, []string{"NAME"}, nil))
			if _, err := stuntgres.InspectIndex(c.p.Ctx, raw, "public", "dc_control_idx"); err == nil || err.Error() != "stuntgres: index \"public\".\"dc_control_idx\" does not exist" {
				t.Fatal("Reset did not remove selected index", err)
			}
			state, err := stuntgres.InspectIndex(c.p.Ctx, braw, "public", "dc_control_idx")
			if err != nil || !state.Ready || !state.Valid || !state.Live {
				t.Fatal("Reset affected peer index", state, err)
			}
			if err := stuntgres.Restore(c.p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			state, err = stuntgres.InspectIndex(c.p.Ctx, raw, "public", "dc_control_idx")
			if err != nil || !state.Ready || !state.Valid || !state.Live {
				t.Fatal("Restore lost selected index", state, err)
			}
			if !old.Next() {
				t.Fatal("retained row missing", old.Err())
			}
			var retained int64
			if err := old.Scan(&retained); err != nil || retained != 42 {
				t.Fatalf("retained value %d: %v", retained, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("retained result changed", old.Err())
			}
			late := dump.String() + "\nCREATE SEQUENCE dc_late CACHE 0;"
			wantErr := databaseCreateDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, stuntgres.Restore(c.p.Ctx, raw, strings.NewReader(late)), &wantErr)
			canceled, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, raw, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled Restore: %v", err)
			}
			result, err := a.ExecContext(canceled, "CREATE DATABASE dc_precanceled")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled CREATE: %v %v", result, err)
			}
			c.absent("dc_precanceled")
			if err := stuntgres.Reset(c.p.Ctx, a); err == nil {
				t.Fatal("wrapped control accepted")
			}
		}
		fixture.RunStep(t, c.p.Ctx, held, ar, want)
		fixture.RunStep(t, c.p.Ctx, b, br, fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		c.run(fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		c.run(metaWant)
		fixture.RunStep(t, c.p.Ctx, a, ar, metaWant)
		c.run(fixture.Select("SELECT rolname FROM pg_roles WHERE rolname='dc_owner'", []string{"rolname"}, []string{"NAME"}, [][]any{{"dc_owner"}}))
		var retainedOID int64
		if err := a.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&retainedOID); err != nil || retainedOID != originalOID {
			t.Fatalf("registration changed: %d -> %d: %v", originalOID, retainedOID, err)
		}
		permissionConn, err := a.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer permissionConn.Close()
		for _, q := range []string{"SET ROLE dc_reader", "CREATE SCHEMA dc_control_schema", "RESET ROLE"} {
			fixture.RunStep(t, c.p.Ctx, permissionConn, ar, fixture.Exec(q, 0))
		}
	})
}

func TestReferenceDatabaseCreateFeatureSmoke(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		for _, q := range []string{"CREATE ROLE dc_actor CREATEDB", "CREATE ROLE dc_owner", "CREATE ROLE dc_denied", "GRANT dc_owner TO dc_actor WITH SET TRUE"} {
			c.run(fixture.Exec(q, 0))
		}
		c.run(fixture.Exec("SET ROLE dc_actor", 0))
		c.create("dc_feature", "OWNER dc_owner ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C'", false)
		c.error("CREATE DATABASE dc_feature", "42P04", "database \"dc_feature\" already exists")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Select("SELECT d.datname,r.rolname,d.encoding,d.datcollate,d.datctype FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname='dc_feature'", []string{"datname", "rolname", "encoding", "datcollate", "datctype"}, []string{"NAME", "NAME", "INT4", "TEXT", "TEXT"}, [][]any{{"dc_feature", "dc_owner", int64(6), "C", "C"}}))
		db, raw, rec := c.pool("dc_feature", "")
		conn, err := db.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, conn, rec, s) }
		run(fixture.Exec("SET ROLE dc_owner", 0))
		run(fixture.Exec("CREATE TABLE dc_feature_value(v integer)", 0))
		run(fixture.Exec("INSERT INTO dc_feature_value VALUES (42)", 1))
		run(fixture.Exec("RESET ROLE", 0))
		want := fixture.Select("SELECT v FROM dc_feature_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		run(fixture.Prepared(want))
		c.run(fixture.Select("SELECT relname FROM pg_class WHERE relname='dc_feature_value'", []string{"relname"}, []string{"NAME"}, nil))
		c.run(fixture.Exec("SET ROLE dc_denied", 0))
		c.error("CREATE DATABASE dc_feature_denied", "42501", "permission denied to create database")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Exec("BEGIN", 0))
		c.error("CREATE DATABASE dc_feature_block", "25001", "CREATE DATABASE cannot run inside a transaction block")
		c.run(fixture.Exec("ROLLBACK", 0))
		c.absent("dc_feature_denied")
		c.absent("dc_feature_block")
		if !c.backend.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, raw, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(c.p.Ctx, raw); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(c.p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(want)
		}
	})
}
