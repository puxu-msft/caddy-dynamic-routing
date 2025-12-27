# Dev Debug Logs

This example shows how to enable Caddy debug logs for troubleshooting dynamic routing.

## Enable debug logs (Caddyfile)

Add a global options block with `debug`:

```caddy
{
    debug
}
```

## Run with the Caddyfile

```bash
./caddy run --config ./Caddyfile --adapter caddyfile
```

With debug enabled, the dynamic routing path logs messages like:

- no routing key extracted, using fallback
- data source unhealthy, using fallback
- failed to get route config
- selected upstream via dynamic routing
