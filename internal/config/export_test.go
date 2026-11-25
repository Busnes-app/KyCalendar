package config

// OpenMultiTenantGateForTest lets LoadFromEnv return a complete multi-tenant configuration.
func OpenMultiTenantGateForTest() func() {
	multiTenantStartup = true
	return func() { multiTenantStartup = false }
}

// TestIssuer is testIssuer, which only the end-to-end test build calls.
var TestIssuer = testIssuer
