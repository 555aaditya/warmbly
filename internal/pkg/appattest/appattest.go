// Package appattest verifies Apple App Attest attestations and assertions:
// proof that a request comes from a genuine, unmodified build of the native app
// on a real Apple device. It holds no state; callers store keys and counters.
package appattest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
)

//go:embed apple_app_attestation_root_ca.pem
var appleRootPEM []byte

// Environment is the App Attest environment a key was created in.
type Environment string

const (
	Production  Environment = "production"
	Development Environment = "development"
)

// ErrInvalid wraps every verification failure; the wrapped text is for logs only.
var ErrInvalid = errors.New("appattest: invalid")

var (
	aaguidProduction  = append([]byte("appattest"), make([]byte, 7)...)
	aaguidDevelopment = []byte("appattestdevelop")
	nonceExtensionOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 2}
)

const (
	flagAttestedData = 0x40
	flagExtensions   = 0x80

	extValidationCategory = "apple_validation_category_01"
	categoryTestFlight    = 2
	categoryDevelopment   = 3
	categoryAppStore      = 4
)

// Policy is what a server accepts.
type Policy struct {
	// AppIDs are the accepted "<team id>.<bundle id>" values.
	AppIDs []string
	// AllowDevelopment accepts keys from development-signed builds.
	AllowDevelopment bool
	// Roots overrides Apple's root; nil means Apple's. Tests only.
	Roots *x509.CertPool
}

// Key is an attested key, ready to store.
type Key struct {
	ID          []byte
	PublicKey   []byte // uncompressed P-256 point
	AppID       string
	Environment Environment
	Receipt     []byte
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func appleRoots() (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(appleRootPEM) {
		return nil, errors.New("appattest: embedded root does not parse")
	}
	return pool, nil
}

type attestationObject struct {
	Fmt     string `cbor:"fmt"`
	AttStmt struct {
		X5C     [][]byte `cbor:"x5c"`
		Receipt []byte   `cbor:"receipt"`
	} `cbor:"attStmt"`
	AuthData []byte `cbor:"authData"`
}

type authenticatorData struct {
	rpIDHash     []byte
	flags        byte
	counter      uint32
	aaguid       []byte
	credentialID []byte
	extensions   map[string]any
}

// parseAuthenticatorData reads the fields; assertions set the attested-data
// flag without carrying the data, so only attestations parse it.
func parseAuthenticatorData(raw []byte, attested bool) (*authenticatorData, error) {
	if len(raw) < 37 {
		return nil, invalid("authenticator data is %d bytes", len(raw))
	}
	ad := &authenticatorData{rpIDHash: raw[:32], flags: raw[32], counter: binary.BigEndian.Uint32(raw[33:37])}
	rest := raw[37:]
	if attested {
		if ad.flags&flagAttestedData == 0 {
			return nil, invalid("attested credential data is missing")
		}
		if len(rest) < 18 {
			return nil, invalid("attested credential data is truncated")
		}
		ad.aaguid = rest[:16]
		n := int(binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]
		if len(rest) < n {
			return nil, invalid("credential id is truncated")
		}
		ad.credentialID = rest[:n]
		var coseKey cbor.RawMessage
		var err error
		if rest, err = cbor.UnmarshalFirst(rest[n:], &coseKey); err != nil {
			return nil, invalid("credential public key: %v", err)
		}
	}
	if ad.flags&flagExtensions != 0 {
		var err error
		if rest, err = cbor.UnmarshalFirst(rest, &ad.extensions); err != nil {
			return nil, invalid("extensions: %v", err)
		}
	}
	if len(rest) != 0 {
		return nil, invalid("%d trailing bytes in authenticator data", len(rest))
	}
	return ad, nil
}

// checkCategory refuses a launch category the policy does not cover. Devices
// that report none (older systems) are judged on the rest of the checks.
func (ad *authenticatorData) checkCategory(allowDevelopment bool) error {
	raw, ok := ad.extensions[extValidationCategory]
	if !ok {
		return nil
	}
	var category uint64
	switch v := raw.(type) {
	case uint64:
		category = v
	case int64:
		if v < 0 {
			return invalid("validation category %d", v)
		}
		category = uint64(v)
	default:
		return invalid("validation category has type %T", raw)
	}
	switch {
	case category == categoryAppStore, category == categoryTestFlight:
		return nil
	case category == categoryDevelopment && allowDevelopment:
		return nil
	}
	return invalid("validation category %d is not accepted", category)
}

func matchAppID(rpIDHash []byte, appIDs []string) (string, bool) {
	for _, id := range appIDs {
		sum := sha256.Sum256([]byte(id))
		if subtle.ConstantTimeCompare(sum[:], rpIDHash) == 1 {
			return id, true
		}
	}
	return "", false
}

// VerifyAttestation checks a key attestation made over clientDataHash and
// returns the key to store. keyID is the raw 32-byte key identifier.
func VerifyAttestation(object, keyID, clientDataHash []byte, policy Policy, now time.Time) (*Key, error) {
	var att attestationObject
	if err := cbor.Unmarshal(object, &att); err != nil {
		return nil, invalid("attestation object: %v", err)
	}
	if att.Fmt != "apple-appattest" {
		return nil, invalid("format %q", att.Fmt)
	}
	if len(att.AttStmt.X5C) < 2 {
		return nil, invalid("certificate chain has %d entries", len(att.AttStmt.X5C))
	}

	certs := make([]*x509.Certificate, 0, len(att.AttStmt.X5C))
	for _, der := range att.AttStmt.X5C {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, invalid("certificate: %v", err)
		}
		certs = append(certs, cert)
	}
	roots := policy.Roots
	if roots == nil {
		var err error
		if roots, err = appleRoots(); err != nil {
			return nil, err
		}
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	credCert := certs[0]
	if _, err := credCert.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, invalid("certificate chain: %v", err)
	}

	nonce := sha256.Sum256(append(append([]byte{}, att.AuthData...), clientDataHash...))
	var certNonce []byte
	for _, ext := range credCert.Extensions {
		if !ext.Id.Equal(nonceExtensionOID) {
			continue
		}
		var wrapped struct {
			Nonce []byte `asn1:"tag:1,explicit"`
		}
		if _, err := asn1.Unmarshal(ext.Value, &wrapped); err != nil {
			return nil, invalid("nonce extension: %v", err)
		}
		certNonce = wrapped.Nonce
	}
	if subtle.ConstantTimeCompare(certNonce, nonce[:]) != 1 {
		return nil, invalid("nonce does not match the challenge")
	}

	pub, ok := credCert.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, invalid("credential key is not P-256")
	}
	ecdhKey, err := pub.ECDH()
	if err != nil {
		return nil, invalid("credential key: %v", err)
	}
	point := ecdhKey.Bytes()
	pointHash := sha256.Sum256(point)
	if subtle.ConstantTimeCompare(pointHash[:], keyID) != 1 {
		return nil, invalid("key id does not match the credential key")
	}

	ad, err := parseAuthenticatorData(att.AuthData, true)
	if err != nil {
		return nil, err
	}
	appID, ok := matchAppID(ad.rpIDHash, policy.AppIDs)
	if !ok {
		return nil, invalid("app id is not accepted")
	}
	if ad.counter != 0 {
		return nil, invalid("counter is %d", ad.counter)
	}
	var env Environment
	switch {
	case bytes.Equal(ad.aaguid, aaguidProduction):
		env = Production
	case bytes.Equal(ad.aaguid, aaguidDevelopment) && policy.AllowDevelopment:
		env = Development
	default:
		return nil, invalid("environment is not accepted")
	}
	if !bytes.Equal(ad.credentialID, keyID) {
		return nil, invalid("credential id does not match the key id")
	}
	if err := ad.checkCategory(policy.AllowDevelopment); err != nil {
		return nil, err
	}

	return &Key{
		ID:          append([]byte{}, keyID...),
		PublicKey:   point,
		AppID:       appID,
		Environment: env,
		Receipt:     att.AttStmt.Receipt,
	}, nil
}

