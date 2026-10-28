//go:build !noservertests

package reference_test

import (
	"bytes"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingFunctionNotice(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			admin := serverAuthenticated(t, dsn)
			clientEncodingCommand(t, admin, "CREATE TABLE ce_function_notice(v int)", "CREATE TABLE")
			clientEncodingCommand(t, admin, `CREATE FUNCTION ce_function_notice_ok() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN INSERT INTO ce_function_notice VALUES(1); RAISE NOTICE USING MESSAGE=U&'\00E4\00F6', DETAIL=U&'\00E9', HINT=U&'\00F6'; RETURN 1; END $$`, "CREATE FUNCTION")
			clientEncodingCommand(t, admin, `CREATE FUNCTION ce_function_notice_bad() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN INSERT INTO ce_function_notice VALUES(2); RAISE NOTICE USING MESSAGE=U&'\20AC'; RETURN 1; END $$`, "CREATE FUNCTION")
			clientEncodingCommand(t, admin, `CREATE FUNCTION ce_function_notice_policy() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN INSERT INTO ce_function_notice VALUES(3); PERFORM set_config('client_encoding','LATIN1',false); RAISE NOTICE USING MESSAGE=U&'\20AC'; RETURN 1; END $$`, "CREATE FUNCTION")
			clientEncodingCommand(t, admin, `CREATE FUNCTION ce_function_notice_error() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN INSERT INTO ce_function_notice VALUES(4); PERFORM set_config('client_encoding','LATIN1',false); RAISE NOTICE USING MESSAGE=U&'\00E4'; RAISE EXCEPTION USING MESSAGE=U&'\00E9', DETAIL=U&'\00F6'; END $$`, "CREATE FUNCTION")
			oids := make(map[string]uint32)
			for _, name := range []string{"ce_function_notice_ok", "ce_function_notice_bad", "ce_function_notice_policy", "ce_function_notice_error"} {
				frames := admin.query(t, "SELECT '"+name+"()'::regprocedure::oid")
				serverWantTags(t, frames, "TDCZ")
				values := serverValues(t, frames)
				if len(values) != 1 || len(values[0]) != 1 {
					t.Fatalf("function OID: %s", clientEncodingTranscript(frames))
				}
				n, err := strconv.ParseUint(values[0][0], 10, 32)
				if err != nil || n == 0 {
					t.Fatalf("function OID %q: %v", values[0][0], err)
				}
				oids[name] = uint32(n)
			}
			call := func(t *testing.T, w *serverWire, name string) []serverFrame {
				t.Helper()
				// FunctionCall completes at its own ReadyForQuery, with no Sync.
				w.send(t, serverFunction(oids[name], nil, nil, 1))
				return w.ready(t)
			}
			t.Run("representable_order_and_bytes", func(t *testing.T) {
				w := clientEncodingLogin(t, dsn, "LATIN1")
				frames := call(t, w, "ce_function_notice_ok")
				serverWantTags(t, frames, "NVZ")
				clientEncodingDiagnostic(t, frames, 'N', map[byte]string{'S': "NOTICE", 'V': "NOTICE", 'C': "00000", 'M': "\xe4\xf6", 'D': "\xe9", 'H': "\xf6", 'W': "PL/pgSQL function ce_function_notice_ok() line 1 at RAISE"})
				if !bytes.Equal(frames[1].body, []byte{0, 0, 0, 4, 0, 0, 0, 1}) {
					t.Fatalf("FunctionCall binary result: %s", clientEncodingTranscript(frames))
				}
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames)
				serverWantReady(t, frames, 'I')
				clientEncodingStatus(t, frames, "client_encoding")
				serverScalar(t, admin, "SELECT v FROM ce_function_notice ORDER BY v", "1")
				serverScalar(t, w, "SELECT 42", "42")
			})
			for _, explicit := range []bool{false, true} {
				name := "autocommit"
				if explicit {
					name = "explicit_transaction"
				}
				t.Run(name, func(t *testing.T) {
					clientEncodingCommand(t, admin, "TRUNCATE ce_function_notice", "TRUNCATE TABLE")
					w := clientEncodingLogin(t, dsn, "LATIN1")
					if explicit {
						clientEncodingCommand(t, w, "BEGIN; INSERT INTO ce_function_notice VALUES(9)", "BEGIN", "INSERT 0 1")
					}
					frames := call(t, w, "ce_function_notice_bad")
					serverWantTags(t, frames, "EZ")
					clientEncodingError(t, frames, "22P05", clientEncodingEuroError, "")
					clientEncodingStatus(t, frames, "client_encoding")
					ready := byte('I')
					if explicit {
						ready = 'E'
					}
					serverWantReady(t, frames, ready)
					serverScalar(t, admin, "SELECT count(*) FROM ce_function_notice", "0")
					if explicit {
						frames = w.query(t, "SELECT 42")
						serverWantTags(t, frames, "EZ")
						clientEncodingError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
						serverWantReady(t, frames, 'E')
						frames = clientEncodingCommand(t, w, "ROLLBACK", "ROLLBACK")
						serverWantReady(t, frames, 'I')
					}
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "SELECT count(*) FROM ce_function_notice", "0")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			t.Run("failing_policy_before_unwind", func(t *testing.T) {
				clientEncodingCommand(t, admin, "TRUNCATE ce_function_notice", "TRUNCATE TABLE")
				w := clientEncodingLogin(t, dsn, "UTF8")
				frames := call(t, w, "ce_function_notice_policy")
				// The function selects LATIN1, then fails converting its Euro
				// notice. UTF8 restored by error unwind must not serialize the
				// queued original as a successful notice/result or commit DML.
				serverWantTags(t, frames, "EZ")
				clientEncodingError(t, frames, "22P05", clientEncodingEuroError, "")
				clientEncodingStatus(t, frames, "client_encoding")
				serverWantReady(t, frames, 'I')
				serverScalar(t, admin, "SELECT count(*) FROM ce_function_notice", "0")
				serverScalar(t, w, "SHOW client_encoding", "UTF8")
				serverScalar(t, w, "SELECT count(*) FROM ce_function_notice", "0")
				serverScalar(t, w, "SELECT 42", "42")
			})
			t.Run("diagnostic_bytes_use_failing_policy", func(t *testing.T) {
				clientEncodingCommand(t, admin, "TRUNCATE ce_function_notice", "TRUNCATE TABLE")
				w := clientEncodingLogin(t, dsn, "UTF8")
				frames := call(t, w, "ce_function_notice_error")
				serverWantTags(t, frames, "NEZ")
				clientEncodingDiagnostic(t, frames, 'N', map[byte]string{'S': "NOTICE", 'V': "NOTICE", 'C': "00000", 'M': "\xe4", 'W': "PL/pgSQL function ce_function_notice_error() line 1 at RAISE"})
				clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "P0001", 'M': "\xe9", 'D': "\xf6", 'W': "PL/pgSQL function ce_function_notice_error() line 1 at RAISE"})
				serverWantCompletions(t, frames)
				clientEncodingStatus(t, frames, "client_encoding")
				serverWantReady(t, frames, 'I')
				serverScalar(t, admin, "SELECT count(*) FROM ce_function_notice", "0")
				serverScalar(t, w, "SHOW client_encoding", "UTF8")
				serverScalar(t, w, "SELECT count(*) FROM ce_function_notice", "0")
				serverScalar(t, w, "SELECT 42", "42")
			})
		})
	}
}
