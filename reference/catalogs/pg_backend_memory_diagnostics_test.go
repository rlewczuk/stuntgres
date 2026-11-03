package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Initial candidates follow REL_18_6 parse_func.c and the existing released
// restricted-SRF view errors. Root resolves exposed fields and phases against
// the pinned oracle before the native regression; empty fields are asserted.
func TestReferenceBackendMemoryDiagnostics(t *testing.T) {
	cases:=[]struct{name,query,stage string; diagnostic fixture.Diagnostic}{
		{"insert","INSERT INTO pg_catalog.pg_backend_memory_contexts DEFAULT VALUES","prepare",fixture.Diagnostic{Code:"55000",Message:`cannot insert into view "pg_backend_memory_contexts"`,Detail:"Views that do not select from a single table or view are not automatically updatable.",Hint:"To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule."}},
		{"update","UPDATE pg_catalog.pg_backend_memory_contexts SET name='changed'","prepare",fixture.Diagnostic{Code:"55000",Message:`cannot update view "pg_backend_memory_contexts"`,Detail:"Views that do not select from a single table or view are not automatically updatable.",Hint:"To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."}},
		{"delete","DELETE FROM pg_catalog.pg_backend_memory_contexts","prepare",fixture.Diagnostic{Code:"55000",Message:`cannot delete from view "pg_backend_memory_contexts"`,Detail:"Views that do not select from a single table or view are not automatically updatable.",Hint:"To enable deleting from the view, provide an INSTEAD OF DELETE trigger or an unconditional ON DELETE DO INSTEAD rule."}},
		{"truncate","TRUNCATE pg_catalog.pg_backend_memory_contexts","execute",fixture.Diagnostic{Code:"42809",Message:`"pg_backend_memory_contexts" is not a table`}},
		{"integer_argument","SELECT pg_get_backend_memory_contexts(1)","prepare",fixture.Diagnostic{Code:"42883",Message:"function pg_get_backend_memory_contexts(integer) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)}},
		{"text_argument","/* é */ SELECT pg_get_backend_memory_contexts('x'::text)","prepare",fixture.Diagnostic{Code:"42883",Message:"function pg_get_backend_memory_contexts(text) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"star","SELECT pg_get_backend_memory_contexts(*)","prepare",fixture.Diagnostic{Code:"42809",Message:"pg_get_backend_memory_contexts(*) specified, but pg_get_backend_memory_contexts is not an aggregate function",Position:fixture.Position(8)}},
		{"window","SELECT pg_get_backend_memory_contexts() OVER ()","prepare",fixture.Diagnostic{Code:"42809",Message:"OVER specified, but pg_get_backend_memory_contexts is not a window function nor an aggregate function",Position:fixture.Position(8)}},
		{"filter","SELECT pg_get_backend_memory_contexts() FILTER (WHERE true)","prepare",fixture.Diagnostic{Code:"42809",Message:"FILTER specified, but pg_get_backend_memory_contexts is not an aggregate function",Position:fixture.Position(8)}},
		{"where","SELECT 1 WHERE pg_get_backend_memory_contexts() IS NOT NULL","prepare",fixture.Diagnostic{Code:"0A000",Message:"set-returning functions are not allowed in WHERE",Position:fixture.Position(16)}},
	}
	for _,tc:=range cases {t.Run(tc.name,func(t *testing.T){
		if tc.name=="text_argument"{tc.diagnostic.Position=fixture.Position(utf8.RuneCountInString(tc.query[:strings.Index(tc.query,"pg_get_backend_memory_contexts")])+1)}
		p:=fixture.NewScenario(t)
		for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
			c:=fixture.SequenceCoreConn(t,p.Ctx,b.DB)
			backendMemorySetupSentinel(t,p,b,c)
			for _,prepared:=range []bool{false,true}{
				s:=fixture.ImplicitError(tc.query,tc.diagnostic);s.SelectRows=strings.HasPrefix(tc.query,"SELECT")||strings.HasPrefix(tc.query,"/*");s.Prepared=prepared;s.ErrorStage="execute";if prepared{s.ErrorStage=tc.stage}
				fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
				backendMemorySentinel(t,p,b,c)
				backendMemoryRead(t,p,b,c,backendMemoryView,false)
				backendMemoryRead(t,p,b,c,backendMemoryFunction,false)
				backendMemoryInitialPrivileges(t,p,b,c)
			}
		})}
	})}
}
