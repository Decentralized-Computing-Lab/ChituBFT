package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

func GenKeyPair() (pubKey, priKey []byte, e error) {
	pubKey, priKey, e = ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return nil, nil, e
	}
	return
}

func Sign(priKey, message []byte) []byte {
	return ed25519.Sign(priKey, message)
}

func Verify(pubKey, message, sig []byte) bool {
	return ed25519.Verify(pubKey, message, sig)
}

// GenerateEcdsaKey generates a pair of private and public keys and stores them in the given folder
func GenerateEcdsaKey() ([]byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	pkcs8Encoded, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	pemEncoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Encoded})

	return pemEncoded, err
}

// LoadKey loads the private key in the given path
func LoadKey(pemEncoded []byte) *ecdsa.PrivateKey {

	pemBlock, _ := pem.Decode(pemEncoded)
	key, _ := x509.ParsePKCS8PrivateKey(pemBlock.Bytes)
	caKey := key.(*ecdsa.PrivateKey)
	return caKey
}

// SignECDSA signs the digest of a message
func SignECDSA(k *ecdsa.PrivateKey, digest []byte) ([]byte, error) {
	r, s, err := ecdsa.Sign(rand.Reader, k, digest)
	if err != nil {
		return nil, err
	}
	s, err = ToLowS(&k.PublicKey, s)
	if err != nil {
		return nil, err
	}
	return MarshalECDSASignature(r, s)
}

// VerifyECDSA verifies the digest of a message
func VerifyECDSA(k *ecdsa.PublicKey, digest []byte, signature []byte) (bool, error) {
	r, s, err := UnmarshalECDSASignature(signature)
	if err != nil {
		return false, fmt.Errorf("Failed unmashalling signature [%s]", err)
	}
	lowS, err := IsLowS(k, s)
	if err != nil {
		return false, err
	}
	if !lowS {
		return false, fmt.Errorf("invalid S. Must be smaller than half the order [%s][%s]", s, GetCurveHalfOrdersAt(k.Curve))
	}
	return ecdsa.Verify(k, digest, r, s), nil
}
