package settings

import (
 "reflect"
 "testing"
)

func TestConnectionSettingsDefinitions(t *testing.T) {
 registry := NewRegistry(nil,false)
 restricted := 0
 // Independent REL_18_6 descriptors, with explicitly simulated build labels.
 for _, tc := range []struct {
  name,boot,description,category,extra string
  kind Type
  access Access
  unit Unit
  min,max int32
  restricted bool
  enums []string
 }{
 {"authentication_timeout","1min","Sets the maximum allowed time to complete client authentication.","Connections and Authentication / Authentication","",Integer,Sighup,Seconds,1,600,false,nil},
 {"bonjour","off","Enables advertising the server via Bonjour.","Connections and Authentication / Connection Settings","",Boolean,StartupOnly,UnitNone,0,0,false,nil},
 {"bonjour_name","","Sets the Bonjour service name.","Connections and Authentication / Connection Settings","An empty string means use the computer name.",String,StartupOnly,UnitNone,0,0,false,nil},
 {"client_connection_check_interval","0","Sets the time interval between checks for disconnection while running queries.","Connections and Authentication / TCP Settings","0 disables connection checks.",Integer,Session,Milliseconds,0,2147483647,false,nil},
 {"gss_accept_delegation","off","Sets whether GSSAPI delegation should be accepted from the client.","Connections and Authentication / Authentication","",Boolean,Sighup,UnitNone,0,0,false,nil},
 {"krb_caseins_users","off","Sets whether Kerberos and GSSAPI user names should be treated as case-insensitive.","Connections and Authentication / Authentication","",Boolean,Sighup,UnitNone,0,0,false,nil},
 {"krb_server_keyfile","FILE:/etc/krb5.keytab","Sets the location of the Kerberos server key file.","Connections and Authentication / Authentication","",String,Sighup,UnitNone,0,0,true,nil},
 {"listen_addresses","localhost","Sets the host name or IP address(es) to listen to.","Connections and Authentication / Connection Settings","",String,StartupOnly,UnitNone,0,0,false,nil},
 {"max_connections","100","Sets the maximum number of concurrent connections.","Connections and Authentication / Connection Settings","",Integer,StartupOnly,UnitNone,1,262143,false,nil},
 {"md5_password_warnings","on","Enables deprecation warnings for MD5 passwords.","Connections and Authentication / Authentication","",Boolean,Session,UnitNone,0,0,false,nil},
 {"oauth_validator_libraries","","Lists libraries that may be called to validate OAuth v2 bearer tokens.","Connections and Authentication / Authentication","",String,Sighup,UnitNone,0,0,true,nil},
 {"password_encryption","scram-sha-256","Chooses the algorithm for encrypting passwords.","Connections and Authentication / Authentication","",Enum,Session,UnitNone,0,0,false,[]string{"md5","scram-sha-256"}},
 {"port","5432","Sets the TCP port the server listens on.","Connections and Authentication / Connection Settings","",Integer,StartupOnly,UnitNone,1,65535,false,nil},
 {"reserved_connections","0","Sets the number of connection slots reserved for roles with privileges of pg_use_reserved_connections.","Connections and Authentication / Connection Settings","",Integer,StartupOnly,UnitNone,0,262143,false,nil},
 {"scram_iterations","4096","Sets the iteration count for SCRAM secret generation.","Connections and Authentication / Authentication","",Integer,Session,UnitNone,1,2147483647,false,nil},
 {"ssl","off","Enables SSL connections.","Connections and Authentication / SSL","",Boolean,Sighup,UnitNone,0,0,false,nil},
 {"ssl_ca_file","","Location of the SSL certificate authority file.","Connections and Authentication / SSL","",String,Sighup,UnitNone,0,0,false,nil},
 {"ssl_cert_file","server.crt","Location of the SSL server certificate file.","Connections and Authentication / SSL","",String,Sighup,UnitNone,0,0,false,nil},
 {"ssl_ciphers","HIGH:MEDIUM:+3DES:!aNULL","Sets the list of allowed TLSv1.2 (and lower) ciphers.","Connections and Authentication / SSL","",String,Sighup,UnitNone,0,0,true,nil},
 {"ssl_crl_dir","","Location of the SSL certificate revocation list directory.","Connections and Authentication / SSL","",String,Sighup,UnitNone,0,0,false,nil},
 {"ssl_crl_file","","Location of the SSL certificate revocation list file.","Connections and Authentication / SSL","",String,Sighup,UnitNone,0,0,false,nil},
 {"ssl_dh_params_file","","Location of the SSL DH parameters file.","Connections and Authentication / SSL","An empty string means use compiled-in default parameters.",String,Sighup,UnitNone,0,0,true,nil},
 {"ssl_groups","X25519:prime256v1","Sets the group(s) to use for Diffie-Hellman key exchange.","Connections and Authentication / SSL","Multiple groups can be specified using a colon-separated list.",String,Sighup,UnitNone,0,0,true,nil},
 {"ssl_key_file","server.key","Location of the SSL server private key file.","Connections and Authentication / SSL","",String,Sighup,UnitNone,0,0,false,nil},
 {"ssl_max_protocol_version","","Sets the maximum SSL/TLS protocol version to use.","Connections and Authentication / SSL","",Enum,Sighup,UnitNone,0,0,true,[]string{"","TLSv1","TLSv1.1","TLSv1.2","TLSv1.3"}},
 {"ssl_min_protocol_version","TLSv1.2","Sets the minimum SSL/TLS protocol version to use.","Connections and Authentication / SSL","",Enum,Sighup,UnitNone,0,0,true,[]string{"TLSv1","TLSv1.1","TLSv1.2","TLSv1.3"}},
 {"ssl_passphrase_command","","Command to obtain passphrases for SSL.","Connections and Authentication / SSL","An empty string means use the built-in prompting mechanism.",String,Sighup,UnitNone,0,0,true,nil},
 {"ssl_passphrase_command_supports_reload","off","Controls whether \"ssl_passphrase_command\" is called during server reload.","Connections and Authentication / SSL","",Boolean,Sighup,UnitNone,0,0,false,nil},
 {"ssl_prefer_server_ciphers","on","Give priority to server ciphersuite order.","Connections and Authentication / SSL","",Boolean,Sighup,UnitNone,0,0,false,nil},
 {"ssl_tls13_ciphers","","Sets the list of allowed TLSv1.3 cipher suites.","Connections and Authentication / SSL","An empty string means use the default cipher suites.",String,Sighup,UnitNone,0,0,true,nil},
 {"superuser_reserved_connections","3","Sets the number of connection slots reserved for superusers.","Connections and Authentication / Connection Settings","",Integer,StartupOnly,UnitNone,0,262143,false,nil},
 {"tcp_keepalives_count","0","Maximum number of TCP keepalive retransmits.","Connections and Authentication / TCP Settings","Number of consecutive keepalive retransmits that can be lost before a connection is considered dead. 0 means use the system default.",Integer,Session,UnitNone,0,2147483647,false,nil},
 {"tcp_keepalives_idle","0","Time between issuing TCP keepalives.","Connections and Authentication / TCP Settings","0 means use the system default.",Integer,Session,Seconds,0,2147483647,false,nil},
 {"tcp_keepalives_interval","0","Time between TCP keepalive retransmits.","Connections and Authentication / TCP Settings","0 means use the system default.",Integer,Session,Seconds,0,2147483647,false,nil},
 {"tcp_user_timeout","0","TCP user timeout.","Connections and Authentication / TCP Settings","0 means use the system default.",Integer,Session,Milliseconds,0,2147483647,false,nil},
 {"unix_socket_directories","/tmp","Sets the directories where Unix-domain sockets will be created.","Connections and Authentication / Connection Settings","",String,StartupOnly,UnitNone,0,0,true,nil},
 {"unix_socket_group","","Sets the owning group of the Unix-domain socket.","Connections and Authentication / Connection Settings","The owning user of the socket is always the user that starts the server. An empty string means use the user's default group.",String,StartupOnly,UnitNone,0,0,false,nil},
 {"unix_socket_permissions","0777","Sets the access permissions of the Unix-domain socket.","Connections and Authentication / Connection Settings","Unix-domain sockets use the usual Unix file system permission set. The parameter value is expected to be a numeric mode specification in the form accepted by the chmod and umask system calls. (To use the customary octal format the number must start with a 0 (zero).)",Integer,StartupOnly,UnitNone,0,511,false,nil},
 } {
  t.Run(tc.name,func(t *testing.T) {
   d,err := registry.Lookup(tc.name); if err != nil { t.Fatal(err) }
   if d.Name != tc.name || d.Type != tc.kind || d.Access != tc.access || d.Unit != tc.unit || d.IntegerMin != tc.min || d.IntegerMax != tc.max || d.ReadRestricted != tc.restricted || d.BootDefaultNull || d.NativeStartup != nil || d.Category != tc.category || d.Description != tc.description || d.ExtraDescription != tc.extra || !reflect.DeepEqual(d.EnumValues,tc.enums) {
    t.Fatalf("descriptor %+v want %+v",d,tc)
   }
   if d.BootDefault != tc.boot || d.Format(d.BootValue()) != tc.boot || d.Format(d.DefaultValue()) != tc.boot || !reflect.DeepEqual(d.BootValue(),d.DefaultValue()) { t.Fatal("captured boot/default",d) }
  })
  if tc.restricted { restricted++ }
 }
 if restricted != 10 { t.Fatal("independent restricted inventory",restricted) }
 for _, name := range []string{"ssl_ciphers_high","ssl_ciphers_medium","ssl_ciphers_not_anull","ssl_ciphers_plus_3des"} {
  if _,err := registry.Lookup(name); err == nil { t.Fatal("explanatory anchor registered",name) }
 }
 custom,err := Validate(Definition{Name:"connection.empty",Type:String})
 if err != nil || custom.BootDefaultNull || custom.ReadRestricted || custom.NativeStartup != nil || custom.BootValue().Text != "" { t.Fatal("ordinary empty custom metadata changed",custom,err) }
}