type assertionObject struct {
	Signature         []byte `cbor:"signature"`
	AuthenticatorData []byte `cbor:"authenticatorData"`
}

// VerifyAssertion checks an assertion over clientData made with a stored key
// and returns its counter, which must be stored for the next one.
func VerifyAssertion(object, clientData, publicKey []byte, appID string, prevCounter uint32, allowDevelopment bool) (uint32, error) {
	var as assertionObject
	if err := cbor.Unmarshal(object, &as); err != nil {
		return 0, invalid("assertion object: %v", err)
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), publicKey)
	if err != nil {
		return 0, invalid("stored key: %v", err)
	}

	clientDataHash := sha256.Sum256(clientData)
	nonce := sha256.Sum256(append(append([]byte{}, as.AuthenticatorData...), clientDataHash[:]...))
	// The device signs nonce as the message, so ES256 hashes it once more.
	digest := sha256.Sum256(nonce[:])
	if !ecdsa.VerifyASN1(pub, digest[:], as.Signature) {
		return 0, invalid("signature does not verify")
	}

	ad, err := parseAuthenticatorData(as.AuthenticatorData, false)
	if err != nil {
		return 0, err
	}
	if _, ok := matchAppID(ad.rpIDHash, []string{appID}); !ok {
		return 0, invalid("app id does not match the key")
	}
	if ad.counter <= prevCounter {
		return 0, invalid("counter %d did not advance past %d", ad.counter, prevCounter)
	}
	if err := ad.checkCategory(allowDevelopment); err != nil {
		return 0, err
	}
	return ad.counter, nil
}
