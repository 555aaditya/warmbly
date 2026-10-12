package applekey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
)

func p8(t *testing.T, curve elliptic.Curve) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestParseAcceptsEveryPastedForm(t *testing.T) {
	want, text := p8(t, elliptic.P256())
	b64 := base64.StdEncoding.EncodeToString([]byte(text))
	forms := map[string]string{
		"pem":              text,
		"pem with spaces":  "  \n" + text + "\n ",
		"escaped newlines": strings.ReplaceAll(strings.TrimSpace(text), "\n", `\n`),
		"base64":           b64,
		"wrapped base64":   b64[:40] + "\n" + b64[40:] + "\n",
	}
	for name, value := range forms {
		got, err := Parse(value)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("%s: parsed a different key", name)
		}
	}
}

func TestParseRefusesWhatIsNotAnAppleKey(t *testing.T) {
	_, p384 := p8(t, elliptic.P384())
	cases := map[string]string{
		"empty":          "  ",
		"not base64":     "not base64!",
		"base64 not pem": base64.StdEncoding.EncodeToString([]byte("no pem here")),
		"broken pem":     "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----",
		"wrong curve":    p384,
	}
	for name, value := range cases {
		_, err := Parse(value)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if v := strings.TrimSpace(value); v != "" && strings.Contains(err.Error(), v) {
			t.Errorf("%s: error echoes the value", name)
		}
	}
}