func TestConnectionSettingsFormatting(t *testing.T) {
 registry := NewRegistry(nil,false)
 for _, tc := range []struct{ name,input,show,raw,current string }{
  {"unix_socket_permissions","511","0777","511","0777"},
  {"unix_socket_permissions","0777","0777","511","0777"},
  {"unix_socket_permissions","0","0000","0","0000"},
  {"tcp_keepalives_count","7","7","7","7"},
  {"tcp_keepalives_idle","1min","60","60","60"},
  {"tcp_keepalives_interval","60s","60","60","60"},
  {"tcp_user_timeout","1s","1000","1000","1000"},
  {"client_connection_check_interval","1000ms","1s","1000","1000"},
  {"authentication_timeout","60s","1min","60","60"},
  {"statement_timeout","60000ms","1min","60000","60000"},
  {"port","5432","5432","5432","5432"},
 } {
  t.Run(tc.name+"/"+tc.input,func(t *testing.T) {
   d,err := registry.Lookup(tc.name); if err != nil { t.Fatal(err) }
   v,err := d.Parse(tc.input); if err != nil { t.Fatal(err) }
   if d.Format(v) != tc.show || d.Raw(v) != tc.raw { t.Fatal("SHOW versus numeric metadata",d.Format(v),d.Raw(v),tc) }
   // Keep test-first compilation independent of the new method so missing
   // descriptors/behavior, rather than an undefined symbol, provide useful red.
   formatter,ok := any(d).(interface{ CurrentRaw(Value) string })
   if !ok { t.Fatal("Definition lacks distinct current catalog presentation") }
   if got := formatter.CurrentRaw(v); got != tc.current { t.Fatal("current pg_settings presentation",got,tc.current) }
  })
 }
}
