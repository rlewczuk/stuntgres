package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Each consumer is retained before replacement. None of these cases reissues
// DEFAULT/CHECK/index DDL to repair its old routine capture.
func TestReferenceTransactionalRoutineStoredReplacement(t *testing.T) {
	cases := []struct {
		name                  string
		setup                 []string
		replace               string
		consumer              string
		count                 int64
		rejected              string
		rejection             fixture.Diagnostic
		rollbackWant          [][]any
		notice, noticeContext string
		query                 string
		columns, types        []string
		want                  [][]any
	}{
		{
			name:     "default",
			setup:    []string{"CREATE FUNCTION rr_default() RETURNS integer BEGIN ATOMIC SELECT 5; END", "CREATE TABLE rr_data(v integer DEFAULT rr_default())", "INSERT INTO rr_data DEFAULT VALUES"},
			replace:  "CREATE OR REPLACE FUNCTION rr_default() RETURNS integer BEGIN ATOMIC SELECT 9; END",
			consumer: "INSERT INTO rr_data DEFAULT VALUES", count: 1,
			query: "SELECT v FROM rr_data ORDER BY v", columns: []string{"v"}, types: []string{"INT4"}, want: [][]any{{int64(5)}, {int64(9)}},
		},
		{
			name:     "check",
			setup:    []string{"CREATE FUNCTION rr_check(integer) RETURNS boolean IMMUTABLE RETURN $1>0", "CREATE TABLE rr_data(v integer CONSTRAINT rr_positive CHECK(rr_check(v)))", "INSERT INTO rr_data VALUES(7)"},
			replace:  "CREATE OR REPLACE FUNCTION rr_check(integer) RETURNS boolean IMMUTABLE RETURN $1<0",
			consumer: "INSERT INTO rr_data VALUES(-5)", count: 1,
			rejected:  "INSERT INTO rr_data VALUES(5)",
			rejection: fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: "new row for relation \"rr_data\" violates check constraint \"rr_positive\"", Detail: "Failing row contains (5).", Schema: "public", Table: "rr_data", Constraint: "rr_positive"},
			query:     "SELECT v FROM rr_data ORDER BY v", columns: []string{"v"}, types: []string{"INT4"}, want: [][]any{{int64(-5)}, {int64(7)}},
		},
		{
			name:    "index-key",
			setup:   []string{"CREATE FUNCTION rr_key(bigint) RETURNS integer IMMUTABLE LANGUAGE SQL AS $$SELECT $1::integer$$", "CREATE TABLE rr_data(id integer,v integer)", "CREATE INDEX rr_index ON rr_data(rr_key(v))", "INSERT INTO rr_data VALUES(1,7)"},
			replace: "CREATE OR REPLACE FUNCTION rr_key(bigint) RETURNS integer LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RAISE NOTICE 'replacement-key'; RETURN $1; END$$",
			notice:  "replacement-key", noticeContext: "PL/pgSQL function rr_key(bigint) line 1 at RAISE",
			consumer: "ALTER TABLE rr_data ALTER COLUMN v TYPE bigint", count: 0,
			query: "SELECT id,v FROM rr_data", columns: []string{"id", "v"}, types: []string{"INT4", "INT8"}, want: [][]any{{int64(1), int64(7)}},
		},
		{
			name:    "index-predicate",
			setup:   []string{"CREATE FUNCTION rr_predicate(bigint) RETURNS boolean IMMUTABLE LANGUAGE SQL AS $$SELECT $1>0$$", "CREATE TABLE rr_data(id integer,v integer)", "CREATE INDEX rr_index ON rr_data(v) WHERE rr_predicate(v)", "INSERT INTO rr_data VALUES(1,7)"},
			replace: "CREATE OR REPLACE FUNCTION rr_predicate(bigint) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RAISE NOTICE 'replacement-predicate'; RETURN true; END$$",
			notice:  "replacement-predicate", noticeContext: "PL/pgSQL function rr_predicate(bigint) line 1 at RAISE",
			consumer: "ALTER TABLE rr_data ALTER COLUMN v TYPE bigint", count: 0,
			query: "SELECT id,v FROM rr_data", columns: []string{"id", "v"}, types: []string{"INT4", "INT8"}, want: [][]any{{int64(1), int64(7)}},
		},
		{
			name:    "inherited",
			setup:   []string{"CREATE FUNCTION rr_check(bigint) RETURNS boolean STABLE LANGUAGE SQL AS $$SELECT true$$", "CREATE TABLE rr_parent(id integer,v integer)", "INSERT INTO rr_parent VALUES(1,10)", "CREATE TABLE rr_data() INHERITS(rr_parent)", "INSERT INTO rr_data VALUES(2,20),(3,30)", "ALTER TABLE ONLY rr_data ADD CONSTRAINT rr_positive CHECK(rr_check(v))"},
			replace: "CREATE OR REPLACE FUNCTION rr_check(bigint) RETURNS boolean LANGUAGE plpgsql STABLE AS $$BEGIN RAISE NOTICE 'replacement-child'; RETURN true; END$$",
			notice:  "replacement-child", noticeContext: "PL/pgSQL function rr_check(bigint) line 1 at RAISE",
			consumer: "ALTER TABLE rr_parent ADD COLUMN fill integer DEFAULT 7, ALTER COLUMN v TYPE bigint", count: 0,
			query: "SELECT id,v,fill FROM rr_parent ORDER BY id", columns: []string{"id", "v", "fill"}, types: []string{"INT4", "INT8", "INT4"}, want: [][]any{{int64(1), int64(10), int64(7)}, {int64(2), int64(20), int64(7)}, {int64(3), int64(30), int64(7)}},
		},
		{
			name:    "preflight-no-rewrite",
			setup:   []string{"CREATE FUNCTION rr_key(integer) RETURNS integer IMMUTABLE RETURN $1", "CREATE FUNCTION rr_guard() RETURNS text IMMUTABLE LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE 'replacement-preflight'; RETURN 'checked'; END$$", "CREATE DOMAIN rr_text AS text CHECK(rr_guard() IS NOT NULL)", "CREATE TABLE rr_data(id integer)", "CREATE INDEX rr_index ON rr_data(rr_key(id))", "INSERT INTO rr_data VALUES(1)"},
			replace: "CREATE OR REPLACE FUNCTION rr_key(integer) RETURNS integer LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RETURN 10/($1-$1); END$$",
			notice:  "replacement-preflight", noticeContext: "PL/pgSQL function rr_guard() line 1 at RAISE",
			consumer: "ALTER TABLE rr_data ADD COLUMN token text DEFAULT (('filled'::rr_text)::text)", count: 0,
			query: "SELECT id,token FROM rr_data", columns: []string{"id", "token"}, types: []string{"INT4", "TEXT"}, want: [][]any{{int64(1), "filled"}},
		},
		{
			name:     "fk-set-default",
			setup:    []string{"CREATE FUNCTION rr_default() RETURNS text BEGIN ATOMIC SELECT 'fallback'::text; END", "CREATE TABLE rr_parent(id text PRIMARY KEY)", "INSERT INTO rr_parent VALUES('old'),('fallback'),('new')", "CREATE TABLE rr_data(id integer PRIMARY KEY,v text DEFAULT rr_default() REFERENCES rr_parent(id) ON DELETE SET DEFAULT)", "INSERT INTO rr_data VALUES(1,'old')"},
			replace:  "CREATE OR REPLACE FUNCTION rr_default() RETURNS text BEGIN ATOMIC SELECT 'new'::text; END",
			consumer: "DELETE FROM rr_parent WHERE id='old'", count: 1,
			query: "SELECT id,v FROM rr_data", columns: []string{"id", "v"}, types: []string{"INT4", "TEXT"}, want: [][]any{{int64(1), "new"}},
			rollbackWant: [][]any{{int64(1), "old"}},
		},
	}
	for _, tc := range cases {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held-%t", tc.name, held), func(t *testing.T) {
				routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					for _, q := range tc.setup {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Fatal(q, err)
						}
					}
					var consumer fixture.Executor = c
					if held {
						s, err := c.PrepareContext(p.Ctx, tc.consumer)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						consumer = fixture.HeldStatement{Executor: c, Statement: s, SQL: tc.consumer}
					}
					routineExec(t, p, b, c, tc.replace)
					if tc.rollbackWant != nil {
						routineExec(t, p, b, c, "BEGIN")
					}
					*notices = nil
					fixture.RunStep(t, p.Ctx, consumer, b.Recorder, fixture.Exec(tc.consumer, tc.count))
					if tc.notice != "" {
						if len(*notices) == 0 {
							t.Fatal("replacement implementation was not reached")
						}
						want := routineDropNotice(tc.notice)
						want.Context = tc.noticeContext
						for _, got := range *notices {
							if !reflect.DeepEqual(got, want) {
								t.Fatalf("replacement notice got %#v want %#v", got, want)
							}
						}
					}
					if tc.rejected != "" {
						var rejection fixture.Executor = c
						if held {
							s, err := c.PrepareContext(p.Ctx, tc.rejected)
							if err != nil {
								t.Fatal(err)
							}
							defer s.Close()
							rejection = fixture.HeldStatement{Executor: c, Statement: s, SQL: tc.rejected}
						}
						step := fixture.ErrorDiagnostic(tc.rejected, tc.rejection)
						step.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, rejection, b.Recorder, step)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(tc.query, tc.columns, tc.types, tc.want))
					if tc.rollbackWant != nil {
						routineExec(t, p, b, c, "ROLLBACK")
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(tc.query, tc.columns, tc.types, tc.rollbackWant))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM rr_parent ORDER BY id", []string{"id"}, []string{"TEXT"}, [][]any{{"fallback"}, {"new"}, {"old"}}))
					}
				})
			})
		}
	}
}

