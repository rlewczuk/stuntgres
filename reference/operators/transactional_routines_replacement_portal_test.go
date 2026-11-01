//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

// Bind precedes replacement: moving Bind after DDL would miss the accepted
// relation-definition lifetime exercised here.
func TestReferenceTransactionalRoutineReplacementPortal(t *testing.T) {
	for _, kind := range []string{"default", "check", "foreign_action"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%v", kind, commit), func(t *testing.T) {
				f := fixture.NewServerFixture(t)
				for _, b := range f.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
						triggerProtocolCode(t, w.query(t, "CREATE FUNCTION portal_default() RETURNS integer RETURN 5; CREATE FUNCTION portal_check(integer) RETURNS boolean RETURN $1=5; CREATE TABLE portal_rows(v integer DEFAULT portal_default()); CREATE TABLE portal_checked(v integer CHECK(portal_check(v))); CREATE TABLE portal_parent(v integer PRIMARY KEY); INSERT INTO portal_parent VALUES(1),(5),(9); CREATE TABLE portal_child(v integer DEFAULT portal_default() REFERENCES portal_parent ON DELETE SET DEFAULT); INSERT INTO portal_child VALUES(1)"), "")
						query, tag, value, probe := "INSERT INTO portal_rows DEFAULT VALUES RETURNING v", "INSERT 0 1", "5", "SELECT count(*) FROM portal_rows"
						switch kind {
						case "check":
							value = "9"
							query, probe = "INSERT INTO portal_checked VALUES(9) RETURNING v", "SELECT count(*) FROM portal_checked"
						case "foreign_action":
							query, tag, value, probe = "DELETE FROM portal_parent WHERE v=1 RETURNING v", "DELETE 1", "1", "SELECT v FROM portal_child"
						}
						triggerProtocolCode(t, w.query(t, "BEGIN"), "")
						defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
						frames := w.cycle(t, &pgproto3.Parse{Name: "consumer", Query: query}, &pgproto3.Describe{ObjectType: 'S', Name: "consumer"}, &pgproto3.Bind{DestinationPortal: "consumer", PreparedStatement: "consumer"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolTags(t, frames, "1tT2Z")
						triggerProtocolReady(t, frames, 'T')
						triggerProtocolCode(t, w.query(t, "CREATE OR REPLACE FUNCTION portal_default() RETURNS integer RETURN 9; CREATE OR REPLACE FUNCTION portal_check(integer) RETURNS boolean RETURN $1=9"), "")
						frames = w.cycle(t, &pgproto3.Execute{Portal: "consumer"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolTags(t, frames, "DCZ")
						triggerProtocolValues(t, frames, [][]string{{value}})
						triggerProtocolCompletions(t, frames, tag)
						triggerProtocolReady(t, frames, 'T')
						if kind == "foreign_action" {
							triggerProtocolScalar(t, w, probe, "9")
						}
						if kind == "default" {
							// A genuinely new Bind uses the successor; the accepted
							// portal above keeps its original planned DEFAULT.
							fresh := w.cycle(t, &pgproto3.Bind{DestinationPortal: "fresh", PreparedStatement: "consumer"}, &pgproto3.Execute{Portal: "fresh"})
							triggerProtocolCode(t, fresh, "")
							triggerProtocolTags(t, fresh, "2DCZ")
							triggerProtocolValues(t, fresh, [][]string{{"9"}})
							triggerProtocolCompletions(t, fresh, tag)
							triggerProtocolReady(t, fresh, 'T')
						}
						end, after := "ROLLBACK", "0"
						if commit {
							end, after = "COMMIT", "1"
							if kind == "default" {
								after = "2"
							}
						}
						if kind == "foreign_action" {
							after = "1"
							if commit {
								after = "9"
							}
						}
						frames = w.query(t, end)
						triggerProtocolCode(t, frames, "")
						triggerProtocolReady(t, frames, 'I')
						triggerProtocolScalar(t, w, probe, after)
					})
				}
			})
		}
	}
}

