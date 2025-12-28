package caddyslb

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(&GatewayJWT{})

	// Caddyfile directive which outputs an HTTP handler.
	httpcaddyfile.RegisterHandlerDirective("gateway_jwt", parseGatewayJWTDirective)
	// Ensure we run before reverse_proxy so vars are available to placeholders.
	httpcaddyfile.RegisterDirectiveOrder("gateway_jwt", httpcaddyfile.Before, "reverse_proxy")
}

// GatewayJWT validates a JWT issued by an internal gateway and maps trusted claims
// into request vars for downstream routing decisions.
//
// This module is designed for "gateway already authenticated" architectures:
// - Only allow trusted sources (CIDRs) and/or mTLS client certificates.
// - Verify JWT signature via JWKS URL with caching.
// - Convert claims (tenant/service/version/instance) into routing keys.
type GatewayJWT struct {
	// JWKSURL is the URL to fetch a JSON Web Key Set (JWKS) from.
	JWKSURL string `json:"jwks_url,omitempty"`

	// Issuer is the expected JWT issuer (iss).
	Issuer string `json:"issuer,omitempty"`

	// Audiences is the list of allowed JWT audiences (aud). If empty, audience is not validated.
	Audiences []string `json:"audiences,omitempty"`

	// AllowedAlgs is the allowlist of accepted JWT algorithms (e.g. RS256, ES256).
	// If empty, defaults to RS256 and ES256.
	AllowedAlgs []string `json:"allowed_algs,omitempty"`

	// Leeway is clock skew tolerance for exp/nbf/iat validation.
	Leeway caddy.Duration `json:"leeway,omitempty"`

	// TokenHeader is the header containing the JWT (default: Authorization).
	TokenHeader string `json:"token_header,omitempty"`

	// TokenPrefix is stripped from TokenHeader value (default: "Bearer ").
	TokenPrefix string `json:"token_prefix,omitempty"`

	// Optional, if true, passes the request through when the token is missing.
	// Invalid tokens are still rejected.
	Optional bool `json:"optional,omitempty"`

	// TrustedSources is an optional allowlist of CIDR ranges for the request source IP.
	TrustedSources []string `json:"trusted_sources,omitempty"`

	// RequireMTLS requires that the request has a verified client certificate.
	RequireMTLS bool `json:"require_mtls,omitempty"`

	// AllowedClientCertSHA256 is an optional allowlist of mTLS client cert fingerprints (SHA-256 hex).
	// If empty and RequireMTLS is true, any client certificate is accepted.
	AllowedClientCertSHA256 []string `json:"allowed_client_cert_sha256,omitempty"`

	// ClaimTenant/Service/Version/Instance specify claim names.
	ClaimTenant   string `json:"claim_tenant,omitempty"`
	ClaimService  string `json:"claim_service,omitempty"`
	ClaimVersion  string `json:"claim_version,omitempty"`
	ClaimInstance string `json:"claim_instance,omitempty"`

	// VersionKeyTemplate is used to build the version routing key from claims.
	// Supported placeholders: {tenant}, {service}, {version}.
	VersionKeyTemplate string `json:"version_key_template,omitempty"`

	// InstanceKeyTemplate is used to build the instance routing key from claims.
	// Supported placeholders: {tenant}, {service}, {version}, {instance}.
	InstanceKeyTemplate string `json:"instance_key_template,omitempty"`

	// KeyPrefix is optionally prepended to computed keys.
	KeyPrefix string `json:"key_prefix,omitempty"`

	// Var names to set.
	VersionKeyVar  string `json:"version_key_var,omitempty"`
	InstanceKeyVar string `json:"instance_key_var,omitempty"`

	TenantVar   string `json:"tenant_var,omitempty"`
	ServiceVar  string `json:"service_var,omitempty"`
	VersionVar  string `json:"version_var,omitempty"`
	InstanceVar string `json:"instance_var,omitempty"`

	// JWKS refresh behavior.
	JWKSRefreshInterval caddy.Duration `json:"jwks_refresh_interval,omitempty"`
	JWKSTimeout         caddy.Duration `json:"jwks_timeout,omitempty"`
	AllowInsecureJWKS   bool           `json:"allow_insecure_jwks,omitempty"`

	logger *zap.Logger

	trustedSourcePrefixes []netip.Prefix
	allowedCertFP         map[[32]byte]struct{}

	jwks *jwksCache

	// for tests
	now func() time.Time
}

