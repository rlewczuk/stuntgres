package stuntgres

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/settings"
)

// SepgsqlSimulationConfig selects deterministic query-compatible simulated
// policy, not kernel SELinux enforcement or host isolation. Registration is
// initialization-only. Audit handlers run synchronously outside runtime locks;
// they must not synchronously reenter the same physical connection.
type SepgsqlSimulationConfig = sepgsql.SimulationConfig
type SepgsqlPolicy = sepgsql.Policy
type SepgsqlRule = sepgsql.Rule
type SepgsqlCreateTransition = sepgsql.CreateTransition
type SepgsqlTrustedTransition = sepgsql.TrustedTransition
type SepgsqlTranslation = sepgsql.Translation
type SepgsqlAuditEvent = sepgsql.AuditEvent

// RegisterSepgsqlSimulation validates and copies the complete policy atomically.
// ID and Version are application promises of compatibility across native Restore.
// Activation registers provider selinux; InstallSQL separately installs the five
// pg_catalog C declarations in newly created logical databases.
func (d *Driver) RegisterSepgsqlSimulation(config SepgsqlSimulationConfig) error {
	runtime, err := sepgsql.Compile(config)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return fmt.Errorf("stuntgres: sepgsql registration after first connector")
	}
	if d.sepgsql != nil {
		return fmt.Errorf("stuntgres: duplicate sepgsql simulation registration")
	}
	for _, definition := range d.settings {
		if strings.HasPrefix(definition.Name, "sepgsql.") {
			return fmt.Errorf("stuntgres: setting prefix sepgsql is already registered")
		}
	}
	d.sepgsql = runtime
	d.settings = append(d.settings, settings.SepgsqlDefinitions(runtime.StartupPermissive())...)
	return nil
}

// WithSepgsqlContext copies the connector and asserts a trusted application-
// selected simulated identity. It never authenticates an OS peer, accepts a DSN
// label or derives a subject from a SQL role. Validation occurs at Connect.
func (c *Connector) WithSepgsqlContext(context string) *Connector {
	copy := *c
	copy.sepgsqlContext, copy.explicitSepgsqlContext = context, true
	return &copy
}
