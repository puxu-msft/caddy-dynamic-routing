package caddyslb

import (
	"encoding/json"

	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"

	"github.com/puxu-msft/caddy-dynamic-routing/datasource/consul"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/etcd"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/file"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/http"
	"github.com/puxu-msft/caddy-dynamic-routing/datasource/redis"
)

func init() {
	httpcaddyfile.RegisterDirective("lb_policy_dynamic", parseCaddyfile)
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
//
// Syntax:
//
//	lb_policy dynamic {
//	    key {http.request.header.X-Tenant}
//
//	    etcd {
//	        endpoints localhost:2379 localhost:2380
//	        prefix /caddy/routing/
//	        dial_timeout 5s
//	        username admin
//	        password secret
//	        tls {
//	            cert /path/to/cert.pem
//	            key /path/to/key.pem
//	            ca /path/to/ca.pem
//	        }
//	    }
//
//	    # OR redis
//	    redis {
//	        addresses localhost:6379
//	        password secret
//	        db 0
//	        prefix caddy:routing:
//	        cluster
//	        pubsub_channel caddy:routing:updates
//	    }
//
//	    # OR consul
//	    consul {
//	        address localhost:8500
//	        scheme https
//	        token my-acl-token
//	        prefix caddy/routing/
//	    }
//
//	    # OR file
//	    file {
//	        path /etc/caddy/routes/
//	        watch true
//	    }
//
//	    # OR http
//	    http {
//	        url http://config-service/routes
//	        timeout 5s
//	        cache_ttl 1m
//	        header Authorization "Bearer token"
//	    }
//
//	    fallback random
//	}
func (s *DynamicSelection) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	// Consume the directive name
	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "key":
				if !d.NextArg() {
					return d.ArgErr()
				}
				s.Key = d.Val()

			case "etcd":
				etcdSource := &etcd.EtcdSource{}
				if err := etcdSource.UnmarshalCaddyfile(d.NewFromNextSegment()); err != nil {
					return err
				}
				s.DataSourceRaw = caddyconfig.JSONModuleObject(etcdSource, "source", "etcd", nil)

			case "redis":
				redisSource := &redis.RedisSource{}
				if err := redisSource.UnmarshalCaddyfile(d.NewFromNextSegment()); err != nil {
					return err
				}
				s.DataSourceRaw = caddyconfig.JSONModuleObject(redisSource, "source", "redis", nil)

			case "consul":
				consulSource := &consul.ConsulSource{}
				if err := consulSource.UnmarshalCaddyfile(d.NewFromNextSegment()); err != nil {
					return err
				}
				s.DataSourceRaw = caddyconfig.JSONModuleObject(consulSource, "source", "consul", nil)

			case "file":
				fileSource := &file.FileSource{}
				if err := fileSource.UnmarshalCaddyfile(d.NewFromNextSegment()); err != nil {
					return err
				}
				s.DataSourceRaw = caddyconfig.JSONModuleObject(fileSource, "source", "file", nil)

			case "http":
				httpSource := &http.HTTPSource{}
				if err := httpSource.UnmarshalCaddyfile(d.NewFromNextSegment()); err != nil {
					return err
				}
				s.DataSourceRaw = caddyconfig.JSONModuleObject(httpSource, "source", "http", nil)

			case "fallback":
				if !d.NextArg() {
					return d.ArgErr()
				}
				policyName := d.Val()
				// Create the fallback policy based on name
				fallbackPolicy := createFallbackPolicy(policyName)
				if fallbackPolicy != nil {
					s.FallbackRaw = caddyconfig.JSONModuleObject(fallbackPolicy, "policy", policyName, nil)
				}

			default:
				return d.Errf("unrecognized subdirective: %s", d.Val())
			}
		}
	}

	return nil
}

// createFallbackPolicy creates a fallback selection policy by name.
func createFallbackPolicy(name string) interface{} {
	switch name {
	case "random":
		return &reverseproxy.RandomSelection{}
	case "round_robin":
		return &reverseproxy.RoundRobinSelection{}
	case "least_conn":
		return &reverseproxy.LeastConnSelection{}
	case "first":
		return &reverseproxy.FirstSelection{}
	case "ip_hash":
		return &reverseproxy.IPHashSelection{}
	case "uri_hash":
		return &reverseproxy.URIHashSelection{}
	default:
		// Return a generic map for unknown policies
		return map[string]interface{}{}
	}
}

// parseCaddyfile is a helper for parsing the directive in global context.
func parseCaddyfile(h httpcaddyfile.Helper) ([]httpcaddyfile.ConfigValue, error) {
	if !h.Next() {
		return nil, h.ArgErr()
	}

	var s DynamicSelection
	if err := s.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}

	return []httpcaddyfile.ConfigValue{
		{
			Class: "lb_policy",
			Value: &s,
		},
	}, nil
}

// MarshalJSON implements json.Marshaler to ensure proper JSON output.
func (s DynamicSelection) MarshalJSON() ([]byte, error) {
	type Alias DynamicSelection
	return json.Marshal(&struct {
		Policy string `json:"policy"`
		Alias
	}{
		Policy: "dynamic",
		Alias:  (Alias)(s),
	})
}

// Interface guards
var (
	_ caddyfile.Unmarshaler = (*DynamicSelection)(nil)
)
