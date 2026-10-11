package appattest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// Vectors produced by a real device, from github.com/bas-d/appattest
// (BSD-3-Clause, Copyright (c) 2019, Duo Labs).
type deviceVectors struct {
	AppID                 string `json:"app_id"`
	KeyID                 string `json:"key_id"`
	Attestation           string `json:"attestation"`
	AttestationClientData string `json:"attestation_client_data"`
	AttestedAt            string `json:"attested_at"`
	PublicKey             string `json:"public_key"`
	Assertion             string `json:"assertion"`
	AssertionClientData   string `json:"assertion_client_data"`
}

func loadVectors(t *testing.T) (deviceVectors, []byte, []byte, []byte, []byte, time.Time) {
	t.Helper()
	raw, err := os.ReadFile("testdata/device_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v deviceVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	keyID, err := base64.StdEncoding.DecodeString(v.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	att, err := base64.RawURLEncoding.DecodeString(v.Attestation)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := hex.DecodeString(v.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	as, err := base64.RawURLEncoding.DecodeString(v.Assertion)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, v.AttestedAt)
	if err != nil {
		t.Fatal(err)
	}
	return v, keyID, att, pub, as, at
}

func TestVerifyAttestation(t *testing.T) {
	v, keyID, att, pub, _, at := loadVectors(t)
	cdh := sha256.Sum256([]byte(v.AttestationClientData))
	dev := Policy{AppIDs: []string{"OTHERTEAM.com.example", v.AppID}, AllowDevelopment: true}

	key, err := VerifyAttestation(att, keyID, cdh[:], dev, at)
	if err != nil {
		t.Fatalf("device attestation refused: %v", err)
	}
	if hex.EncodeToString(key.PublicKey) != hex.EncodeToString(pub) {
		t.Fatalf("public key = %x", key.PublicKey)
	}
	if key.AppID != v.AppID || key.Environment != Development {
		t.Fatalf("app id %q, environment %q", key.AppID, key.Environment)
	}

	otherHash := sha256.Sum256([]byte("another-challenge"))
	otherKey := append([]byte{}, keyID...)
	otherKey[0] ^= 1
	cases := map[string]struct {
		keyID, cdh []byte
		policy     Policy
		now        time.Time
	}{
		"development refused":   {keyID, cdh[:], Policy{AppIDs: []string{v.AppID}}, at},
		"another app":           {keyID, cdh[:], Policy{AppIDs: []string{"OTHERTEAM.com.example"}, AllowDevelopment: true}, at},
		"another challenge":     {keyID, otherHash[:], dev, at},
		"another key id":        {otherKey, cdh[:], dev, at},
		"expired credential":    {keyID, cdh[:], dev, at.AddDate(0, 1, 0)},
		"no accepted app ids":   {keyID, cdh[:], Policy{AllowDevelopment: true}, at},
		"truncated attestation": {keyID, cdh[:], dev, at},
	}
	for name, tc := range cases {
		object := att
		if name == "truncated attestation" {
			object = att[:len(att)/2]
		}
		if _, err := VerifyAttestation(object, tc.keyID, tc.cdh, tc.policy, tc.now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestVerifyAssertion(t *testing.T) {
	v, _, _, pub, as, _ := loadVectors(t)
	data := []byte(v.AssertionClientData)

	counter, err := VerifyAssertion(as, data, pub, v.AppID, 0, true)
	if err != nil {
		t.Fatalf("device assertion refused: %v", err)
	}
	if counter == 0 {
		t.Fatal("counter did not advance")
	}

	if _, err := VerifyAssertion(as, data, pub, v.AppID, counter, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("replayed counter: err = %v", err)
	}
	if _, err := VerifyAssertion(as, []byte(`{"challenge":"other"}`), pub, v.AppID, 0, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("other client data: err = %v", err)
	}
	if _, err := VerifyAssertion(as, data, pub, "OTHERTEAM.com.example", 0, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("other app id: err = %v", err)
	}
	tampered := append([]byte{}, as...)
	tampered[len(tampered)-1] ^= 1
	if _, err := VerifyAssertion(tampered, data, pub, v.AppID, 0, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("tampered assertion: err = %v", err)
	}
}

func TestValidationCategory(t *testing.T) {
	ad := &authenticatorData{extensions: map[string]any{}}
	for category, want := range map[uint64]bool{2: true, 3: false, 4: true, 5: false, 10: false, 0: false} {
		ad.extensions[extValidationCategory] = category
		if got := ad.checkCategory(false) == nil; got != want {
			t.Errorf("category %d accepted = %v", category, got)
		}
	}
	ad.extensions[extValidationCategory] = uint64(3)
	if err := ad.checkCategory(true); err != nil {
		t.Errorf("development build refused with development allowed: %v", err)
	}
	delete(ad.extensions, extValidationCategory)
	if err := ad.checkCategory(false); err != nil {
		t.Errorf("no category reported: %v", err)
	}
}
