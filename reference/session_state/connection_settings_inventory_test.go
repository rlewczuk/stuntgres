package reference_test

import (
 "database/sql"
 "fmt"
 "reflect"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

// Literal expectations from REL_18_6 guc_tables.c and the chosen simulated build.
// PostgreSQL initialized values are separate from release boot metadata.
type connectionSetting struct {
 name, kind, context, boot, description, category, unit, min, max, extra string
 restricted bool
 enums []string
}

var connectionSettings = []connectionSetting{
 {"authentication_timeout","integer","sighup","60","Sets the maximum allowed time to complete client authentication.","Authentication","s","1","600","",false,nil},
 {"bonjour","bool","postmaster","off","Enables advertising the server via Bonjour.","Connection Settings","","","","",false,nil},
 {"bonjour_name","string","postmaster","","Sets the Bonjour service name.","Connection Settings","","","","An empty string means use the computer name.",false,nil},
 {"client_connection_check_interval","integer","user","0","Sets the time interval between checks for disconnection while running queries.","TCP Settings","ms","0","2147483647","0 disables connection checks.",false,nil},
 {"gss_accept_delegation","bool","sighup","off","Sets whether GSSAPI delegation should be accepted from the client.","Authentication","","","","",false,nil},
 {"krb_caseins_users","bool","sighup","off","Sets whether Kerberos and GSSAPI user names should be treated as case-insensitive.","Authentication","","","","",false,nil},
 {"krb_server_keyfile","string","sighup","FILE:/etc/krb5.keytab","Sets the location of the Kerberos server key file.","Authentication","","","","",true,nil},
 {"listen_addresses","string","postmaster","localhost","Sets the host name or IP address(es) to listen to.","Connection Settings","","","","",false,nil},
 {"max_connections","integer","postmaster","100","Sets the maximum number of concurrent connections.","Connection Settings","","1","262143","",false,nil},
 {"md5_password_warnings","bool","user","on","Enables deprecation warnings for MD5 passwords.","Authentication","","","","",false,nil},
 {"oauth_validator_libraries","string","sighup","","Lists libraries that may be called to validate OAuth v2 bearer tokens.","Authentication","","","","",true,nil},
 {"password_encryption","enum","user","scram-sha-256","Chooses the algorithm for encrypting passwords.","Authentication","","","","",false,[]string{"md5","scram-sha-256"}},
 {"port","integer","postmaster","5432","Sets the TCP port the server listens on.","Connection Settings","","1","65535","",false,nil},
 {"reserved_connections","integer","postmaster","0","Sets the number of connection slots reserved for roles with privileges of pg_use_reserved_connections.","Connection Settings","","0","262143","",false,nil},
 {"scram_iterations","integer","user","4096","Sets the iteration count for SCRAM secret generation.","Authentication","","1","2147483647","",false,nil},
 {"ssl","bool","sighup","off","Enables SSL connections.","SSL","","","","",false,nil},
 {"ssl_ca_file","string","sighup","","Location of the SSL certificate authority file.","SSL","","","","",false,nil},
 {"ssl_cert_file","string","sighup","server.crt","Location of the SSL server certificate file.","SSL","","","","",false,nil},
 {"ssl_ciphers","string","sighup","HIGH:MEDIUM:+3DES:!aNULL","Sets the list of allowed TLSv1.2 (and lower) ciphers.","SSL","","","","",true,nil},
 {"ssl_crl_dir","string","sighup","","Location of the SSL certificate revocation list directory.","SSL","","","","",false,nil},
 {"ssl_crl_file","string","sighup","","Location of the SSL certificate revocation list file.","SSL","","","","",false,nil},
 {"ssl_dh_params_file","string","sighup","","Location of the SSL DH parameters file.","SSL","","","","An empty string means use compiled-in default parameters.",true,nil},
 {"ssl_groups","string","sighup","X25519:prime256v1","Sets the group(s) to use for Diffie-Hellman key exchange.","SSL","","","","Multiple groups can be specified using a colon-separated list.",true,nil},
 {"ssl_key_file","string","sighup","server.key","Location of the SSL server private key file.","SSL","","","","",false,nil},
 {"ssl_max_protocol_version","enum","sighup","","Sets the maximum SSL/TLS protocol version to use.","SSL","","","","",true,[]string{"","TLSv1","TLSv1.1","TLSv1.2","TLSv1.3"}},
 {"ssl_min_protocol_version","enum","sighup","TLSv1.2","Sets the minimum SSL/TLS protocol version to use.","SSL","","","","",true,[]string{"TLSv1","TLSv1.1","TLSv1.2","TLSv1.3"}},
 {"ssl_passphrase_command","string","sighup","","Command to obtain passphrases for SSL.","SSL","","","","An empty string means use the built-in prompting mechanism.",true,nil},
 {"ssl_passphrase_command_supports_reload","bool","sighup","off","Controls whether \"ssl_passphrase_command\" is called during server reload.","SSL","","","","",false,nil},
 {"ssl_prefer_server_ciphers","bool","sighup","on","Give priority to server ciphersuite order.","SSL","","","","",false,nil},
 {"ssl_tls13_ciphers","string","sighup","","Sets the list of allowed TLSv1.3 cipher suites.","SSL","","","","An empty string means use the default cipher suites.",true,nil},
 {"superuser_reserved_connections","integer","postmaster","3","Sets the number of connection slots reserved for superusers.","Connection Settings","","0","262143","",false,nil},
 {"tcp_keepalives_count","integer","user","0","Maximum number of TCP keepalive retransmits.","TCP Settings","","0","2147483647","Number of consecutive keepalive retransmits that can be lost before a connection is considered dead. 0 means use the system default.",false,nil},
 {"tcp_keepalives_idle","integer","user","0","Time between issuing TCP keepalives.","TCP Settings","s","0","2147483647","0 means use the system default.",false,nil},
 {"tcp_keepalives_interval","integer","user","0","Time between TCP keepalive retransmits.","TCP Settings","s","0","2147483647","0 means use the system default.",false,nil},
 {"tcp_user_timeout","integer","user","0","TCP user timeout.","TCP Settings","ms","0","2147483647","0 means use the system default.",false,nil},
 {"unix_socket_directories","string","postmaster","/tmp","Sets the directories where Unix-domain sockets will be created.","Connection Settings","","","","",true,nil},
 {"unix_socket_group","string","postmaster","","Sets the owning group of the Unix-domain socket.","Connection Settings","","","","The owning user of the socket is always the user that starts the server. An empty string means use the user's default group.",false,nil},
 {"unix_socket_permissions","integer","postmaster","511","Sets the access permissions of the Unix-domain socket.","Connection Settings","","0","511","Unix-domain sockets use the usual Unix file system permission set. The parameter value is expected to be a numeric mode specification in the form accepted by the chmod and umask system calls. (To use the customary octal format the number must start with a 0 (zero).)",false,nil},
}

var connectionAnchors = []string{"ssl_ciphers_high", "ssl_ciphers_medium", "ssl_ciphers_not_anull", "ssl_ciphers_plus_3des"}

func connectionCases(t *testing.T, roles []string, body func(*physicalCase)) {
 t.Helper()
 if len(roles) != 0 { fixture.RoleReferenceCleanup(t, roles) }
 p := fixture.NewScenario(t)
 for _, backend := range p.Backends() {
  t.Run(backend.Name, func(t *testing.T) {
   conn, err := backend.DB.Conn(p.Ctx)
   if err != nil { t.Fatal(err) }
   defer conn.Close()
   body(&physicalCase{t, p, conn, backend.PG, backend.Recorder})
  })
 }
}

func connectionNames() string {
 names := make([]string, len(connectionSettings))
 for i, tc := range connectionSettings { names[i] = physicalLiteral(tc.name) }
 return strings.Join(names, ",")
}

func connectionInitial(tc connectionSetting, pg bool) string {
 switch tc.name {
 case "authentication_timeout": return "1min"
 case "unix_socket_permissions": return "0777"
 }
 if pg {
  // Initialized build/socket values of the pinned PostgreSQL 18.6 fixture.
  switch tc.name {
  case "listen_addresses": return "*"
  case "unix_socket_directories": return "/var/run/postgresql"
  case "krb_server_keyfile": return "FILE:/etc/postgresql-common/krb5.keytab"
  case "tcp_keepalives_count": return "9"
  case "tcp_keepalives_idle": return "7200"
  case "tcp_keepalives_interval": return "75"
  }
 }
 return tc.boot
}

func connectionRaw(tc connectionSetting, display string) string {
 if tc.name == "authentication_timeout" && display == "1min" { return "60" }
 if tc.name == "client_connection_check_interval" {
  switch display { case "1500ms": return "1500"; case "1s": return "1000"; case "1min": return "60000" }
 }
 return display
}

func connectionContext(tc connectionSetting) fixture.Diagnostic {
 if tc.context == "postmaster" { return physicalRestart(tc.name) }
 return physicalDiagnostic("55P02", fmt.Sprintf("parameter %q cannot be changed now", tc.name))
}

func (s *physicalCase) connectionRead(tc connectionSetting, display string, prepared bool) {
 s.t.Helper()
 for _, query := range []string{"SHOW " + tc.name, "SELECT current_setting(" + physicalLiteral(tc.name) + ")"} {
  column := tc.name
  if strings.HasPrefix(query, "SELECT") { column = "current_setting" }
  step := fixture.Select(query, []string{column}, []string{"TEXT"}, [][]any{{display}})
  step.Prepared = prepared
  step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, s.pg, column == "current_setting") }
  s.run(step)
 }
 step := fixture.Select("SELECT setting FROM pg_settings WHERE name="+physicalLiteral(tc.name), []string{"setting"}, []string{"TEXT"}, [][]any{{connectionRaw(tc, display)}})
 step.Prepared = prepared
 s.run(step)
}