func TestReferenceTransactionalRoutineReplacementVisibility(t *testing.T) {
	t.Run("retained-acl-current-execute", routineReplacementAuthority)
	for _, mode := range []string{"sql", "go"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit-%t", mode, commit), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						routineExec(t, p, b, owner, "CREATE FUNCTION rr_default() RETURNS integer RETURN 5", "CREATE FUNCTION rr_default(integer) RETURNS integer RETURN 104", "CREATE TABLE rr_owner(v integer DEFAULT rr_default())", "CREATE TABLE rr_peer(v integer DEFAULT rr_default())")
						oid := transactionalRoutineOID(t, p, owner, "SELECT 'rr_default()'::regprocedure::oid")
						edges := routineReplacementEdges(t, p, owner, oid)
						if len(edges) != 2 {
							t.Fatal("expected two original DEFAULT dependencies", edges)
						}
						checkEdges := func(e fixture.Executor) {
							t.Helper()
							if got := routineReplacementEdges(t, p, e, oid); !reflect.DeepEqual(got, edges) {
								t.Fatal("replacement changed retained DEFAULT dependency bag", edges, got)
							}
						}
						peerSQL := "INSERT INTO rr_peer DEFAULT VALUES"
						ps, err := peer.PrepareContext(p.Ctx, peerSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer ps.Close()
						peerHeld := fixture.HeldStatement{Executor: peer, Statement: ps, SQL: peerSQL}
						var e fixture.Executor = owner
						var tx *sql.Tx
						if mode == "go" {
							tx, err = owner.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
						} else {
							routineExec(t, p, b, owner, "BEGIN")
							defer owner.ExecContext(p.Ctx, "ROLLBACK")
						}
						ownerSQL := "INSERT INTO rr_owner DEFAULT VALUES"
						os, err := e.PrepareContext(p.Ctx, ownerSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer os.Close()
						ownerHeld := fixture.HeldStatement{Executor: e, Statement: os, SQL: ownerSQL}
						replaceSQL := "CREATE OR REPLACE FUNCTION rr_default() RETURNS integer RETURN 9"
						rs, err := e.PrepareContext(p.Ctx, replaceSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer rs.Close()
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: rs, SQL: replaceSQL}, b.Recorder, fixture.Exec(replaceSQL, 0))
						if got := transactionalRoutineOID(t, p, e, "SELECT 'rr_default()'::regprocedure::oid"); got != oid {
							t.Fatal("replacement identity", oid, got)
						}
						checkEdges(e)
						checkEdges(peer)
						for _, q := range []string{"SAVEPOINT rr_mark", "CREATE OR REPLACE FUNCTION rr_default() RETURNS integer RETURN 11"} {
							fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
						}
						fixture.RunStep(t, p.Ctx, ownerHeld, b.Recorder, fixture.Exec(ownerSQL, 1))
						fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT v FROM rr_owner", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
						for _, q := range []string{"ROLLBACK TO rr_mark", "ROLLBACK TO rr_mark", "RELEASE rr_mark"} {
							fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
						}
						checkEdges(e)
						fixture.RunStep(t, p.Ctx, ownerHeld, b.Recorder, fixture.Exec(ownerSQL, 1))
						fixture.RunStep(t, p.Ctx, peerHeld, b.Recorder, fixture.Exec(peerSQL, 1))
						fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT v FROM rr_owner", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM rr_peer", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM rr_owner", []string{"v"}, []string{"INT4"}, nil))
						if tx != nil {
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
						} else if commit {
							routineExec(t, p, b, owner, "COMMIT")
						} else {
							routineExec(t, p, b, owner, "ROLLBACK")
						}
						want := int64(5)
						var ownerRows [][]any
						if commit {
							want = 9
							ownerRows = [][]any{{int64(9)}}
						}
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM rr_owner", []string{"v"}, []string{"INT4"}, ownerRows))
						fixture.RunStep(t, p.Ctx, peerHeld, b.Recorder, fixture.Exec(peerSQL, 1))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM rr_peer ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}, {want}}))
						routineValue(t, p, b, peer, "SELECT rr_default(1) AS v", "v", "INT4", int64(104))
						if got := transactionalRoutineOID(t, p, peer, "SELECT 'rr_default()'::regprocedure::oid"); got != oid {
							t.Fatal("published identity", oid, got)
						}
						checkEdges(peer)
					})
				}
			})
		}
	}
}

func routineReplacementAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rr_reader"})
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE ROLE rr_reader", "GRANT USAGE ON SCHEMA public TO rr_reader", "CREATE FUNCTION rr_default() RETURNS integer RETURN 5", "REVOKE EXECUTE ON FUNCTION rr_default() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION rr_default() TO rr_reader", "CREATE TABLE rr_data(v integer DEFAULT rr_default())", "GRANT INSERT,SELECT ON rr_data TO rr_reader", "SET ROLE rr_reader")
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		q := "INSERT INTO rr_data DEFAULT VALUES"
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
		routineExec(t, p, b, c, "RESET ROLE", "CREATE OR REPLACE FUNCTION rr_default() RETURNS integer RETURN 9", "SET ROLE rr_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1))
		routineExec(t, p, b, c, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION rr_default() FROM rr_reader", "SET ROLE rr_reader")
		denied := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function rr_default"))
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM rr_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
		routineExec(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION rr_default() TO rr_reader", "SET ROLE rr_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM rr_data ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(9)}}))
	})
}

// Compare the complete incoming dependency bag within one backend, preserving
// allocator-independent identity relationships rather than cross-server OIDs.
func routineReplacementEdges(t *testing.T, p *fixture.Pair, e fixture.Executor, oid int64) []string {
	t.Helper()
	q := fmt.Sprintf("SELECT classid::text,objid::text,objsubid::text,refclassid::text,refobjid::text,refobjsubid::text,deptype::text FROM pg_depend WHERE refclassid='pg_proc'::regclass AND refobjid=%d ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype", oid)
	rows, err := e.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var values [7]string
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6]); err != nil {
			t.Fatal(err)
		}
		result = append(result, fmt.Sprint(values))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestReferenceTransactionalRoutineReplacementOutputShape(t *testing.T) {
	for _, standard := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("standard-%t/held-%t", standard, held), func(t *testing.T) {
				routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ *[]fixture.Diagnostic) {
					body := "LANGUAGE SQL AS $$SELECT 5,'old'::text$$"
					next := "LANGUAGE SQL AS $$SELECT 9,'new'::text$$"
					if standard {
						body = "BEGIN ATOMIC SELECT 5,'old'::text; END"
						next = "BEGIN ATOMIC SELECT 9,'new'::text; END"
					}
					routineExec(t, p, b, c, "CREATE FUNCTION rr_tuple(OUT a integer,OUT b text) "+body,
						"CREATE TABLE rr_tuple_rows(v integer DEFAULT (rr_tuple()).a,w text DEFAULT (rr_tuple()).b)")
					query := "INSERT INTO rr_tuple_rows DEFAULT VALUES"
					var consumer fixture.Executor = c
					if held {
						s, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						consumer = fixture.HeldStatement{Executor: c, Statement: s, SQL: query}
					}
					fixture.RunStep(t, p.Ctx, consumer, b.Recorder, fixture.Exec(query, 1))
					retained, err := c.QueryContext(p.Ctx, "SELECT (rr_tuple()).a AS v")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					routineScalarMetadata(b, "v", "INT4", false, false)(t, retained)
					// Native results are eager and detached; drain the PostgreSQL
					// wire result before issuing another command on its connection.
					if b.PG {
						if !retained.Next() {
							t.Fatal("missing original tuple field", retained.Err())
						}
						var v int64
						if err := retained.Scan(&v); err != nil || v != 5 {
							t.Fatal(v, err)
						}
						if retained.Next() || retained.Err() != nil {
							t.Fatal(retained.Err())
						}
						if err := retained.Close(); err != nil {
							t.Fatal(err)
						}
					}
					oid := transactionalRoutineOID(t, p, c, "SELECT 'rr_tuple()'::regprocedure::oid")
					edges := routineReplacementEdges(t, p, c, oid)
					routineExec(t, p, b, c, "CREATE OR REPLACE FUNCTION rr_tuple(OUT a integer,OUT b text) "+next)
					fixture.RunStep(t, p.Ctx, consumer, b.Recorder, fixture.Exec(query, 1))
					rejected := "CREATE OR REPLACE FUNCTION rr_tuple(OUT a integer,OUT b bigint) LANGUAGE SQL AS $$SELECT 9,10::bigint$$"
					want := fixture.ControlsReferenceDiagnostic("42P13", "cannot change return type of existing function")
					want.Detail, want.Hint = "Row type defined by OUT parameters is different.", "Use DROP FUNCTION rr_tuple() first."
					step := fixture.ErrorDiagnostic(rejected, want)
					step.Prepared, step.ErrorStage = held, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					fixture.RunStep(t, p.Ctx, consumer, b.Recorder, fixture.Exec(query, 1))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v,w FROM rr_tuple_rows ORDER BY v,w", []string{"v", "w"}, []string{"INT4", "TEXT"}, [][]any{{int64(5), "old"}, {int64(9), "new"}, {int64(9), "new"}}))
					if transactionalRoutineOID(t, p, c, "SELECT 'rr_tuple()'::regprocedure::oid") != oid || !reflect.DeepEqual(routineReplacementEdges(t, p, c, oid), edges) {
						t.Fatal("tuple replacement changed identity or dependency authority")
					}
					if !b.PG {
						routineScalarMetadata(b, "v", "INT4", false, false)(t, retained)
						if !retained.Next() {
							t.Fatal("lost retained tuple field", retained.Err())
						}
						var v int64
						if err := retained.Scan(&v); err != nil || v != 5 {
							t.Fatal("mutated retained tuple field", v, err)
						}
						if retained.Next() || retained.Err() != nil {
							t.Fatal("retained tuple cardinality", retained.Err())
						}
					}
				})
			})
		}
	}
}