// Sequence state distinguishes an unevaluated accepted recipe from a cached
// value: neither Bind nor an empty source may invoke the volatile DEFAULT.
func TestReferenceTransactionalRoutineDefaultDemand(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
			triggerProtocolCode(t, w.query(t, "CREATE SEQUENCE portal_demand_seq START 100; CREATE FUNCTION portal_demand() RETURNS bigint RETURN nextval('portal_demand_seq')+5; CREATE TABLE portal_demand_rows(v bigint DEFAULT portal_demand(), marker integer)"), "")
			triggerProtocolCode(t, w.query(t, "BEGIN"), "")
			defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
			frames := w.cycle(t,
				&pgproto3.Parse{Name: "demand", Query: "INSERT INTO portal_demand_rows DEFAULT VALUES RETURNING v"},
				&pgproto3.Describe{ObjectType: 'S', Name: "demand"},
				&pgproto3.Bind{DestinationPortal: "first", PreparedStatement: "demand"},
				&pgproto3.Bind{DestinationPortal: "second", PreparedStatement: "demand"},
				&pgproto3.Parse{Name: "empty", Query: "INSERT INTO portal_demand_rows(marker) SELECT 1 WHERE false"},
				&pgproto3.Bind{DestinationPortal: "empty", PreparedStatement: "empty"})
			triggerProtocolCode(t, frames, "")
			triggerProtocolScalar(t, w, "SELECT is_called::text FROM portal_demand_seq", "false")
			triggerProtocolCode(t, w.query(t, "CREATE OR REPLACE FUNCTION portal_demand() RETURNS bigint RETURN nextval('portal_demand_seq')+9"), "")
			frames = w.cycle(t, &pgproto3.Execute{Portal: "empty"})
			triggerProtocolCode(t, frames, "")
			triggerProtocolCompletions(t, frames, "INSERT 0 0")
			triggerProtocolScalar(t, w, "SELECT is_called::text FROM portal_demand_seq", "false")
			for i, name := range []string{"first", "second"} {
				frames = w.cycle(t, &pgproto3.Execute{Portal: name})
				triggerProtocolCode(t, frames, "")
				triggerProtocolTags(t, frames, "DCZ")
				triggerProtocolValues(t, frames, [][]string{{fmt.Sprint(105 + i)}})
				triggerProtocolCompletions(t, frames, "INSERT 0 1")
				triggerProtocolReady(t, frames, 'T')
			}
			frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "fresh", PreparedStatement: "demand"}, &pgproto3.Execute{Portal: "fresh"})
			triggerProtocolCode(t, frames, "")
			triggerProtocolValues(t, frames, [][]string{{"111"}})
			triggerProtocolCompletions(t, frames, "INSERT 0 1")
			triggerProtocolScalar(t, w, "SELECT last_value FROM portal_demand_seq", "102")
			triggerProtocolCode(t, w.query(t, "ROLLBACK"), "")
			triggerProtocolScalar(t, w, "SELECT count(*) FROM portal_demand_rows", "0")
		})
	}
}

