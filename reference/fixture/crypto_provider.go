package fixture

var legacyCryptoProvider bool

// WithLegacyCryptoProvider activates OpenSSL's default and legacy providers on
// the package's single test oracle only. It does not configure the native engine.
// Call it through Main before startup; other reference packages remain default-off.
func WithLegacyCryptoProvider() StartupOption {
	return func() { legacyCryptoProvider = true }
}
