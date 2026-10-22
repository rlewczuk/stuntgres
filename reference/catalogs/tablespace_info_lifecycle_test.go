package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTablespaceInfoLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		oid := tablespaceInfoCurrent(t, p, b.DB)
		setup := func() {
			run(fixture.Exec("CREATE TABLE tsi_lifecycle(a integer,loc text DEFAULT pg_tablespace_location(0))", 0))
			run(fixture.Exec("INSERT INTO tsi_lifecycle(a) VALUES(7)", 1))
			run(fixture.Exec("CREATE VIEW tsi_lifecycle_scalar AS SELECT pg_tablespace_location(0) AS loc", 0))
			run(fixture.Exec("CREATE VIEW tsi_lifecycle_set AS SELECT pg_tablespace_databases(1663) AS db", 0))
		}
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("PostgreSQL replay requires actual native dump bytes")
			}
			if _, err := b.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native dump replay", err)
			}
		} else {
			setup()
		}
		check := func() {
			run(fixture.Select("SELECT a,loc FROM tsi_lifecycle ORDER BY a", []string{"a", "loc"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), ""}}))
			run(fixture.Select("SELECT loc FROM tsi_lifecycle_scalar", []string{"loc"}, []string{"TEXT"}, [][]any{{""}}))
			run(fixture.Select("SELECT db FROM tsi_lifecycle_set WHERE db=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"db"}, []string{"OID"}, [][]any{{tablespaceInfoOID(b, oid)}}))
		}
		check()
		// Runtime refusal must not publish any partial data or CTAS descriptor.
		run(fixture.Exec("CREATE TABLE tsi_atomic(loc text)", 0))
		run(fixture.Exec("INSERT INTO tsi_atomic VALUES('sentinel')", 1))
		for _, q := range []string{
			"INSERT INTO tsi_atomic SELECT pg_tablespace_location('4294967295'::oid)",
			"CREATE TABLE tsi_failed_ctas AS SELECT pg_tablespace_location('4294967295'::oid) AS loc",
		} {
			s := fixture.ErrorDiagnostic(q, tablespaceInfoLocationError(b.PG))
			s.ErrorStage = "execute"
			run(s)
		}
		run(fixture.Select("SELECT loc FROM tsi_atomic", []string{"loc"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='tsi_failed_ctas'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("DROP TABLE tsi_atomic", 0))
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		q := "SELECT v FROM pg_tablespace_databases($1::oid) AS t(v) WHERE v=(SELECT oid FROM pg_database WHERE datname=current_database())"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		heldCheck := func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, tablespaceInfoSelect(q, "OID", [][]any{{tablespaceInfoOID(b, oid)}}, int64(1663)))
		}
		heldCheck()
		run(fixture.Exec("CREATE TABLE tsi_mutation(a integer)", 0))
		heldCheck()
		var old *sql.Rows
		if !b.PG {
			old, err = stmt.QueryContext(p.Ctx, int64(1663))
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
		}
		run(fixture.Exec("DROP TABLE tsi_mutation", 0))
		heldCheck()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(dump.Bytes(), []byte("pg_tablespace_location")) || !bytes.Contains(dump.Bytes(), []byte("pg_tablespace_databases")) {
				t.Fatal("symbolic calls lost", dump.String())
			}
			emitted = append([]byte(nil), dump.Bytes()...)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			heldCheck()
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			heldCheck()
			check()
			types, err := old.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "OID" {
				t.Fatal(types, err)
			}
			if !old.Next() {
				t.Fatal("retained occupancy lost", old.Err())
			}
			var got int64
			if err := old.Scan(&got); err != nil || got != oid {
				t.Fatal(got, oid, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("retained cursor", old.Err())
			}
			old.Close()
			freshConnector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-destination")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(freshConnector)
			defer fresh.Close()
			var destination string
			if err := fresh.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&destination); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			var name, loc string
			if err := fresh.QueryRowContext(p.Ctx, "SELECT current_database(),loc FROM tsi_lifecycle_scalar").Scan(&name, &loc); err != nil || name != destination || loc != "" {
				t.Fatal(name, destination, loc, err)
			}
			freshOID := tablespaceInfoCurrent(t, p, fresh)
			var restoredOID int64
			if err := fresh.QueryRowContext(p.Ctx, "SELECT db FROM tsi_lifecycle_set").Scan(&restoredOID); err != nil || restoredOID != freshOID {
				t.Fatal(restoredOID, freshOID, err)
			}
			if _, err := fresh.ExecContext(p.Ctx, "INSERT INTO tsi_lifecycle(a) VALUES(8)"); err != nil {
				t.Fatal(err)
			}
			if err := fresh.QueryRowContext(p.Ctx, "SELECT loc FROM tsi_lifecycle WHERE a=8").Scan(&loc); err != nil || loc != "" {
				t.Fatal(loc, err)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0))
			defer expire()
			for _, tc := range []struct {
				ctx  context.Context
				want error
			}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
				for _, query := range []string{"SELECT pg_tablespace_location(0)", "SELECT * FROM pg_tablespace_databases(1663)"} {
					rows, err := c.QueryContext(tc.ctx, query)
					if rows != nil {
						rows.Close()
						t.Fatal("context failure exposed rows")
					}
					if !errors.Is(err, tc.want) {
						t.Fatal(err, tc.want)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatal("context error fabricated SQLSTATE", state.SQLState())
					}
				}
			}
			heldCheck()
		}
		active := "SELECT pg_tablespace_location(0),pg_sleep(600) FROM pg_tablespace_databases(1663) LIMIT 1"
		fixture.SleepActiveCancel(t, p, b, c, active, nil, func(ctx context.Context) fixture.RelationOutcome {
			result, err := c.ExecContext(ctx, active)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		heldCheck()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, tablespaceInfoSelect("SELECT pg_tablespace_location(0) AS v", "TEXT", [][]any{{""}}))
		if !b.PG && strings.TrimSpace(string(emitted)) == "" {
			t.Fatal("missing native dump")
		}
	})
}
