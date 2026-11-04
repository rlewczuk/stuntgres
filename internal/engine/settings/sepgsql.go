package settings

// SepgsqlDefinitions are activated only by immutable Driver registration.
func SepgsqlDefinitions(permissive bool) []Definition {
	definitions := []Definition{
		{Name: "sepgsql.permissive", Type: Boolean, Access: Sighup, BootDefault: "off", Description: "Switches SELinux checks to permissive mode.", Category: "Customized Options", FixedDefault: true},
		{Name: "sepgsql.debug_audit", Type: Boolean, Access: Session, BootDefault: "off", Description: "Prints SELinux audit logs regardless of policy.", Category: "Customized Options"},
	}
	if permissive {
		definitions[0].NativeStartup = &StartupDefault{Value: "on", Source: "configuration file"}
	}
	for i := range definitions {
		d := &definitions[i]
		value, err := d.Parse(d.BootDefault)
		if err != nil {
			panic(err)
		}
		d.bootValue = value
		if d.NativeStartup != nil {
			value, err = d.Parse(d.NativeStartup.Value)
			if err != nil {
				panic(err)
			}
			d.startupValue = value
		}
	}
	return definitions
}
