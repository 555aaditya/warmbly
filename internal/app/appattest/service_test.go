package appattest

import (
	"testing"
)

// The iOS app builds the same bytes; changing them breaks every shipped install.
func TestClientDataIsTheWireContract(t *testing.T) {
	if got := string(ClientData(ActionLogin, "abc")); got != "warmbly-app-attest/v1\nlogin\nabc" {
		t.Fatalf("client data = %q", got)
	}
}

func TestOffWithoutAppIDs(t *testing.T) {
	s := New(nil, nil, Config{})
	if s.Enabled() {
		t.Fatal("enabled with nothing configured")
	}
	if New(nil, nil, Config{AppIDs: []string{"TEAM.com.example"}}).Enabled() {
		t.Fatal("enabled without storage")
	}
}