func (*GatewayJWT) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.dynamic_gateway_jwt",
		New: func() caddy.Module { return new(GatewayJWT) },
	}
}

func (g *GatewayJWT) Provision(ctx caddy.Context) error {
	g.logger = ctx.Logger()
	if g.now == nil {
		g.now = time.Now
	}

	if g.JWKSURL == "" {
		return fmt.Errorf("jwks_url is required")
	}
	parsedURL, err := url.Parse(g.JWKSURL)
	if err != nil {
		return fmt.Errorf("invalid jwks_url: %v", err)
	}
	if !g.AllowInsecureJWKS && parsedURL.Scheme != "https" {
		return fmt.Errorf("jwks_url must be https unless allow_insecure_jwks is true")
	}

	if g.TokenHeader == "" {
		g.TokenHeader = "Authorization"
	}
	if g.TokenPrefix == "" {
		g.TokenPrefix = "Bearer "
	}

	if len(g.AllowedAlgs) == 0 {
		g.AllowedAlgs = []string{"RS256", "ES256"}
	}

	if g.ClaimTenant == "" {
		g.ClaimTenant = "tenant"
	}
	if g.ClaimService == "" {
		g.ClaimService = "service"
	}
	if g.ClaimVersion == "" {
		g.ClaimVersion = "version"
	}
	if g.ClaimInstance == "" {
		g.ClaimInstance = "instance"
	}

	if g.VersionKeyTemplate == "" {
		g.VersionKeyTemplate = "{tenant}/{service}/{version}"
	}
	if g.InstanceKeyTemplate == "" {
		g.InstanceKeyTemplate = "{tenant}/{service}/{version}/{instance}"
	}

	if g.VersionKeyVar == "" {
		g.VersionKeyVar = "route_version_key"
	}
	if g.InstanceKeyVar == "" {
		g.InstanceKeyVar = "route_instance_key"
	}
	if g.TenantVar == "" {
		g.TenantVar = "route_tenant"
	}
	if g.ServiceVar == "" {
		g.ServiceVar = "route_service"
	}
	if g.VersionVar == "" {
		g.VersionVar = "route_version"
	}
	if g.InstanceVar == "" {
		g.InstanceVar = "route_instance"
	}

	if g.JWKSTimeout == 0 {
		g.JWKSTimeout = caddy.Duration(5 * time.Second)
	}
	if g.JWKSRefreshInterval == 0 {
		g.JWKSRefreshInterval = caddy.Duration(10 * time.Minute)
	}

	// Parse trusted sources.
	if len(g.TrustedSources) > 0 {
		g.trustedSourcePrefixes = make([]netip.Prefix, 0, len(g.TrustedSources))
		for _, cidr := range g.TrustedSources {
			cidr = strings.TrimSpace(cidr)
			if cidr == "" {
				continue
			}
			p, err := netip.ParsePrefix(cidr)
			if err != nil {
				return fmt.Errorf("invalid trusted_sources CIDR %q: %v", cidr, err)
			}
			g.trustedSourcePrefixes = append(g.trustedSourcePrefixes, p)
		}
	}

	// Parse allowed client cert fingerprints.
	if g.RequireMTLS && len(g.AllowedClientCertSHA256) > 0 {
		g.allowedCertFP = make(map[[32]byte]struct{}, len(g.AllowedClientCertSHA256))
		for _, hexFP := range g.AllowedClientCertSHA256 {
			fpBytes, err := hex.DecodeString(strings.TrimSpace(strings.ToLower(hexFP)))
			if err != nil {
				return fmt.Errorf("invalid allowed_client_cert_sha256 %q: %v", hexFP, err)
			}
			if len(fpBytes) != sha256.Size {
				return fmt.Errorf("allowed_client_cert_sha256 must be %d bytes hex, got %d", sha256.Size, len(fpBytes))
			}
			var fp [32]byte
			copy(fp[:], fpBytes)
			g.allowedCertFP[fp] = struct{}{}
		}
	}

	g.jwks = newJWKSCache(jwksCacheConfig{
		URL:            g.JWKSURL,
		Refresh:        time.Duration(g.JWKSRefreshInterval),
		Timeout:        time.Duration(g.JWKSTimeout),
		Logger:         g.logger,
		Now:            g.now,
		AllowInsecure:  g.AllowInsecureJWKS,
		ExpectedIssuer: g.Issuer,
	})

	if err := g.jwks.Refresh(ctx.Context); err != nil {
		return err
	}

	return nil
}