func connectionNullable(s string) any { if s == "" { return nil }; return s }

func connectionEnumArray(values []string) any {
 if values == nil { return nil }
 escaped := make([]string, len(values))
 for i, value := range values {
  if value == "" { escaped[i] = `""` } else { escaped[i] = value }
 }
 return "{" + strings.Join(escaped, ",") + "}"
}

func (s *physicalCase) connectionMetadata(tc connectionSetting, display, source string, prepared bool) {
 s.t.Helper()
 reset := tc.boot
 var file, line any
 if s.pg {
  switch tc.name {
  case "listen_addresses":
   reset, source = "*", "configuration file"
   file, line = "/var/lib/postgresql/18/docker/postgresql.conf", int64(60)
  case "max_connections":
   source = "configuration file"
   file, line = "/var/lib/postgresql/18/docker/postgresql.conf", int64(65)
  case "unix_socket_directories": reset = "/var/run/postgresql"
  case "krb_server_keyfile": reset = "FILE:/etc/postgresql-common/krb5.keytab"
  }
 }
 query := "SELECT " + physicalColumnsSQL + " FROM pg_settings WHERE name=" + physicalLiteral(tc.name)
 boot := tc.boot
 if s.pg && tc.name == "krb_server_keyfile" { boot = "FILE:/etc/postgresql-common/krb5.keytab" }
 if s.pg && tc.name == "unix_socket_directories" { boot = "/var/run/postgresql" }
 want := []any{tc.name,connectionRaw(tc,display),connectionNullable(tc.unit),"Connections and Authentication / "+tc.category,tc.description,connectionNullable(tc.extra),tc.context,tc.kind,source,connectionNullable(tc.min),connectionNullable(tc.max),connectionEnumArray(tc.enums),boot,reset,file,line,false}
 step := fixture.Select(query,physicalColumns,physicalTypes,[][]any{want})
 step.Prepared = prepared
 step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { statisticsSettingsCheckMetadata(t, rows, s.pg, physicalColumns, physicalTypes) }
 s.run(step)
}

