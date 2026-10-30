package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const originStateFlushBoundary = "pg_replication_origin_progress with flush=true requires durable WAL flushing, which is unsupported in embedded mode"

var originStateFunctions = []string{"pg_replication_origin_create", "pg_replication_origin_drop", "pg_replication_origin_oid", "pg_replication_origin_advance", "pg_replication_origin_progress"}
var originStateSignatures = []string{"text", "text", "text", "text,pg_lsn", "text,boolean"}

func originStateCleanup(t *testing.T, db *sql.DB, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, name := range names {
			var id sql.NullInt64
			if err := db.QueryRowContext(ctx, "SELECT pg_replication_origin_oid($1::text)", name).Scan(&id); err != nil {
				t.Error(err)
				continue
			}
			if id.Valid {
				if _, err := db.ExecContext(ctx, "SELECT pg_replication_origin_drop($1::text)", name); err != nil {
					t.Error(err)
				}
			}
		}
	})
}
func originStateRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}
func originStateScalar(query, typ string, value any, args ...any) fixture.Step {
	return fixture.Select(query, []string{"v"}, []string{typ}, [][]any{{value}}, args...)
}
func originStateProgress(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name string, want any) {
	t.Helper()
	originStateRun(t, p, b, e, originStateScalar("SELECT pg_replication_origin_progress($1::text,false) AS v", "PG_LSN", want, name))
}
func originStateNumber(t *testing.T, p *fixture.Pair, e fixture.Executor, query string, args ...any) int64 {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing scalar", rows.Err())
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("scalar cardinality", rows.Err())
	}
	return id
}
func originStateID(t *testing.T, p *fixture.Pair, e fixture.Executor, name string) int64 {
	t.Helper()
	id := originStateNumber(t, p, e, "SELECT pg_replication_origin_oid($1::text)", name)
	if id <= 0 || id >= 65535 {
		t.Fatal("invalid origin identity", id)
	}
	return id
}
func originStateCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name string) int64 {
	t.Helper()
	id := originStateNumber(t, p, e, "SELECT pg_replication_origin_create($1::text)", name)
	if id <= 0 || id >= 65535 || originStateID(t, p, e, name) != id {
		t.Fatal("create/lookup identity", id)
	}
	fixture.OidReferenceRows(t, p.Ctx, b, e, fixture.Select("SELECT roident,roname FROM pg_replication_origin WHERE roname=$1", []string{"roident", "roname"}, []string{"OID", "TEXT"}, [][]any{{id, name}}, name))
	return id
}
func TestReferenceReplicationOriginStateLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					name := "state_lifecycle"
					originStateCleanup(t, b.DB, name)
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					var e fixture.Executor = c
					if prepared {
						q := "SELECT pg_replication_origin_create($1::text)"
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					id := originStateCreate(t, p, b, e, name)
					q := "SELECT pg_replication_origin_progress($1::text,$2::boolean) AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					for _, position := range []string{"", "80000000/0", "FFFFFFFF/FFFFFFFF", "0/1", "0/0"} {
						if position != "" {
							originStateRun(t, p, b, c, originStateScalar("SELECT pg_replication_origin_advance($1::text,$2::pg_lsn) AS v", "VOID", "", name, position))
						}
						var want any = position
						if position == "" || position == "0/0" {
							want = nil
						}
						originStateRun(t, p, b, c, originStateScalar(q, "PG_LSN", want, name, false))
						originStateRun(t, p, b, held, originStateScalar(q, "PG_LSN", want, name, false))
					}
					fixture.OidReferenceRows(t, p.Ctx, b, c, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", id, name))
					originStateRun(t, p, b, c, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'0/2')", 1, name))
					originStateRun(t, p, b, c, originStateScalar("SELECT pg_replication_origin_drop($1::text) AS v", "VOID", "", name))
					originStateRun(t, p, b, c, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", nil, name))
					originStateRun(t, p, b, held, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: `replication origin "` + name + `" does not exist`}, name, false))
				})
			}
		})
	}
	t.Run("driver_ownership", func(t *testing.T) {
		p := fixture.NewScenario(t)
		originStateCleanup(t, p.Native, "state_driver")
		var id int64
		if err := p.Native.QueryRowContext(p.Ctx, "SELECT pg_replication_origin_create('state_driver')").Scan(&id); err != nil {
			t.Fatal(err)
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("origin-state-isolation")
		if err != nil {
			t.Fatal(err)
		}
		a := sql.OpenDB(connector)
		if _, err = a.ExecContext(p.Ctx, "SELECT pg_replication_origin_create('state_driver')"); err != nil {
			t.Fatal(err)
		}
		a.Close()
		a = sql.OpenDB(connector)
		defer a.Close()
		var retained int64
		if err = a.QueryRowContext(p.Ctx, "SELECT pg_replication_origin_oid('state_driver')").Scan(&retained); err != nil || retained <= 0 {
			t.Fatal(retained, err)
		}
		separate := stuntgres.NewDriver()
		other, err := separate.OpenConnector("origin-state-isolation")
		if err != nil {
			t.Fatal(err)
		}
		isolated := sql.OpenDB(other)
		defer isolated.Close()
		var absent sql.NullInt64
		if err = isolated.QueryRowContext(p.Ctx, "SELECT pg_replication_origin_oid('state_driver')").Scan(&absent); err != nil || absent.Valid {
			t.Fatal(absent, err)
		}
	})
}
func TestReferenceReplicationOriginStateMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			q := `SELECT oid::bigint AS oid,proname,prorettype::bigint AS result,proargtypes::text AS inputs,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,proretset,prosecdef,proargnames::text AS names,pronargdefaults,prolang::bigint AS language FROM pg_proc WHERE oid IN (6003,6004,6005,6012,6013) ORDER BY oid`
			names := []string{"oid", "proname", "result", "inputs", "proisstrict", "volatility", "parallel", "proretset", "prosecdef", "names", "pronargdefaults", "language"}
			types := []string{"INT8", "NAME", "INT8", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "INT2", "INT8"}
			var rows [][]any
			for i, oid := range []int64{6003, 6004, 6005, 6012, 6013} {
				result := int64(26)
				if i == 1 || i == 3 {
					result = 2278
				}
				if i == 4 {
					result = 3220
				}
				inputs := "25"
				if i == 3 {
					inputs = "25 3220"
				}
				if i == 4 {
					inputs = "25 16"
				}
				v, parallel := "v", "u"
				if i == 2 {
					v, parallel = "s", "s"
				}
				rows = append(rows, []any{oid, originStateFunctions[i], result, inputs, true, v, parallel, false, false, nil, int64(0), int64(12)})
			}
			s := fixture.Select(q, names, types, rows)
			originStateRun(t, p, b, b.DB, s)
			originStateRun(t, p, b, b.DB, fixture.Prepared(s))
			for i, fn := range originStateFunctions {
				args := "NULL::text"
				typ := "OID"
				if i == 1 || i == 3 {
					typ = "VOID"
				}
				if i == 3 {
					args += ",NULL::pg_lsn"
				}
				if i == 4 {
					args += ",NULL::boolean"
					typ = "PG_LSN"
				}
				for _, source := range []bool{false, true} {
					q := "SELECT " + fn + "(" + args + ") AS v"
					if source {
						q = "SELECT * FROM " + fn + "(" + args + ") AS v"
					}
					s := originStateScalar(q, typ, nil)
					originStateRun(t, p, b, b.DB, s)
					originStateRun(t, p, b, b.DB, fixture.Prepared(s))
					s.Rows = nil
					s.Query += " WHERE FALSE"
					originStateRun(t, p, b, b.DB, s)
				}
			}
			originStateCleanup(t, b.DB, "state_metadata")
			originStateCreate(t, p, b, b.DB, "state_metadata")
			originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance('state_metadata','0/5')", 1))
			acquired, err := b.DB.QueryContext(p.Ctx, "SELECT pg_replication_origin_progress('state_metadata',false) AS v")
			if err != nil {
				t.Fatal(err)
			}
			defer acquired.Close()
			columns, err := acquired.ColumnTypes()
			if err != nil || len(columns) != 1 || columns[0].DatabaseTypeName() != "PG_LSN" {
				t.Fatal(columns, err)
			}
			if !b.PG {
				originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance('state_metadata','0/6')", 1))
			}
			if !acquired.Next() {
				t.Fatal(acquired.Err())
			}
			var old string
			if err := acquired.Scan(&old); err != nil || old != "0/5" {
				t.Fatal(old, err)
			}
			after, err := acquired.ColumnTypes()
			if err != nil || !reflect.DeepEqual(columns[0].DatabaseTypeName(), after[0].DatabaseTypeName()) {
				t.Fatal(after, err)
			}
			if acquired.Next() || acquired.Err() != nil {
				t.Fatal(acquired.Err())
			}
		})
	}
}