func (g *GatewayJWT) Validate() error {
	// Only basic config validation here; deeper validation in Provision.
	if g.JWKSURL == "" {
		return fmt.Errorf("jwks_url is required")
	}
	return nil
}

func (g *GatewayJWT) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if g.logger == nil {
		g.logger = zap.NewNop()
	}

	// Trusted sources check.
	if len(g.trustedSourcePrefixes) > 0 {
		ip, err := remoteIP(r)
		if err != nil {
			g.logger.Debug("unable to parse remote IP", zap.Error(err))
			return caddyhttp.Error(http.StatusForbidden, err)
		}
		allowed := false
		for _, p := range g.trustedSourcePrefixes {
			if p.Contains(ip) {
				allowed = true
				break
			}
		}
		if !allowed {
			return caddyhttp.Error(http.StatusForbidden, fmt.Errorf("request source not allowed"))
		}
	}

	// mTLS requirement.
	if g.RequireMTLS {
		if !hasVerifiedClientCert(r.TLS) {
			return caddyhttp.Error(http.StatusForbidden, fmt.Errorf("mTLS client certificate required"))
		}
		if len(g.allowedCertFP) > 0 {
			fpOK := false
			for _, cert := range r.TLS.PeerCertificates {
				fp := sha256.Sum256(cert.Raw)
				if _, ok := g.allowedCertFP[fp]; ok {
					fpOK = true
					break
				}
			}
			if !fpOK {
				return caddyhttp.Error(http.StatusForbidden, fmt.Errorf("mTLS client certificate not allowed"))
			}
		}
	}

	tokenStr, tokenMissing := g.extractToken(r)
	if tokenMissing {
		if g.Optional {
			return next.ServeHTTP(w, r)
		}
		return caddyhttp.Error(http.StatusUnauthorized, fmt.Errorf("missing token"))
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(
		tokenStr,
		claims,
		func(t *jwt.Token) (any, error) { return g.jwks.KeyForToken(r.Context(), t) },
		jwt.WithValidMethods(g.AllowedAlgs),
		jwt.WithLeeway(time.Duration(g.Leeway)),
	)
	if err != nil {
		return caddyhttp.Error(http.StatusUnauthorized, err)
	}
	if token == nil || !token.Valid {
		return caddyhttp.Error(http.StatusUnauthorized, fmt.Errorf("invalid token"))
	}

	if g.Issuer != "" {
		iss, _ := claims["iss"].(string)
		if iss != g.Issuer {
			return caddyhttp.Error(http.StatusUnauthorized, fmt.Errorf("invalid issuer"))
		}
	}
	if len(g.Audiences) > 0 {
		if !audienceAllowed(claims["aud"], g.Audiences) {
			return caddyhttp.Error(http.StatusUnauthorized, fmt.Errorf("invalid audience"))
		}
	}

	tenant, err := claimStringRequired(claims, g.ClaimTenant)
	if err != nil {
		return caddyhttp.Error(http.StatusUnauthorized, err)
	}
	service, err := claimStringRequired(claims, g.ClaimService)
	if err != nil {
		return caddyhttp.Error(http.StatusUnauthorized, err)
	}
	version, err := claimStringRequired(claims, g.ClaimVersion)
	if err != nil {
		return caddyhttp.Error(http.StatusUnauthorized, err)
	}
	instance, _ := claimStringOptional(claims, g.ClaimInstance)

	versionKey, err := renderKeyTemplate(g.VersionKeyTemplate, map[string]string{
		"tenant":  tenant,
		"service": service,
		"version": version,
	})
	if err != nil {
		return caddyhttp.Error(http.StatusUnauthorized, err)
	}
	instanceKey := ""
	if instance != "" {
		instanceKey, err = renderKeyTemplate(g.InstanceKeyTemplate, map[string]string{
			"tenant":   tenant,
			"service":  service,
			"version":  version,
			"instance": instance,
		})
		if err != nil {
			return caddyhttp.Error(http.StatusUnauthorized, err)
		}
	}

	if g.KeyPrefix != "" {
		versionKey = g.KeyPrefix + versionKey
		if instanceKey != "" {
			instanceKey = g.KeyPrefix + instanceKey
		}
	}

	// Ensure vars table exists.
	ctx := r.Context()
	if ctx.Value(caddyhttp.VarsCtxKey) == nil {
		ctx = context.WithValue(ctx, caddyhttp.VarsCtxKey, map[string]any{})
		r = r.WithContext(ctx)
	}

	caddyhttp.SetVar(ctx, g.TenantVar, tenant)
	caddyhttp.SetVar(ctx, g.ServiceVar, service)
	caddyhttp.SetVar(ctx, g.VersionVar, version)
	caddyhttp.SetVar(ctx, g.InstanceVar, instance)
	caddyhttp.SetVar(ctx, g.VersionKeyVar, versionKey)
	if instanceKey != "" {
		caddyhttp.SetVar(ctx, g.InstanceKeyVar, instanceKey)
	} else {
		caddyhttp.SetVar(ctx, g.InstanceKeyVar, nil)
	}

	return next.ServeHTTP(w, r)
}