func TestReferenceTransactionalRoutineUpdateDefaults(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			t.Run(fmt.Sprintf("conflict=%v/replace=%v", conflict, replace), func(t *testing.T) {
				f := fixture.NewServerFixture(t)
				for _, b := range f.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
						triggerProtocolCode(t, w.query(t, "CREATE DOMAIN update_default_domain AS integer; CREATE FUNCTION update_default() RETURNS integer RETURN 5; CREATE FUNCTION update_check(integer,integer,integer) RETURNS boolean RETURN ($1=$2 AND $3=9); CREATE TABLE update_default_rows(id integer PRIMARY KEY,v update_default_domain DEFAULT update_default(),w update_default_domain DEFAULT update_default(),guard integer CHECK(update_check(v,w,guard))); INSERT INTO update_default_rows VALUES(1,1,1,9)"), "")
						guard := "9"
						if replace {
							guard = "7"
						}
						query, tag := "UPDATE update_default_rows SET v=DEFAULT,w=DEFAULT,guard="+guard+" WHERE id=1 RETURNING v,w,guard", "UPDATE 1"
						if conflict {
							query = "INSERT INTO update_default_rows VALUES(1,0,0," + guard + ") ON CONFLICT(id) DO UPDATE SET v=DEFAULT,w=DEFAULT,guard=" + guard + " RETURNING v,w,guard"
							tag = "INSERT 0 1"
						}
						triggerProtocolCode(t, w.query(t, "BEGIN"), "")
						defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
						frames := w.cycle(t, &pgproto3.Parse{Name: "update_defaults", Query: query}, &pgproto3.Describe{ObjectType: 'S', Name: "update_defaults"}, &pgproto3.Bind{DestinationPortal: "old_update", PreparedStatement: "update_defaults"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolTags(t, frames, "1tT2Z")
						routineDefaultPortalDescription(t, frames, []string{"v", "w", "guard"}, []uint32{23, 23, 23})
						if replace {
							triggerProtocolCode(t, w.query(t, "CREATE OR REPLACE FUNCTION update_default() RETURNS integer RETURN 9; CREATE OR REPLACE FUNCTION update_check(integer,integer,integer) RETURNS boolean RETURN ($1=$2 AND $3=7)"), "")
						}
						frames = w.cycle(t, &pgproto3.Execute{Portal: "old_update"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolTags(t, frames, "DCZ")
						triggerProtocolValues(t, frames, [][]string{{"5", "5", guard}})
						triggerProtocolCompletions(t, frames, tag)
						triggerProtocolReady(t, frames, 'T')
						fresh := "5"
						if replace {
							fresh = "9"
						}
						frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "new_update", PreparedStatement: "update_defaults"}, &pgproto3.Execute{Portal: "new_update"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolValues(t, frames, [][]string{{fresh, fresh, guard}})
						triggerProtocolCompletions(t, frames, tag)
						triggerProtocolTags(t, frames, "2DCZ")
						triggerProtocolReady(t, frames, 'T')
						triggerProtocolCode(t, w.query(t, "SAVEPOINT update_check_failure"), "")
						failed := w.query(t, "UPDATE update_default_rows SET v=5,w=9")
						want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "update_default_rows" violates check constraint "update_default_rows_check"`)
						want.Detail = "Failing row contains (1, 5, 9, " + guard + ")."
						want.Schema, want.Table, want.Constraint = "public", "update_default_rows", "update_default_rows_check"
						routineDefaultPortalDiagnostic(t, failed, want)
						triggerProtocolReady(t, failed, 'E')
						triggerProtocolCode(t, w.query(t, "ROLLBACK TO update_check_failure"), "")
						triggerProtocolScalar(t, w, "SELECT v FROM update_default_rows", fresh)
						triggerProtocolCode(t, w.query(t, "ROLLBACK"), "")
						triggerProtocolScalar(t, w, "SELECT v FROM update_default_rows", "1")
					})
				}
			})
		}
	}
}

func TestReferenceTransactionalRoutineDefaultOwnerTransfer(t *testing.T) {
	for _, kind := range []string{"plain", "runtime_invoker", "security_definer"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"portal_default_owner", "portal_default_actor"})
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
					declaration, usage, want := "RETURNS integer RETURN 5", "integer", "5"
					if kind == "runtime_invoker" {
						// Configuration prevents SQL inlining: this witness must
						// retain an actual invocation for Execute-time ACL checks.
						declaration = "RETURNS integer SET search_path=public RETURN 5"
					}
					if kind == "security_definer" {
						declaration, usage, want = "RETURNS text SECURITY DEFINER RETURN current_user::text", "text", "portal_default_owner"
					}
					triggerProtocolCode(t, w.query(t, "CREATE ROLE portal_default_owner; CREATE ROLE portal_default_actor; GRANT USAGE ON SCHEMA public TO portal_default_owner,portal_default_actor; CREATE FUNCTION owner_default() "+declaration+"; CREATE TABLE owner_default_rows(v "+usage+" DEFAULT owner_default()); GRANT INSERT,SELECT ON owner_default_rows TO portal_default_actor; REVOKE EXECUTE ON FUNCTION owner_default() FROM PUBLIC; GRANT EXECUTE ON FUNCTION owner_default() TO portal_default_actor; SET ROLE portal_default_actor"), "")
					triggerProtocolCode(t, w.query(t, "BEGIN"), "")
					defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK; RESET ROLE"), "") }()
					frames := w.cycle(t, &pgproto3.Parse{Name: "owner_default", Query: "INSERT INTO owner_default_rows DEFAULT VALUES RETURNING v"}, &pgproto3.Describe{ObjectType: 'S', Name: "owner_default"}, &pgproto3.Bind{DestinationPortal: "old_owner", PreparedStatement: "owner_default"})
					triggerProtocolCode(t, frames, "")
					triggerProtocolTags(t, frames, "1tT2Z")
					oid := uint32(23)
					if kind == "security_definer" {
						oid = 25
					}
					routineDefaultPortalDescription(t, frames, []string{"v"}, []uint32{oid})
					triggerProtocolCode(t, w.query(t, "RESET ROLE; ALTER FUNCTION owner_default() OWNER TO portal_default_owner; SET ROLE portal_default_actor"), "")
					frames = w.cycle(t, &pgproto3.Execute{Portal: "old_owner"})
					triggerProtocolCode(t, frames, "")
					triggerProtocolValues(t, frames, [][]string{{want}})
					triggerProtocolCompletions(t, frames, "INSERT 0 1")
					triggerProtocolReady(t, frames, 'T')
					frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "new_owner", PreparedStatement: "owner_default"}, &pgproto3.Execute{Portal: "new_owner"})
					triggerProtocolCode(t, frames, "")
					triggerProtocolValues(t, frames, [][]string{{want}})
					triggerProtocolCompletions(t, frames, "INSERT 0 1")
					triggerProtocolScalar(t, w, "SELECT count(*) FROM owner_default_rows", "2")
					if kind != "plain" {
						// Both portals are genuinely accepted while EXECUTE is granted.
						// Only the denied one is consumed by the failed subtransaction.
						frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "denied_owner", PreparedStatement: "owner_default"}, &pgproto3.Bind{DestinationPortal: "regranted_owner", PreparedStatement: "owner_default"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolCode(t, w.query(t, "RESET ROLE; REVOKE EXECUTE ON FUNCTION owner_default() FROM portal_default_actor; SET ROLE portal_default_actor; SAVEPOINT owner_permission"), "")
						frames = w.cycle(t, &pgproto3.Execute{Portal: "denied_owner"})
						triggerProtocolError(t, frames, "42501", "permission denied for function owner_default", "")
						triggerProtocolReady(t, frames, 'E')
						triggerProtocolCode(t, w.query(t, "ROLLBACK TO owner_permission; RESET ROLE; GRANT EXECUTE ON FUNCTION owner_default() TO portal_default_actor; SET ROLE portal_default_actor"), "")
						triggerProtocolScalar(t, w, "SELECT count(*) FROM owner_default_rows", "2")
						frames = w.cycle(t, &pgproto3.Execute{Portal: "regranted_owner"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolValues(t, frames, [][]string{{want}})
						triggerProtocolCompletions(t, frames, "INSERT 0 1")
						triggerProtocolReady(t, frames, 'T')
						triggerProtocolScalar(t, w, "SELECT count(*) FROM owner_default_rows", "3")
					}
					triggerProtocolCode(t, w.query(t, "ROLLBACK; RESET ROLE"), "")
					triggerProtocolScalar(t, w, "SELECT count(*) FROM owner_default_rows", "0")
				})
			}
		})
	}
}

func TestReferenceTransactionalRoutineQuotedDefault(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, language := range []string{"SQL", "plpgsql"} {
			t.Run(fmt.Sprintf("%s/commit=%v", language, commit), func(t *testing.T) {
				f := fixture.NewServerFixture(t)
				for _, b := range f.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
						body5, body9, want := "SELECT 5", "SELECT 9", "5"
						if language == "plpgsql" {
							body5, body9, want = "BEGIN RETURN 5; END", "BEGIN RETURN 9; END", "9"
						}
						triggerProtocolCode(t, w.query(t, "CREATE FUNCTION quoted_default() RETURNS integer LANGUAGE "+language+" AS $$"+body5+"$$; CREATE TABLE quoted_default_rows(v integer DEFAULT quoted_default())"), "")
						triggerProtocolCode(t, w.query(t, "BEGIN"), "")
						defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
						frames := w.cycle(t, &pgproto3.Parse{Name: "quoted_default", Query: "INSERT INTO quoted_default_rows DEFAULT VALUES RETURNING v"}, &pgproto3.Describe{ObjectType: 'S', Name: "quoted_default"}, &pgproto3.Bind{DestinationPortal: "old_quoted", PreparedStatement: "quoted_default"})
						triggerProtocolCode(t, frames, "")
						routineDefaultPortalDescription(t, frames, []string{"v"}, []uint32{23})
						triggerProtocolCode(t, w.query(t, "CREATE OR REPLACE FUNCTION quoted_default() RETURNS integer LANGUAGE "+language+" AS $$"+body9+"$$"), "")
						frames = w.cycle(t, &pgproto3.Execute{Portal: "old_quoted"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolValues(t, frames, [][]string{{want}})
						triggerProtocolCompletions(t, frames, "INSERT 0 1")
						triggerProtocolTags(t, frames, "DCZ")
						triggerProtocolReady(t, frames, 'T')
						frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "new_quoted", PreparedStatement: "quoted_default"}, &pgproto3.Execute{Portal: "new_quoted"})
						triggerProtocolCode(t, frames, "")
						triggerProtocolValues(t, frames, [][]string{{"9"}})
						triggerProtocolTags(t, frames, "2DCZ")
						triggerProtocolCompletions(t, frames, "INSERT 0 1")
						triggerProtocolReady(t, frames, 'T')
						end, count, current := "ROLLBACK", "0", "5"
						if commit {
							end, count, current = "COMMIT", "2", "9"
						}
						frames = w.query(t, end)
						triggerProtocolCode(t, frames, "")
						triggerProtocolReady(t, frames, 'I')
						triggerProtocolScalar(t, w, "SELECT count(*) FROM quoted_default_rows", count)
						triggerProtocolScalar(t, w, "SELECT quoted_default()", current)
						if commit {
							frames = w.query(t, "SELECT v FROM quoted_default_rows ORDER BY v")
							triggerProtocolCode(t, frames, "")
							triggerProtocolValues(t, frames, [][]string{{want}, {"9"}})
						}
					})
				}
			})
		}
	}
}

func TestReferenceTransactionalRoutineDefaultArguments(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
			triggerProtocolCode(t, w.query(t, "CREATE FUNCTION portal_argument(a integer) RETURNS integer RETURN a+1; CREATE TABLE portal_argument_rows(folded integer DEFAULT portal_argument(a=>1+3),nested integer DEFAULT portal_argument(portal_argument(4)),siblings integer DEFAULT portal_argument(4)+portal_argument(9))"), "")
			triggerProtocolCode(t, w.query(t, "BEGIN"), "")
			defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
			frames := w.cycle(t, &pgproto3.Parse{Name: "argument", Query: "INSERT INTO portal_argument_rows DEFAULT VALUES RETURNING folded,nested,siblings"}, &pgproto3.Describe{ObjectType: 'S', Name: "argument"}, &pgproto3.Bind{DestinationPortal: "old_argument", PreparedStatement: "argument"})
			triggerProtocolCode(t, frames, "")
			triggerProtocolTags(t, frames, "1tT2Z")
			routineDefaultPortalDescription(t, frames, []string{"folded", "nested", "siblings"}, []uint32{23, 23, 23})
			triggerProtocolScalar(t, w, "SELECT count(*) FROM portal_argument_rows", "0")
			triggerProtocolCode(t, w.query(t, "CREATE OR REPLACE FUNCTION portal_argument(a integer) RETURNS integer RETURN a+10"), "")
			frames = w.cycle(t, &pgproto3.Execute{Portal: "old_argument"})
			triggerProtocolCode(t, frames, "")
			triggerProtocolValues(t, frames, [][]string{{"5", "6", "15"}})
			triggerProtocolCompletions(t, frames, "INSERT 0 1")
			triggerProtocolReady(t, frames, 'T')
			frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "new_argument", PreparedStatement: "argument"}, &pgproto3.Execute{Portal: "new_argument"})
			triggerProtocolCode(t, frames, "")
			triggerProtocolValues(t, frames, [][]string{{"14", "24", "33"}})
			triggerProtocolCompletions(t, frames, "INSERT 0 1")
			triggerProtocolReady(t, frames, 'T')
			triggerProtocolScalar(t, w, "SELECT count(*) FROM portal_argument_rows", "2")
			triggerProtocolCode(t, w.query(t, "ROLLBACK"), "")
			triggerProtocolScalar(t, w, "SELECT count(*) FROM portal_argument_rows", "0")
		})
	}
}

// Each written call retains its own source/prepared association. A current PL
// provider must not change a neighboring SQL predecessor or its prepared inputs.
func TestReferenceTransactionalRoutineCurrentProviderModes(t *testing.T) {
	t.Run("mixed-arguments", func(t *testing.T) {
		f := fixture.NewServerFixture(t)
		for _, b := range f.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
				triggerProtocolCode(t, w.query(t, "CREATE FUNCTION mode_sql(a integer) RETURNS integer RETURN a+1; CREATE FUNCTION mode_quoted(a integer) RETURNS integer LANGUAGE SQL AS $$SELECT a+1$$; CREATE FUNCTION mode_pl(a integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a+2; END$$; CREATE FUNCTION mode_named(a integer,b integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a*10+b+2; END$$; CREATE FUNCTION mode_variadic(VARIADIC a integer[]) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a[1]*10+a[2]+2; END$$; CREATE TABLE mode_rows(s integer DEFAULT mode_sql(3),p integer DEFAULT mode_pl(3),sp integer DEFAULT mode_sql(mode_pl(3)),ps integer DEFAULT mode_pl(mode_sql(3)),siblings integer DEFAULT mode_sql(3)+mode_pl(3),folded integer DEFAULT mode_pl(a=>1+2),named integer DEFAULT mode_named(b=>2,a=>1+2),expanded integer DEFAULT mode_variadic(1+2,8),packed integer DEFAULT mode_variadic(VARIADIC ARRAY[3,8]),quoted integer DEFAULT mode_quoted(3),pp integer DEFAULT mode_pl(mode_pl(3)))"), "")
				triggerProtocolCode(t, w.query(t, "BEGIN"), "")
				defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
				frames := w.cycle(t, &pgproto3.Parse{Name: "modes", Query: "INSERT INTO mode_rows DEFAULT VALUES RETURNING s,p,sp,ps,siblings,folded,named,expanded,packed,quoted,pp"}, &pgproto3.Describe{ObjectType: 'S', Name: "modes"}, &pgproto3.Bind{DestinationPortal: "old_modes", PreparedStatement: "modes"})
				triggerProtocolCode(t, frames, "")
				triggerProtocolTags(t, frames, "1tT2Z")
				routineDefaultPortalDescription(t, frames, []string{"s", "p", "sp", "ps", "siblings", "folded", "named", "expanded", "packed", "quoted", "pp"}, []uint32{23, 23, 23, 23, 23, 23, 23, 23, 23, 23, 23})
				triggerProtocolScalar(t, w, "SELECT count(*) FROM mode_rows", "0")
				triggerProtocolCode(t, w.query(t, "CREATE OR REPLACE FUNCTION mode_sql(a integer) RETURNS integer RETURN a+10; CREATE OR REPLACE FUNCTION mode_quoted(a integer) RETURNS integer LANGUAGE SQL AS $$SELECT a+10$$; CREATE OR REPLACE FUNCTION mode_pl(a integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a+20; END$$; CREATE OR REPLACE FUNCTION mode_named(a integer,b integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a*10+b+20; END$$; CREATE OR REPLACE FUNCTION mode_variadic(VARIADIC a integer[]) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a[1]*10+a[2]+20; END$$"), "")
				frames = w.cycle(t, &pgproto3.Execute{Portal: "old_modes"})
				triggerProtocolCode(t, frames, "")
				triggerProtocolTags(t, frames, "DCZ")
				triggerProtocolValues(t, frames, [][]string{{"4", "23", "24", "24", "27", "23", "52", "58", "58", "4", "43"}})
				triggerProtocolCompletions(t, frames, "INSERT 0 1")
				triggerProtocolReady(t, frames, 'T')
				frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "new_modes", PreparedStatement: "modes"}, &pgproto3.Execute{Portal: "new_modes"})
				triggerProtocolCode(t, frames, "")
				triggerProtocolTags(t, frames, "2DCZ")
				triggerProtocolValues(t, frames, [][]string{{"13", "23", "33", "33", "36", "23", "52", "58", "58", "13", "43"}})
				triggerProtocolCompletions(t, frames, "INSERT 0 1")
				triggerProtocolReady(t, frames, 'T')
				triggerProtocolScalar(t, w, "SELECT count(*) FROM mode_rows", "2")
				triggerProtocolCode(t, w.query(t, "ROLLBACK"), "")
				triggerProtocolScalar(t, w, "SELECT count(*) FROM mode_rows", "0")
				triggerProtocolScalar(t, w, "SELECT mode_pl(3)", "5")
			})
		}
	})
	t.Run("volatile-demand", func(t *testing.T) {
		f := fixture.NewServerFixture(t)
		for _, b := range f.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
				triggerProtocolCode(t, w.query(t, "CREATE SEQUENCE mode_demand_seq START 100; CREATE FUNCTION mode_demand() RETURNS bigint LANGUAGE plpgsql AS $$BEGIN RETURN nextval('mode_demand_seq')+2; END$$; CREATE TABLE mode_demand_rows(v bigint DEFAULT mode_demand(),marker integer)"), "")
				triggerProtocolCode(t, w.query(t, "BEGIN"), "")
				defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
				frames := w.cycle(t, &pgproto3.Parse{Name: "mode_demand", Query: "INSERT INTO mode_demand_rows DEFAULT VALUES RETURNING v"}, &pgproto3.Describe{ObjectType: 'S', Name: "mode_demand"}, &pgproto3.Bind{DestinationPortal: "first_mode", PreparedStatement: "mode_demand"}, &pgproto3.Bind{DestinationPortal: "second_mode", PreparedStatement: "mode_demand"}, &pgproto3.Parse{Name: "empty_mode", Query: "INSERT INTO mode_demand_rows(marker) SELECT 1 WHERE false"}, &pgproto3.Bind{DestinationPortal: "empty_mode", PreparedStatement: "empty_mode"})
				triggerProtocolCode(t, frames, "")
				routineDefaultPortalDescription(t, frames, []string{"v"}, []uint32{20})
				triggerProtocolScalar(t, w, "SELECT is_called::text FROM mode_demand_seq", "false")
				triggerProtocolCode(t, w.query(t, "CREATE OR REPLACE FUNCTION mode_demand() RETURNS bigint LANGUAGE plpgsql AS $$BEGIN RETURN nextval('mode_demand_seq')+20; END$$"), "")
				frames = w.cycle(t, &pgproto3.Execute{Portal: "empty_mode"})
				triggerProtocolCode(t, frames, "")
				triggerProtocolCompletions(t, frames, "INSERT 0 0")
				triggerProtocolScalar(t, w, "SELECT is_called::text FROM mode_demand_seq", "false")
				for i, portal := range []string{"first_mode", "second_mode"} {
					frames = w.cycle(t, &pgproto3.Execute{Portal: portal})
					triggerProtocolCode(t, frames, "")
					triggerProtocolValues(t, frames, [][]string{{fmt.Sprint(120 + i)}})
					triggerProtocolCompletions(t, frames, "INSERT 0 1")
					triggerProtocolReady(t, frames, 'T')
				}
				frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "fresh_mode", PreparedStatement: "mode_demand"}, &pgproto3.Execute{Portal: "fresh_mode"})
				triggerProtocolCode(t, frames, "")
				triggerProtocolValues(t, frames, [][]string{{"122"}})
				triggerProtocolCompletions(t, frames, "INSERT 0 1")
				triggerProtocolScalar(t, w, "SELECT last_value FROM mode_demand_seq", "102")
				triggerProtocolCode(t, w.query(t, "ROLLBACK"), "")
				triggerProtocolScalar(t, w, "SELECT count(*) FROM mode_demand_rows", "0")
			})
		}
	})
	t.Run("current-owner-and-execute", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"mode_owner", "mode_actor"})
		f := fixture.NewServerFixture(t)
		for _, b := range f.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
				triggerProtocolCode(t, w.query(t, "CREATE ROLE mode_owner; CREATE ROLE mode_actor; GRANT USAGE ON SCHEMA public TO mode_owner,mode_actor; CREATE FUNCTION mode_owner_value() RETURNS text SECURITY DEFINER LANGUAGE plpgsql AS $$BEGIN RETURN 'old:'||current_user::text; END$$; CREATE TABLE mode_owner_rows(v text DEFAULT mode_owner_value()); GRANT INSERT,SELECT ON mode_owner_rows TO mode_actor; REVOKE EXECUTE ON FUNCTION mode_owner_value() FROM PUBLIC; GRANT EXECUTE ON FUNCTION mode_owner_value() TO mode_actor; SET ROLE mode_actor"), "")
				triggerProtocolCode(t, w.query(t, "BEGIN"), "")
				defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK; RESET ROLE"), "") }()
				frames := w.cycle(t, &pgproto3.Parse{Name: "mode_owner", Query: "INSERT INTO mode_owner_rows DEFAULT VALUES RETURNING v"}, &pgproto3.Describe{ObjectType: 'S', Name: "mode_owner"}, &pgproto3.Bind{DestinationPortal: "old_mode_owner", PreparedStatement: "mode_owner"}, &pgproto3.Bind{DestinationPortal: "denied_mode_owner", PreparedStatement: "mode_owner"}, &pgproto3.Bind{DestinationPortal: "regranted_mode_owner", PreparedStatement: "mode_owner"})
				triggerProtocolCode(t, frames, "")
				routineDefaultPortalDescription(t, frames, []string{"v"}, []uint32{25})
				triggerProtocolCode(t, w.query(t, "RESET ROLE; CREATE OR REPLACE FUNCTION mode_owner_value() RETURNS text SECURITY DEFINER LANGUAGE plpgsql AS $$BEGIN RETURN 'new:'||current_user::text; END$$; ALTER FUNCTION mode_owner_value() OWNER TO mode_owner; SET ROLE mode_actor"), "")
				frames = w.cycle(t, &pgproto3.Execute{Portal: "old_mode_owner"})
				triggerProtocolCode(t, frames, "")
				triggerProtocolValues(t, frames, [][]string{{"new:mode_owner"}})
				triggerProtocolCompletions(t, frames, "INSERT 0 1")
				triggerProtocolCode(t, w.query(t, "RESET ROLE; REVOKE EXECUTE ON FUNCTION mode_owner_value() FROM mode_actor; SET ROLE mode_actor; SAVEPOINT mode_permission"), "")
				frames = w.cycle(t, &pgproto3.Execute{Portal: "denied_mode_owner"})
				triggerProtocolError(t, frames, "42501", "permission denied for function mode_owner_value", "")
				triggerProtocolReady(t, frames, 'E')
				triggerProtocolCode(t, w.query(t, "ROLLBACK TO mode_permission; RESET ROLE; GRANT EXECUTE ON FUNCTION mode_owner_value() TO mode_actor; SET ROLE mode_actor"), "")
				triggerProtocolScalar(t, w, "SELECT count(*) FROM mode_owner_rows", "1")
				frames = w.cycle(t, &pgproto3.Execute{Portal: "regranted_mode_owner"})
				triggerProtocolCode(t, frames, "")
				triggerProtocolValues(t, frames, [][]string{{"new:mode_owner"}})
				triggerProtocolCompletions(t, frames, "INSERT 0 1")
				triggerProtocolReady(t, frames, 'T')
				frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "fresh_mode_owner", PreparedStatement: "mode_owner"}, &pgproto3.Execute{Portal: "fresh_mode_owner"})
				triggerProtocolCode(t, frames, "")
				triggerProtocolValues(t, frames, [][]string{{"new:mode_owner"}})
				triggerProtocolCompletions(t, frames, "INSERT 0 1")
				triggerProtocolCode(t, w.query(t, "ROLLBACK; RESET ROLE"), "")
				triggerProtocolScalar(t, w, "SELECT count(*) FROM mode_owner_rows", "0")
			})
		}
	})
}

func routineDefaultPortalDescription(t *testing.T, frames [][]byte, names []string, types []uint32) {
	t.Helper()
	count := 0
	for _, frame := range frames {
		if frame[0] != 'T' {
			continue
		}
		var description pgproto3.RowDescription
		if err := description.Decode(frame[5:]); err != nil {
			t.Fatal(err)
		}
		count++
		if len(description.Fields) != len(names) {
			t.Fatal("wrong DEFAULT result width", description.Fields)
		}
		for i, field := range description.Fields {
			if string(field.Name) != names[i] || field.DataTypeOID != types[i] || field.TypeModifier != -1 || field.Format != 0 {
				t.Fatal("wrong DEFAULT result metadata", description.Fields)
			}
		}
	}
	if count != 1 {
		t.Fatal("missing or duplicate DEFAULT RowDescription", count)
	}
}

func routineDefaultPortalDiagnostic(t *testing.T, frames [][]byte, want fixture.Diagnostic) {
	t.Helper()
	triggerProtocolCode(t, frames, want.Code)
	for _, frame := range frames {
		if frame[0] != 'E' {
			continue
		}
		var d pgproto3.ErrorResponse
		if err := d.Decode(frame[5:]); err != nil {
			t.Fatal(err)
		}
		got := fixture.Diagnostic{Severity: d.Severity, SeverityUnlocalized: d.SeverityUnlocalized, Code: d.Code, Message: d.Message, Detail: d.Detail, Hint: d.Hint, Position: fixture.Position(int(d.Position)), InternalPosition: fixture.Position(int(d.InternalPosition)), InternalQuery: d.InternalQuery, Context: d.Where, Schema: d.SchemaName, Table: d.TableName, Column: d.ColumnName, DataType: d.DataTypeName, Constraint: d.ConstraintName}
		if got != want {
			t.Fatalf("DEFAULT diagnostic %#v want %#v", got, want)
		}
	}
}
