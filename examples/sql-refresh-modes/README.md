# SQL Refresh Modes Example

This example shows how to configure the SQL datasource to use either:

- full refresh (default): periodically scans the whole table and reconciles hard deletes
- incremental refresh: polls only rows updated since the last cursor, with an optional periodic full reconcile

> Note: incremental refresh requires an `updated_at` (or equivalent) column that monotonically increases for updates.
> If you need delete propagation without periodic full refresh, add a soft-delete column (e.g. `deleted_at`).

## Full refresh (default)

```caddy
{
    admin :2019
}

example.com {
    reverse_proxy {
        lb_policy dynamic {
            key {http.request.header.X-Tenant}

            sql {
                driver postgres
                dsn postgres://user:pass@localhost:5432/routing?sslmode=disable

                table routing_configs
                key_column routing_key
                config_column config

                # Full refresh is the default mode.
                poll_interval 30s

                # Optional: fail fast if initial load is slow.
                initial_load_timeout 30s
            }

            fallback random
        }
    }
}
```

## Incremental refresh (cursor-based)

```caddy
{
    admin :2019
}

example.com {
    reverse_proxy {
        lb_policy dynamic {
            key {http.request.header.X-Tenant}

            sql {
                driver postgres
                dsn postgres://user:pass@localhost:5432/routing?sslmode=disable

                table routing_configs
                key_column routing_key
                config_column config

                poll_interval 2s

                refresh_mode incremental
                updated_at_column updated_at

                # Optional: treat rows as deleted when deleted_at IS NOT NULL.
                deleted_at_column deleted_at

                # Optional: periodic full refresh to reconcile hard deletes.
                full_refresh_interval 5m

                initial_load_timeout 30s
            }

            fallback random
        }
    }
}
```