func (g *GatewayJWT) extractToken(r *http.Request) (token string, missing bool) {
	v := strings.TrimSpace(r.Header.Get(g.TokenHeader))
	if v == "" {
		return "", true
	}
	if g.TokenPrefix != "" {
		if !strings.HasPrefix(v, g.TokenPrefix) {
			return "", true
		}
		v = strings.TrimSpace(strings.TrimPrefix(v, g.TokenPrefix))
	}
	if v == "" {
		return "", true
	}
	return v, false
}

func remoteIP(r *http.Request) (netip.Addr, error) {
	if r == nil {
		return netip.Addr{}, fmt.Errorf("nil request")
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return netip.Addr{}, fmt.Errorf("missing remote addr")
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, err
	}
	return addr, nil
}

func hasVerifiedClientCert(tlsState *tls.ConnectionState) bool {
	if tlsState == nil {
		return false
	}
	// VerifiedChains is only set when mutual TLS verification succeeded.
	return len(tlsState.VerifiedChains) > 0
}

func audienceAllowed(audClaim any, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = struct{}{}
	}
	switch v := audClaim.(type) {
	case string:
		_, ok := allowedSet[v]
		return ok
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				if _, ok := allowedSet[s]; ok {
					return true
				}
			}
		}
	}
	return false
}

func claimStringRequired(claims jwt.MapClaims, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("empty claim key")
	}
	val, ok := claims[key]
	if !ok {
		return "", fmt.Errorf("missing claim %q", key)
	}
	s, ok := val.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("invalid claim %q", key)
	}
	return s, nil
}

