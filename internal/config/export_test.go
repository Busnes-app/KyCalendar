package config

// OpenMultiTenantGateForTest lets LoadFromEnv return a complete multi-tenant configuration.
func OpenMultiTenantGateForTest() func() {
	multiTenantStartup = true
	return func() { multiTenantStartup = false }
}
