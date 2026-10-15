package cloudlink

import (
	"strings"
	"testing"
)

func TestCloudURLAllowed(t *testing.T) {
	for env, loopback := range map[string]bool{"dev": true, "": true, "prod": false} {
		t.Setenv("APP_ENV", env)
		if !cloudURLAllowed("https://api.warmbly.com") {
			t.Errorf("APP_ENV=%q: refused the hosted API", env)
		}
		if got := cloudURLAllowed("http://localhost:18301"); got != loopback {
			t.Errorf("APP_ENV=%q: loopback over http = %v, want %v", env, got, loopback)
		}
		for _, u := range []string{"http://api.warmbly.com", "https://user:pw@api.warmbly.com", "ftp://x", "https://", "https://a.com?x=1"} {
			if cloudURLAllowed(u) {
				t.Errorf("APP_ENV=%q: accepted %q", env, u)
			}
		}
	}
}

func TestRemoteFailureShowsOnlyCloudVocabulary(t *testing.T) {
	message := "This address already exists in the linked Cloud workspace, but is not enrolled for this local mailbox. Contact Warmbly support to check the association. Do not delete or re-enroll the mailbox."
	x := remoteFailure(409, "/instance/mailboxes", []byte(`{"code":"pool_link_mailbox_conflict","message":"`+message+`"}`))
	if x.Identifier != "pool_link_mailbox_conflict" || x.Message != message {
		t.Errorf("mailbox conflict lost its explanation: got %q %q", x.Identifier, x.Message)
	}
	x = remoteFailure(409, "/codes", []byte(`{"code":"pool_link_denied","message":"The code was denied."}`))
	if x.Identifier != "pool_link_denied" || x.Message != "The code was denied." {
		t.Errorf("pool-link refusal: got %q %q", x.Identifier, x.Message)
	}
	x = remoteFailure(401, "/instance", []byte(`{"code":"unauthorized","message":"<script>x</script> internal detail"}`))
	if x.Identifier != "unauthorized" || strings.Contains(x.Message, "internal detail") {
		t.Errorf("other refusal: got %q %q", x.Identifier, x.Message)
	}
	x = remoteFailure(502, "/instance", []byte(`<html>bad gateway</html>`))
	if x.Identifier != "cloud_link_remote" || strings.Contains(x.Message, "gateway") {
		t.Errorf("non-JSON refusal: got %q %q", x.Identifier, x.Message)
	}
}
