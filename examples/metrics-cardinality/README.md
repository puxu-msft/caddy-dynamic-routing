# Metrics cardinality mode

This example shows how to reduce Prometheus time-series cardinality in high-key (multi-tenant) deployments.

## Why

By default, route hit/miss metrics include the routing `key` (and hits include `upstream`) as Prometheus labels.
In environments with many keys, this can create an unbounded number of time series.

## Coarse mode

Set `metrics_cardinality coarse` to collapse the `key`/`upstream` labels to a constant value.
Miss `reason` remains labeled.

### Caddyfile

```caddyfile
{
	admin :2019
}

:8080 {
	reverse_proxy {
		to 127.0.0.1:9001 127.0.0.1:9002

		lb_policy dynamic {
			key {http.request.header.X-Tenant}
			metrics_cardinality coarse

			# pick exactly one data source block
			etcd {
				endpoints localhost:2379
				prefix /caddy/routing/
			}
		}
	}
}
```

## JSON config

If you use JSON, the equivalent field is:

- `metrics_cardinality`: `"coarse"` or `"detailed"`
