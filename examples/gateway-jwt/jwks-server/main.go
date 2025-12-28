package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9000", "listen address")
	issuer := flag.String("issuer", "example-issuer", "JWT issuer")
	audience := flag.String("audience", "example-audience", "JWT audience")
	tenant := flag.String("tenant", "t1", "tenant claim")
	service := flag.String("service", "svc", "service claim")
	version := flag.String("version", "blue", "version claim")
	instance := flag.String("instance", "i1", "instance claim")
	flag.Parse()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}

	kid := keyID(&key.PublicKey)
	jwksDoc, err := makeJWKS(&key.PublicKey, kid)
	if err != nil {
		log.Fatal(err)
	}

	tokenStr, err := makeToken(key, kid, *issuer, *audience, *tenant, *service, *version, *instance)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("JWKS listening on http://%s/jwks.json", *addr)
	log.Printf("Use this token (valid ~1h):")
	fmt.Printf("JWT=%s\n", shellEscape(tokenStr))

	mux := http.NewServeMux()
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(jwksDoc)
	})

	log.Fatal(http.ListenAndServe(*addr, mux))
}

func makeToken(priv *rsa.PrivateKey, kid, iss, aud, tenant, service, version, instance string) (string, error) {
	claims := jwt.MapClaims{
		"iss":      iss,
		"aud":      aud,
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(1 * time.Hour).Unix(),
		"tenant":   tenant,
		"service":  service,
		"version":  version,
		"instance": instance,
	}

	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = kid
	return t.SignedString(priv)
}

func makeJWKS(pub *rsa.PublicKey, kid string) ([]byte, error) {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())

	// Standard exponent for RSA keys is 65537.
	eBig := big.NewInt(int64(pub.E))
	e := base64.RawURLEncoding.EncodeToString(eBig.Bytes())

	doc := jwks{Keys: []jwk{{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   n,
		E:   e,
	}}}
	return json.MarshalIndent(doc, "", "  ")
}

func keyID(pub *rsa.PublicKey) string {
	// Stable-ish kid derived from public key material.
	h := sha256.New()
	h.Write(pub.N.Bytes())
	h.Write([]byte{0})
	h.Write(big.NewInt(int64(pub.E)).Bytes())
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

func shellEscape(s string) string {
	// Minimal helper so users can `eval` safely for typical JWTs.
	// JWTs are base64url so should not contain quotes, but keep this robust.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}
