package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectSeekPrepared(t *testing.T) {
	truncateScenario(t, []string{"los_seek_reader"}, func(s *truncateSession) {
		defer s.cleanup(928406, 928407)
		s.run(fixture.Select("SELECT oid::bigint,proname::text,proargtypes::text,prorettype::bigint,proisstrict,provolatile::text,proparallel::text,prosrc FROM pg_proc WHERE oid IN (956,958,3170,3171) ORDER BY oid",
			[]string{"oid", "proname", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "prosrc"},
			[]string{"INT8", "TEXT", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "TEXT"}, [][]any{
				{int64(956), "lo_lseek", "23 23 23", int64(23), true, "v", "u", "be_lo_lseek"},
				{int64(958), "lo_tell", "23", int64(23), true, "v", "u", "be_lo_tell"},
				{int64(3170), "lo_lseek64", "23 20 23", int64(20), true, "v", "u", "be_lo_lseek64"},
				{int64(3171), "lo_tell64", "23", int64(20), true, "v", "u", "be_lo_tell64"},
			}))
		s.create(928406, "616263646566")
		s.create(928407, "78797a")
		for _, api := range seekAPIs {
			for _, name := range []string{api.seek, api.tell} {
				cases := []struct{ args, signature string }{{"", ""}, {"true", "boolean"}, {"'0'::text", "text"}}
				if name == api.seek {
					cases = append(cases,
						struct{args, signature string}{"0::integer,0::"+api.offsetType, "integer, "+api.offsetType},
						struct{args, signature string}{"true,0::"+api.offsetType+",0::integer", "boolean, "+api.offsetType+", integer"},
						struct{args, signature string}{"0::integer,'0'::text,0::integer", "integer, text, integer"},
						struct{args, signature string}{"0::integer,0::"+api.offsetType+",0::integer,0::integer", "integer, "+api.offsetType+", integer, integer"})
				} else { cases = append(cases, struct{args, signature string}{"0::integer,0::integer", "integer, integer"}) }
				for _, tc := range cases {
					step := fixture.ImplicitError("SELECT "+name+"("+tc.args+")", fixture.Diagnostic{Code:"42883", Message:"function "+name+"("+tc.signature+") does not exist", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"8"})
					step.SelectRows = true
					s.run(step)
				}
			}
			bad := fixture.ImplicitError("SELECT lo_lseek(0::integer,2147483648::bigint,0::integer)", fixture.Diagnostic{Code:"42883", Message:"function lo_lseek(integer, bigint, integer) does not exist", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"8"})
			bad.SelectRows = true
			s.run(bad)
			for _, isSeek := range []bool{true, false} {
				step := tellValue(api, int64(0), int64(0))
				literal := truncateValue("pg_catalog."+api.tell+"(0::integer)", api.resultType, int64(0))
				if isSeek {
					step = seekValue(api, int64(0), 1, 1, int64(1))
					literal = truncateValue("pg_catalog."+api.seek+"(0::integer,1::"+api.offsetType+",1::integer)", api.resultType, int64(0))
				}
				held, stmt := s.held(step.Query)
				litHeld, litStmt := s.held(literal.Query)
				func() {
					defer stmt.Close()
					defer litStmt.Close()
					// Preparing both parameter and literal definitions did not open
					// a descriptor, move a cursor, or change content/pages.
					s.bytes(928406, 0, 20, "616263646566")
					s.pages(928406, []any{int64(0), int64(6), "616263646566"})
					s.run(fixture.Exec("BEGIN", 0))
					fd := s.open(928406, truncateWrite, 0)
					s.run(tell64(fd, 0))
					var retainedTypes []*sql.ColumnType
					capture := step
					capture.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						var err error
						retainedTypes, err = rows.ColumnTypes()
						if err != nil { t.Fatal(err) }
					}
					retained := s.execute(held, capture)
					position := int64(0)
					if isSeek { position = 1 }
					literal.Rows = [][]any{{position}}
					if isSeek { position++; literal.Rows = [][]any{{position}} }
					s.execute(litHeld, literal)
					s.run(tell64(fd, position))
					s.run(fixture.Exec("COMMIT", 0))
					s.execute(held, seekFailure(step, "42704", "invalid large-object descriptor: 0"))
					s.execute(litHeld, seekFailure(literal, "42704", "invalid large-object descriptor: 0"))
					s.run(fixture.Exec("BEGIN", 0))
					fd = s.open(928407, truncateWrite, 0)
					s.run(seek64(fd, 2, 0, 2))
					again := step
					position = 2
					if isSeek { position++ }
					again.Rows = [][]any{{position}}
					s.execute(held, again)
					if isSeek { position++ }
					literal.Rows = [][]any{{position}}
					s.execute(litHeld, literal)
					s.run(tell64(fd, position))
					s.run(seek64(fd, 0, 0, 0))
					s.read(fd, 3, "78797a")
					s.run(fixture.Exec("ROLLBACK", 0))
					want := int64(0)
					if isSeek { want = 1 }
					if len(retained.Columns) != 1 || retained.Columns[0] != "result" || len(retained.Rows) != 1 || len(retained.Rows[0]) != 1 || retained.Rows[0][0] != want || len(retainedTypes) != 1 || retainedTypes[0].DatabaseTypeName() != api.resultType {
						s.t.Fatalf("detached scalar/metadata changed: %+v types=%v", retained, retainedTypes)
					}
				}()
			}
		}

		peer := s.peer()
		defer peer.close()
		s.run(fixture.Exec("BEGIN", 0))
		fd := s.open(928406, truncateWrite, 0)
		s.run(seek64(fd, 2, 0, 2))
		for _, api := range seekAPIs {
			peer.run(seekFailure(seekValue(api, fd, 0, 0, nil), "42704", "invalid large-object descriptor: 0"))
			peer.run(seekFailure(tellValue(api, fd, nil), "42704", "invalid large-object descriptor: 0"))
		}
		peer.run(fixture.Exec("BEGIN", 0))
		other := peer.open(928406, truncateWrite, 0)
		peer.run(seek64(other, 4, 0, 4))
		s.run(tell64(fd, 2))
		peer.run(tell64(other, 4))
		peer.run(fixture.Exec("ROLLBACK", 0))
		s.run(fixture.Exec("SAVEPOINT los_child", 0))
		child := s.open(928406, truncateRead, 1)
		s.run(seek64(fd, 1, 1, 3))
		s.run(fixture.Exec("ROLLBACK TO SAVEPOINT los_child", 0))
		s.run(tell64(fd, 3))
		for _, api := range seekAPIs {
			for _, step := range []fixture.Step{seekValue(api, child, 0, 0, nil), tellValue(api, child, nil)} {
				s.recover(seekFailure(step, "42704", fmt.Sprintf("invalid large-object descriptor: %d", child)))
			}
		}
		s.run(fixture.Exec("SAVEPOINT los_close", 0))
		s.run(truncateValue("lo_close($1::integer)", "INT4", int64(0), fd))
		s.run(fixture.Exec("ROLLBACK TO SAVEPOINT los_close", 0))
		for _, api := range seekAPIs {
			s.recover(seekFailure(tellValue(api, fd, nil), "42704", "invalid large-object descriptor: 0"))
			s.recover(seekFailure(seekValue(api, fd, 0, 0, nil), "42704", "invalid large-object descriptor: 0"))
		}
		s.run(fixture.Exec("ROLLBACK", 0))

		s.run(fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 928406 TO los_seek_reader", 0))
		s.run(fixture.Exec("SET ROLE los_seek_reader", 0))
		s.run(fixture.Exec("BEGIN", 0))
		fd = s.open(928406, truncateWrite, 0)
		// Hold all four before the peer revokes object ACLs. Existing handles
		// retain access; opening another descriptor must recheck the ACL.
		var handles []fixture.HeldStatement
		var statements []*sql.Stmt
		for _, api := range seekAPIs {
			for _, step := range []fixture.Step{seekValue(api, fd, 2, 0, int64(2)), tellValue(api, fd, int64(2))} {
				h, stmt := s.held(step.Query)
				handles = append(handles, h)
				statements = append(statements, stmt)
			}
		}
		func() {
			defer func() { for _, stmt := range statements { stmt.Close() } }()
			peer.run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 928406 FROM los_seek_reader", 0))
			i := 0
			for _, api := range seekAPIs {
				for _, step := range []fixture.Step{seekValue(api, fd, 2, 0, int64(2)), tellValue(api, fd, int64(2))} { s.execute(handles[i], step); i++ }
			}
			s.recover(truncateError("SELECT lo_open(928406::oid,131072)", "42501", "permission denied for large object 928406"))
		}()
		s.run(fixture.Exec("ROLLBACK", 0))
		s.run(fixture.Exec("RESET ROLE", 0))
	})
}
