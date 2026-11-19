# Architecture decisions

## Initial topology

The MVP puts the limiter in the Go gateway's request path. The gateway performs one atomic Redis Lua operation per decision. Running several gateway replicas does not split quota state: the Redis key derived from the caller identity is shared by all replicas.

This follows the edge-placement direction in the supplied system-design transcript and the shared-quota demonstration in the SandeepBaghel reference. It avoids a separate decision-service hop while preserving a shared distributed state store.

## Redis is the quota database

Redis stores ephemeral token-bucket state (current tokens and last refill time). The Lua script reads, refills, decides, updates, and sets expiry atomically. Redis server time is used so gateway clock skew does not change refill decisions. Redis Cluster can be introduced later because each check touches one key and therefore one hash slot.

The MVP uses one Redis instance in Compose for ease of development. This is not the scale-out or high-availability topology; those claims require the later cluster and failure experiments.

## Why no gRPC in the MVP hot path

A gRPC decision service is a valid alternative and useful for Envoy compatibility, but it adds a network hop between the gateway and limiter. Since this project's first performance goal is to minimize request-path latency, begin with in-process Go middleware and Redis. Add a remote gRPC mode only as a measured comparison if the service-boundary trade-off is useful.

## Why no PostgreSQL in the MVP

The hot-path state needs low-latency atomic updates and expiry. Redis is the appropriate state store for that. PostgreSQL would only be added if a later feature needs durable rule CRUD, tenant records, or audit history; it would not replace Redis bucket state.

## Known consistency limit

Redis Cluster scripts are atomic on the node that owns a key. Cluster replication is asynchronous, so a failover can lose recent quota writes. The project must measure and document possible over-admission during failover rather than claim strict durability or globally linearizable state.

## Phase 1 failure policy

Quota breaches fail fast with HTTP 429 and are never queued in gateway memory. When Redis is slow or unavailable, each gateway uses a 10 ms decision budget and fails open so the product remains available. This intentionally allows unmetered traffic during an outage and favors availability over strict quota enforcement. Responses carry `RateLimit-Degraded: fail-open`, and the event is observable in Prometheus. A bounded local counter fallback is deferred to the resilience phase so its per-replica quota drift can be measured explicitly.

## Algorithm selection

Each path selects one algorithm in static rule configuration. The engine implements token bucket, fixed window, and sliding-window counter with Redis Lua. Healthy requests still execute one atomic script against shared Redis state. The engine computes a stable version hash from normalized rules; Prometheus labels it per gateway so configuration drift is visible.