func (s *physicalCase) connectionVisible(allowed, prepared bool) {
 s.t.Helper()
 var want [][]any
 for _, tc := range connectionSettings { if allowed || !tc.restricted { want = append(want, []any{tc.name}) } }
 step := fixture.Select("SELECT name FROM pg_settings WHERE name IN ("+connectionNames()+") ORDER BY name",[]string{"name"},[]string{"TEXT"},want)
 step.Prepared = prepared
 s.run(step)
 var rows *sql.Rows
 var err error
 if prepared {
  stmt, e := s.conn.PrepareContext(s.p.Ctx,"SHOW ALL"); if e != nil { s.t.Fatal(e) }; defer stmt.Close()
  rows, err = stmt.QueryContext(s.p.Ctx)
 } else { rows, err = s.conn.QueryContext(s.p.Ctx,"SHOW ALL") }
 if err != nil { s.t.Fatal(err) }; defer rows.Close()
 s.connectionCheckAll(rows, allowed)
}

func (s *physicalCase) connectionCheckAll(rows *sql.Rows, allowed bool) {
 columns, err := rows.Columns()
 if err != nil || !reflect.DeepEqual(columns, []string{"name","setting","description"}) { s.t.Fatal(columns,err) }
 types, err := rows.ColumnTypes(); if err != nil { s.t.Fatal(err) }
 for _, typ := range types { if typ.DatabaseTypeName() != "TEXT" { s.t.Fatal(typ) } }
 seen := map[string]bool{}
 for rows.Next() {
  var name,value,description string
  if err := rows.Scan(&name,&value,&description); err != nil { s.t.Fatal(err) }
  for _, anchor := range connectionAnchors { if name == anchor { s.t.Fatal("explanatory anchor published",name) } }
  for _, tc := range connectionSettings { if name == tc.name {
   if (!allowed && tc.restricted) || seen[name] || description != tc.description || value != connectionInitial(tc,s.pg) { s.t.Fatal("SHOW ALL",name,value,description,allowed) }
   seen[name] = true
  } }
 }
 count := 28; if allowed { count = 38 }
 if rows.Err() != nil || len(seen) != count { s.t.Fatal("connection SHOW ALL",len(seen),count,rows.Err()) }
}

func TestReferenceConnectionSettingsInventory(t *testing.T) {
 connectionCases(t,nil,func(s *physicalCase) {
  for _, tc := range connectionSettings {
   for _, prepared := range []bool{false,true} {
    display := connectionInitial(tc,s.pg)
    s.connectionRead(tc,display,prepared)
    s.connectionMetadata(tc,display,"default",prepared)
    for _, query := range []string{"SHOW "+tc.name,"SELECT current_setting("+physicalLiteral(tc.name)+")"} {
     count := int64(1); if strings.HasPrefix(query,"SHOW") { count = 0 }
     step := fixture.Exec(query,count); step.Prepared = prepared; s.run(step)
    }
    step := fixture.Select("SELECT current_setting($1)",[]string{"current_setting"},[]string{"TEXT"},[][]any{{display}},strings.ToUpper(tc.name)); step.Prepared = prepared; s.run(step)
   }
  }
  for _, prepared := range []bool{false,true} {
   s.connectionVisible(true,prepared)
   step := fixture.Select("SELECT current_setting(NULL)",[]string{"current_setting"},[]string{"TEXT"},[][]any{{nil}})
   step.Prepared = prepared
   step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t,rows,s.pg,true) }
   s.run(step)
  }
 })
}
