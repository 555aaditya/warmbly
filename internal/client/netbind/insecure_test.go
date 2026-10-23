package netbind

import "testing"

func TestInsecureTLSAppliesOnlyInDevelopment(t *testing.T) {
	for _, tc := range []struct {
		set    bool
		appEnv string
		want   bool
	}{
		{true, "", true},
		{true, "dev", true},
		{true, "DEV", true},
		{true, "prod", false},
		{true, "staging", false},
		{false, "dev", false},
	} {
		if got := insecureTLSApplies(tc.set, tc.appEnv); got != tc.want {
			t.Errorf("insecureTLSApplies(%v, %q) = %v, want %v", tc.set, tc.appEnv, got, tc.want)
		}
	}
}