func claimStringOptional(claims jwt.MapClaims, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	val, ok := claims[key]
	if !ok {
		return "", false
	}
	s, ok := val.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

func renderKeyTemplate(tmpl string, vars map[string]string) (string, error) {
	if strings.TrimSpace(tmpl) == "" {
		return "", fmt.Errorf("empty key template")
	}
	out := tmpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	if strings.Contains(out, "{") || strings.Contains(out, "}") {
		return "", fmt.Errorf("unresolved placeholder in key template")
	}
	return out, nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
//
// Syntax:
//
//	gateway_jwt {
//		jwks_url <url>
//		issuer <iss>
//		audience <aud>
//		allowed_algs <alg...>
//		trusted_source <cidr>
//		require_mtls
//		version_key_template <tmpl>
//		instance_key_template <tmpl>
//		key_prefix <prefix>
//	}
func (g *GatewayJWT) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "jwks_url":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.JWKSURL = d.Val()
			case "issuer":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.Issuer = d.Val()
			case "audience":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.Audiences = append(g.Audiences, d.Val())
			case "allowed_algs":
				g.AllowedAlgs = g.AllowedAlgs[:0]
				for d.NextArg() {
					g.AllowedAlgs = append(g.AllowedAlgs, d.Val())
				}
				if len(g.AllowedAlgs) == 0 {
					return d.ArgErr()
				}
			case "leeway":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid leeway: %v", err)
				}
				g.Leeway = caddy.Duration(dur)
			case "token_header":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.TokenHeader = d.Val()
			case "token_prefix":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.TokenPrefix = d.Val()
			case "optional":
				g.Optional = true
			case "trusted_source":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.TrustedSources = append(g.TrustedSources, d.Val())
			case "require_mtls":
				g.RequireMTLS = true
			case "allowed_client_cert_sha256":
				for d.NextArg() {
					g.AllowedClientCertSHA256 = append(g.AllowedClientCertSHA256, d.Val())
				}
				if len(g.AllowedClientCertSHA256) == 0 {
					return d.ArgErr()
				}
			case "claim_tenant":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.ClaimTenant = d.Val()
			case "claim_service":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.ClaimService = d.Val()
			case "claim_version":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.ClaimVersion = d.Val()
			case "claim_instance":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.ClaimInstance = d.Val()
			case "version_key_template":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.VersionKeyTemplate = d.Val()
			case "instance_key_template":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.InstanceKeyTemplate = d.Val()
			case "key_prefix":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.KeyPrefix = d.Val()
			case "jwks_refresh_interval":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid jwks_refresh_interval: %v", err)
				}
				g.JWKSRefreshInterval = caddy.Duration(dur)
			case "jwks_timeout":
				if !d.NextArg() {
					return d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return d.Errf("invalid jwks_timeout: %v", err)
				}
				g.JWKSTimeout = caddy.Duration(dur)
			case "allow_insecure_jwks":
				g.AllowInsecureJWKS = true
			default:
				return d.Errf("unrecognized subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

func parseGatewayJWTDirective(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var g GatewayJWT
	if err := g.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return &g, nil
}

// --- JWKS cache implementation ---

type jwksCacheConfig struct {
	URL           string
	Refresh       time.Duration
	Timeout       time.Duration
	Logger        *zap.Logger
	Now           func() time.Time
	AllowInsecure bool

	ExpectedIssuer string
}

type jwksCache struct {
	conf      jwksCacheConfig
	refreshMu sync.Mutex

	mu          sync.RWMutex
	keysByKID   map[string]any
	lastFetched time.Time

	// caching headers
	etag          string
	lastModified  string
	nextRefreshAt time.Time
}

func newJWKSCache(conf jwksCacheConfig) *jwksCache {
	if conf.Logger == nil {
		conf.Logger = zap.NewNop()
	}
	if conf.Now == nil {
		conf.Now = time.Now
	}
	return &jwksCache{
		conf:        conf,
		keysByKID:   make(map[string]any),
		etag:        "",
		lastFetched: time.Time{},
	}
}

func (c *jwksCache) KeyForToken(ctx context.Context, t *jwt.Token) (any, error) {
	kid, _ := t.Header["kid"].(string)
	if kid != "" {
		if key := c.keyByKID(kid); key != nil {
			return key, nil
		}
		// one refresh attempt
		if err := c.Refresh(ctx); err != nil {
			return nil, err
		}
		if key := c.keyByKID(kid); key != nil {
			return key, nil
		}
		return nil, fmt.Errorf("unknown kid")
	}

	// No kid: if exactly one key exists, use it.
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.keysByKID) == 1 {
		for _, k := range c.keysByKID {
			return k, nil
		}
	}
	return nil, fmt.Errorf("token missing kid")
}

func (c *jwksCache) keyByKID(kid string) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.keysByKID[kid]
}

func (c *jwksCache) shouldRefresh(now time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.nextRefreshAt.IsZero() {
		return true
	}
	return now.After(c.nextRefreshAt)
}

