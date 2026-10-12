// Package applekey reads an Apple .p8 signing key (Sign in with Apple, APNs)
// from configuration, in whichever form it was pasted.
package applekey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
)

// Parse accepts the .p8 file's PEM text, the same text with its line breaks
// escaped as \n, or the file base64-encoded. Errors never echo the value.
func Parse(value string) (*ecdsa.PrivateKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("applekey: key is empty")
	}
	var pemBytes []byte
	if strings.Contains(value, "-----BEGIN") {
		pemBytes = []byte(strings.ReplaceAll(value, `\n`, "\n"))
	} else {
		decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value), ""))
		if err != nil {
			return nil, errors.New("applekey: key is neither PEM nor base64-encoded PEM")
		}
		pemBytes = decoded
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("applekey: key is not PEM encoded")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("applekey: key is not a PKCS#8 private key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("applekey: key is not a P-256 key (expected an Apple .p8)")
	}
	return key, nil
}
