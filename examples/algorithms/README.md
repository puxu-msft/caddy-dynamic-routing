# Load Balancing Algorithms

This directory contains examples for each supported load balancing algorithm.

## Available Algorithms

| Algorithm | Description | Use Case |
|-----------|-------------|----------|
| `round_robin` | Distributes requests sequentially | General purpose |
| `weighted_round_robin` | Sequential with weights | Uneven server capacity |
| `weighted_random` | Random with weights (default) | General purpose |
| `ip_hash` | Consistent routing by client IP | Session affinity |
| `uri_hash` | Consistent routing by URI | Caching |
| `header_hash` | Consistent routing by header value | Custom affinity |
| `consistent_hash` | Virtual node consistent hashing | Cache clusters |
| `first` | Always selects first available | Primary/backup |

## Configuration Examples

### Round Robin

```json
{
  "rules": [{
    "upstreams": [
      {"address": "backend1:8080"},
      {"address": "backend2:8080"},
      {"address": "backend3:8080"}
    ]
  }],
  "algorithm": "round_robin"
}
```

### Weighted Round Robin

```json
{
  "rules": [{
    "upstreams": [
      {"address": "backend1:8080", "weight": 5},
      {"address": "backend2:8080", "weight": 3},
      {"address": "backend3:8080", "weight": 2}
    ]
  }],
  "algorithm": "weighted_round_robin"
}
```

### IP Hash (Session Affinity)

```json
{
  "rules": [{
    "upstreams": [
      {"address": "backend1:8080"},
      {"address": "backend2:8080"}
    ]
  }],
  "algorithm": "ip_hash"
}
```

### Header Hash

```json
{
  "rules": [{
    "upstreams": [
      {"address": "backend1:8080"},
      {"address": "backend2:8080"}
    ]
  }],
  "algorithm": "header_hash",
  "algorithm_key": "X-User-ID"
}
```

### Consistent Hash (for Caching)

```json
{
  "rules": [{
    "upstreams": [
      {"address": "cache1:8080"},
      {"address": "cache2:8080"},
      {"address": "cache3:8080"}
    ]
  }],
  "algorithm": "consistent_hash",
  "algorithm_key": "X-Cache-Key"
}
```

### First (Primary/Backup)

```json
{
  "rules": [{
    "upstreams": [
      {"address": "primary:8080"},
      {"address": "backup:8080"}
    ]
  }],
  "algorithm": "first"
}
```

## YAML Format Examples

See the `.yaml` files in this directory for YAML format examples.