func (c *jwksCache) Refresh(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	now := c.conf.Now()
	if !c.shouldRefresh(now) {
		return nil
	}

	u, err := url.Parse(c.conf.URL)
	if err != nil {
		return fmt.Errorf("invalid jwks_url: %v", err)
	}
	if !c.conf.AllowInsecure && u.Scheme != "https" {
		return fmt.Errorf("jwks_url must be https unless allow_insecure_jwks is true")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.conf.URL, nil)
	if err != nil {
		return err
	}

	// conditional headers
	c.mu.RLock()
	etag := c.etag
	lastModified := c.lastModified
	c.mu.RUnlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	client := &http.Client{Timeout: c.conf.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching jwks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		c.mu.Lock()
		c.lastFetched = now
		c.nextRefreshAt = now.Add(c.conf.Refresh)
		c.mu.Unlock()
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("jwks fetch status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("reading jwks: %v", err)
	}

	keysByKID, err := parseJWKSKeysByKID(body)
	if err != nil {
		return err
	}
	if len(keysByKID) == 0 {
		return errors.New("jwks contained no keys")
	}

	newETag := resp.Header.Get("ETag")
	newLastMod := resp.Header.Get("Last-Modified")

	c.mu.Lock()
	c.keysByKID = keysByKID
	c.etag = newETag
	c.lastModified = newLastMod
	c.lastFetched = now
	c.nextRefreshAt = now.Add(c.conf.Refresh)
	c.mu.Unlock()

	return nil
}

func parseJWKSKeysByKID(jwksJSON []byte) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(jwksJSON, &raw); err != nil {
		return nil, fmt.Errorf("parsing jwks json: %v", err)
	}
	keysVal, ok := raw["keys"]
	if !ok {
		return nil, fmt.Errorf("invalid jwks: missing keys")
	}
	keysArr, ok := keysVal.([]any)
	if !ok {
		return nil, fmt.Errorf("invalid jwks: keys not array")
	}

	out := make(map[string]any, len(keysArr))
	for _, item := range keysArr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kid, _ := m["kid"].(string)
		kty, _ := m["kty"].(string)
		if kid == "" || kty == "" {
			continue
		}
		key, err := jwkToPublicKey(m)
		if err != nil {
			return nil, err
		}
		out[kid] = key
	}
	return out, nil
}

// jwkToPublicKey converts a minimal JWK object into a crypto.PublicKey understood by golang-jwt.
// Supported kty: RSA, EC.
func jwkToPublicKey(jwk map[string]any) (any, error) {
	// To keep dependencies small and deterministic, we implement only RSA/EC JWK parsing.
	// If you need OKP or X.509 chain support, extend this.
	kty, _ := jwk["kty"].(string)
	switch kty {
	case "RSA":
		return parseRSAJWK(jwk)
	case "EC":
		return parseECJWK(jwk)
	default:
		return nil, fmt.Errorf("unsupported jwk kty %q", kty)
	}
}

func parseRSAJWK(jwk map[string]any) (any, error) {
	// Minimal RSA public key parsing from n/e.
	nStr, _ := jwk["n"].(string)
	eStr, _ := jwk["e"].(string)
	if nStr == "" || eStr == "" {
		return nil, fmt.Errorf("invalid RSA jwk: missing n/e")
	}
	nBytes, err := decodeB64URL(nStr)
	if err != nil {
		return nil, fmt.Errorf("decoding rsa n: %v", err)
	}
	eBytes, err := decodeB64URL(eStr)
	if err != nil {
		return nil, fmt.Errorf("decoding rsa e: %v", err)
	}
	// Convert exponent bytes (big-endian) to int.
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("invalid rsa exponent")
	}
	n := new(big.Int).SetBytes(nBytes)
	if n.Sign() <= 0 {
		return nil, fmt.Errorf("invalid rsa modulus")
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}

func parseECJWK(jwk map[string]any) (any, error) {
	crv, _ := jwk["crv"].(string)
	xStr, _ := jwk["x"].(string)
	yStr, _ := jwk["y"].(string)
	if crv == "" || xStr == "" || yStr == "" {
		return nil, fmt.Errorf("invalid EC jwk: missing crv/x/y")
	}
	xBytes, err := decodeB64URL(xStr)
	if err != nil {
		return nil, fmt.Errorf("decoding ec x: %v", err)
	}
	yBytes, err := decodeB64URL(yStr)
	if err != nil {
		return nil, fmt.Errorf("decoding ec y: %v", err)
	}
	curve, err := parseECCurve(crv)
	if err != nil {
		return nil, err
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	if x.Sign() < 0 || y.Sign() < 0 {
		return nil, fmt.Errorf("invalid ec point")
	}
	if !curve.IsOnCurve(x, y) {
		return nil, fmt.Errorf("ec point not on curve")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

func parseECCurve(crv string) (elliptic.Curve, error) {
	switch crv {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported ec curve %q", crv)
	}
}

func decodeB64URL(s string) ([]byte, error) {
	// JWK uses base64url without padding.
	return base64.RawURLEncoding.DecodeString(s)
}
