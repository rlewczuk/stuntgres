package reference_test

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"stuntgres"
)

// This host's registration grants connection lifecycle authority only. Scan
// callbacks reject rather than fabricate a scan, row or transport success.
type connectionAliasBinding struct{}

func (connectionAliasBinding) GetForeignRelSize(context.Context, stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	return stuntgres.ForeignRelSize{}, &stuntgres.ExternalSQLUnavailableError{Capability: "connection-only alias registration has no scans"}
}
func (connectionAliasBinding) GetForeignPaths(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	return nil, &stuntgres.ExternalSQLUnavailableError{Capability: "connection-only alias registration has no scans"}
}
func (connectionAliasBinding) GetForeignPlan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	return stuntgres.ForeignPlan{}, &stuntgres.ExternalSQLUnavailableError{Capability: "connection-only alias registration has no scans"}
}
func (connectionAliasBinding) BeginForeignScan(context.Context, stuntgres.ForeignRequest, stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	return nil, &stuntgres.ExternalSQLUnavailableError{Capability: "connection-only alias registration has no scans"}
}
func registerConnectionAlias(d *stuntgres.Driver) error {
	return d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "dbc_alias_handler"}, ExternalProvider: "dblink_fixture", Wrapper: connectionAliasBinding{}})
}
func connectionAliasPhysical(s *connectionScenario) {
	t := s.t
	base, err := pgconn.ParseConfig(s.conninfo("base_alias", true))
	if err != nil {
		t.Fatal(err)
	}
	app := fmt.Sprintf("dbc_alias_%d", connectionSequence.Add(1))
	handler := "NO HANDLER"
	if !s.b.PG {
		handler = "HANDLER public.dbc_alias_handler"
	}
	s.exec("CREATE FOREIGN DATA WRAPPER dbc_remote " + handler + " NO VALIDATOR OPTIONS(host '/dbc_wrapper_option_must_be_ignored')")
	defer s.exec("DROP FOREIGN DATA WRAPPER dbc_remote CASCADE")
	s.exec("CREATE SERVER dbc_bound_alias FOREIGN DATA WRAPPER dbc_remote OPTIONS(host " + pq.QuoteLiteral(base.Host) + ",port " + pq.QuoteLiteral(strconv.Itoa(int(base.Port))) + ",dbname " + pq.QuoteLiteral(base.Database) + ",sslmode 'disable',application_name " + pq.QuoteLiteral(app) + ")")
	s.exec("CREATE USER MAPPING FOR PUBLIC SERVER dbc_bound_alias OPTIONS(user 'dbc_public_mapping_must_not_win',password 'wrong')")
	s.exec("CREATE USER MAPPING FOR CURRENT_USER SERVER dbc_bound_alias OPTIONS(user " + pq.QuoteLiteral(base.User) + ",password " + pq.QuoteLiteral(base.Password) + ")")
	s.exec("CREATE ROLE dbc_reader")
	s.exec("CREATE USER MAPPING FOR dbc_reader SERVER dbc_bound_alias OPTIONS(user " + pq.QuoteLiteral(base.User) + ",password " + pq.QuoteLiteral(base.Password) + ")")
	s.exec("GRANT USAGE ON SCHEMA dbc_ext TO dbc_reader")
	s.exec("GRANT USAGE ON FOREIGN SERVER dbc_bound_alias TO dbc_reader")
	s.exec("GRANT EXECUTE ON FUNCTION dbc_ext.dblink_connect_u(text,text) TO dbc_reader")
	s.exec("SET SESSION AUTHORIZATION dbc_reader")
	defer s.exec("RESET SESSION AUTHORIZATION")
	for _, function := range []string{"dblink_connect", "dblink_connect_u"} {
		s.run(connectionText("SELECT dbc_ext."+function+"($1::text,$2::text) AS value", "OK", "alias", "dbc_bound_alias"))
		pid := s.pid(app)
		if !s.b.PG {
			request := s.provider.LastRequestIdentity()
			role := "dbc_reader"
			if function == "dblink_connect_u" {
				role = "postgres"
			}
			if request.Role != role || !request.MappingPresent || request.ServerOID == 0 || request.MappingOID == 0 || !request.ConninfoPresent {
				t.Fatal("alias did not capture actual effective owner/mapping", request)
			}
			if s.wire(pid).Info().CredentialMechanism != "scram-sha-256" {
				t.Fatal("alias password route did not actually authenticate")
			}
		}
		s.disconnect(new("alias"), pid)
	}
	// A SQL alias without genuine host authority must not borrow the direct binding.
	s.exec("RESET SESSION AUTHORIZATION")
	if !s.b.PG {
		s.exec("CREATE FOREIGN DATA WRAPPER dbc_unbound NO HANDLER NO VALIDATOR")
		defer s.exec("DROP FOREIGN DATA WRAPPER dbc_unbound CASCADE")
		s.exec("CREATE SERVER dbc_unbound_alias FOREIGN DATA WRAPPER dbc_unbound OPTIONS(dbname 'not_opened')")
		s.exec("CREATE USER MAPPING FOR CURRENT_USER SERVER dbc_unbound_alias")
		before := s.activity()
		rows, err := s.c.QueryContext(s.p.Ctx, "SELECT dbc_ext.dblink_connect($1::text)", "dbc_unbound_alias")
		if rows != nil {
			rows.Close()
		}
		if _, ok := err.(*stuntgres.ExternalSQLUnavailableError); !ok {
			t.Fatal("unbound alias borrowed direct provider", err)
		}
		s.unchangedActivity(before)
	}
}
