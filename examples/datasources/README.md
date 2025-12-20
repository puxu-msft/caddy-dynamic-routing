# Data Sources

This directory contains examples for each supported data source.

## Supported Data Sources

| Source | Description | Use Case |
|--------|-------------|----------|
| `etcd` | etcd v3 key-value store | Production clusters |
| `redis` | Redis with Pub/Sub | Low-latency needs |
| `consul` | HashiCorp Consul KV | Service mesh integration |
| `file` | Local file system | Development, testing |
| `http` | HTTP/REST endpoint | Custom backends |
| `composite` | Combine multiple sources | High availability |
| `zookeeper` | Apache Zookeeper | Distributed coordination |
| `sql` | MySQL/PostgreSQL | Database-backed config |
| `kubernetes` | Kubernetes ConfigMap | Cloud-native deployments |

## etcd Configuration

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    etcd {
        endpoints etcd1:2379 etcd2:2379 etcd3:2379
        prefix /caddy/routing/
        dial_timeout 5s

        # Optional: TLS
        tls {
            cert /path/to/cert.pem
            key /path/to/key.pem
            ca /path/to/ca.pem
        }

        # Optional: Authentication
        username admin
        password secret
    }
}
```

## Redis Configuration

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    redis {
        addr redis:6379
        prefix routing:
        db 0

        # Optional: Authentication
        password secret

        # Optional: Cluster mode
        # cluster true
        # addrs redis1:6379 redis2:6379 redis3:6379
    }
}
```

## Consul Configuration

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    consul {
        addr consul:8500
        prefix caddy/routing/
        token <acl-token>
        datacenter dc1
    }
}
```

## File Configuration

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    file {
        path /etc/caddy/routes/
        format yaml
        watch true
    }
}
```

File structure:
```
/etc/caddy/routes/
├── tenant-a.yaml
├── tenant-b.yaml
└── tenant-c.json
```

## HTTP Configuration

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    http {
        url http://config-service:8080/routes/{key}
        timeout 5s
        headers {
            Authorization "Bearer <token>"
        }
        cache_ttl 30s
    }
}
```

## Zookeeper Configuration

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    zookeeper {
        servers zk1:2181 zk2:2181 zk3:2181
        prefix /caddy/routing/
        session_timeout 10s
        connect_timeout 5s

        # Optional: Authentication
        auth digest user:password
    }
}
```

## SQL Configuration

### MySQL

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    sql {
        driver mysql
        dsn "user:password@tcp(localhost:3306)/caddy"
        table routing_configs
        key_column route_key
        value_column config
        poll_interval 30s
        max_open_conns 10
        max_idle_conns 5
    }
}
```

### PostgreSQL

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    sql {
        driver postgres
        dsn "postgres://user:password@localhost:5432/caddy?sslmode=disable"
        table routing_configs
        key_column route_key
        value_column config
        poll_interval 30s
    }
}
```

## Kubernetes Configuration

```caddy
lb_policy dynamic {
    key {http.request.header.X-Tenant}

    kubernetes {
        namespace production
        configmap_name caddy-routes
        label_selector app=caddy

        # Optional: Use external kubeconfig (defaults to in-cluster config)
        # kubeconfig /path/to/kubeconfig
    }
}
```
