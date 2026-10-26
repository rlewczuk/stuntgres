package session

import (
 "reflect"
 "testing"

 "stuntgres/internal/engine/settings"
)

func TestConnectionSettingsDefaultCapture(t *testing.T) {
 custom,err := settings.Validate(settings.Definition{Name:"connection.empty",Type:settings.String})
 if err != nil { t.Fatal(err) }
 registry := settings.NewRegistry([]settings.Definition{custom},false)
 capture := func(provider settings.SessionDefaultProvider) (*SettingsStore,error) {
  store := &SettingsStore{Defaults:make(map[string]string),Values:make(map[string]string)}
  err := store.Initialize(t.Context(),registry.Definitions(),registry.Order(),provider)
  return store,err
 }
 defaults := physicalDefaultProvider{
 "authentication_timeout":"60",
 "bonjour":"off",
 "bonjour_name":"",
 "client_connection_check_interval":"0",
 "gss_accept_delegation":"off",
 "krb_caseins_users":"off",
 "krb_server_keyfile":"FILE:/etc/krb5.keytab",
 "listen_addresses":"localhost",
 "max_connections":"100",
 "md5_password_warnings":"on",
 "oauth_validator_libraries":"",
 "password_encryption":"scram-sha-256",
 "port":"5432",
 "reserved_connections":"0",
 "scram_iterations":"4096",
 "ssl":"off",
 "ssl_ca_file":"",
 "ssl_cert_file":"server.crt",
 "ssl_ciphers":"HIGH:MEDIUM:+3DES:!aNULL",
 "ssl_crl_dir":"",
 "ssl_crl_file":"",
 "ssl_dh_params_file":"",
 "ssl_groups":"X25519:prime256v1",
 "ssl_key_file":"server.key",
 "ssl_max_protocol_version":"",
 "ssl_min_protocol_version":"TLSv1.2",
 "ssl_passphrase_command":"",
 "ssl_passphrase_command_supports_reload":"off",
 "ssl_prefer_server_ciphers":"on",
 "ssl_tls13_ciphers":"",
 "superuser_reserved_connections":"3",
 "tcp_keepalives_count":"0",
 "tcp_keepalives_idle":"0",
 "tcp_keepalives_interval":"0",
 "tcp_user_timeout":"0",
 "unix_socket_directories":"/tmp",
 "unix_socket_group":"",
 "unix_socket_permissions":"511",
 }
 for _, provider := range []settings.SessionDefaultProvider{nil,physicalDefaultProvider{},defaults} {
  store,err := capture(provider); if err != nil { t.Fatal(err) }
  for name := range defaults {
   d := registry.Definitions()[name]
   want := d.Format(d.DefaultValue())
   if store.Value(name) != want || store.Defaults[name] != want || !reflect.DeepEqual(store.DefaultTyped[name],d.DefaultValue()) {
    t.Fatal("captured typed/default value",name,store.Value(name),store.DefaultTyped[name],want)
   }
   supplied, _ := provider.(physicalDefaultProvider)
   _,present := supplied[name]
   if store.ProviderDefaults[name] != present { t.Fatal("provider provenance",name,store.ProviderDefaults[name],present) }
  }
  value,present := store.Defaults["connection.empty"]
  if !present || value != "" || registry.Definitions()["connection.empty"].BootDefaultNull { t.Fatal("ordinary empty default metadata changed",value,present) }
 }
 for _, pair := range []struct{ name,same,display string }{
  {"unix_socket_permissions","511","0777"},
  {"unix_socket_permissions","0777","0777"},
  {"authentication_timeout","60s","1min"},
  {"authentication_timeout","1min","1min"},
  {"ssl","false","off"},
  {"ssl_min_protocol_version","tlsV1.2","TLSv1.2"},
 } {
  store,err := capture(physicalDefaultProvider{pair.name:pair.same})
  if err != nil || store == nil { t.Fatal("normalized immutable default refused",pair,err) }
  if store.Value(pair.name) != pair.display || !store.ProviderDefaults[pair.name] { t.Fatal("same-value provider display/provenance",pair,store.Value(pair.name)) }
 }
 for _, tc := range []struct{ name,different string }{
 {"authentication_timeout","1"},
 {"bonjour","on"},
 {"bonjour_name","different-simulated-label"},
 {"gss_accept_delegation","on"},
 {"krb_caseins_users","on"},
 {"krb_server_keyfile","different-simulated-label"},
 {"listen_addresses","different-simulated-label"},
 {"max_connections","1"},
 {"oauth_validator_libraries","different-simulated-label"},
 {"port","1"},
 {"reserved_connections","1"},
 {"ssl","on"},
 {"ssl_ca_file","different-simulated-label"},
 {"ssl_cert_file","different-simulated-label"},
 {"ssl_ciphers","different-simulated-label"},
 {"ssl_crl_dir","different-simulated-label"},
 {"ssl_crl_file","different-simulated-label"},
 {"ssl_dh_params_file","different-simulated-label"},
 {"ssl_groups","different-simulated-label"},
 {"ssl_key_file","different-simulated-label"},
 {"ssl_max_protocol_version","TLSv1.3"},
 {"ssl_min_protocol_version","TLSv1.3"},
 {"ssl_passphrase_command","different-simulated-label"},
 {"ssl_passphrase_command_supports_reload","on"},
 {"ssl_prefer_server_ciphers","off"},
 {"ssl_tls13_ciphers","different-simulated-label"},
 {"superuser_reserved_connections","1"},
 {"unix_socket_directories","different-simulated-label"},
 {"unix_socket_group","different-simulated-label"},
 {"unix_socket_permissions","1"},
 } {
  if _,err := capture(physicalDefaultProvider{tc.name:tc.different}); err == nil || err.Error() != "stuntgres: default provider cannot change "+tc.name { t.Fatal("immutable provider changed initial value",tc.name,err) }
 }
 provider := &clientDefaultProvider{physicalDefaultProvider:physicalDefaultProvider{
  "client_connection_check_interval":"1s", "md5_password_warnings":"off", "password_encryption":"md5", "scram_iterations":"8192",
  "tcp_keepalives_count":"7", "tcp_keepalives_idle":"60s", "tcp_keepalives_interval":"1min", "tcp_user_timeout":"1s",
 }}
 want := map[string]string{"client_connection_check_interval":"1s","md5_password_warnings":"off","password_encryption":"md5","scram_iterations":"8192","tcp_keepalives_count":"7","tcp_keepalives_idle":"60","tcp_keepalives_interval":"60","tcp_user_timeout":"1000"}
 old,err := capture(provider); if err != nil { t.Fatal(err) }
 if provider.calls != len(registry.Order()) { t.Fatal("provider capture calls",provider.calls) }
 for name,value := range want {
  if old.Value(name) != value || !old.ProviderDefaults[name] { t.Fatal("mutable default capture",name,old.Value(name),value) }
  old.Values[name] = "overridden"
  old.Typed[name] = settings.Value{Text:"overridden"}
 }
 for name := range want { provider.physicalDefaultProvider[name] = defaults[name] }
 old.ResetAllLocked(registry)
 if provider.calls != len(registry.Order()) { t.Fatal("reset reentered provider",provider.calls) }
 for name,value := range want { if old.Value(name) != value { t.Fatal("old physical default changed",name,old.Value(name),value) } }
 replacement,err := capture(provider); if err != nil { t.Fatal(err) }
 if provider.calls != 2*len(registry.Order()) { t.Fatal("replacement did not recapture",provider.calls) }
 for name,value := range want {
  d := registry.Definitions()[name]
  if replacement.Value(name) != d.Format(d.DefaultValue()) || old.Value(name) != value { t.Fatal("physical capture isolation",name,replacement.Value(name),old.Value(name)) }
 }
}
