# PawScript Networking Plan

## Overview

This document outlines a proposed design for networking in PawScript that aligns with the language's philosophy of being sandboxed by default while allowing host applications to extend capabilities.

## Design Principles

1. **Sandboxed by default** - No network access without explicit permission
2. **Practical defaults** - HTTP client covers 90% of scripting needs
3. **Host extensibility** - Advanced networking provided by host applications
4. **Async-friendly** - All operations integrate with PawScript's paws system

## Tiered Access Model

### Tier 1: Built-in (with sandboxing)

These are common enough needs that they should be in the core, but with domain/path restrictions.

#### HTTP/HTTPS Client

The most common scripting need. Sandboxable via domain allowlisting:

```pawscript
# Simple GET
response: {http_get "https://api.example.com/users"}
echo ~response.status    # 200
echo ~response.body      # JSON string
data: {list from: json, ~response.body}

# POST with headers and body
response: {http_post "https://api.example.com/users",
    headers: {list Content-Type: "application/json"},
    body: {json {list name: "Alice"}}
}

# Full request control
response: {http_request "PUT", "https://api.example.com/users/123",
    headers: {list Authorization: "Bearer ~token"},
    body: ~payload,
    timeout: 30000  # ms
}

# Download file to disk
http_download "https://example.com/file.zip", "/tmp/file.zip"
```

#### Unix Sockets

Fits the "local services" model, restricted like file paths:

```pawscript
# Connect to Docker
sock: {socket_connect "/var/run/docker.sock"}
socket_write ~sock, "GET /containers/json HTTP/1.0\r\n\r\n"
response: {socket_read ~sock}
socket_close ~sock
```

### Tier 2: Built-in (disabled by default)

These are useful but require explicit opt-in.

#### WebSockets

Real-time communication for chat, live updates, streaming:

```pawscript
ws: {websocket_connect "wss://stream.example.com/feed"}

# Async receive with fibers
fiber (
    while true, (
        msg: {websocket_recv ~ws}
        if ~msg then (
            echo "Received:", ~msg
        ) else (
            break
        )
    )
)

websocket_send ~ws, "subscribe:ticker"
websocket_close ~ws
```

### Tier 3: Host-provided only

These are too dangerous for sandboxed scripts and must be explicitly provided by the host application.

#### Raw TCP/UDP Client

Arbitrary network access to any host/port:

```pawscript
# Script can only connect to what host explicitly allows
conn: {tcp_connect "game-server", "game.example.com", 7777}
tcp_write ~conn, ~packet
response: {tcp_read ~conn, 1024}
tcp_close ~conn
```

#### Server Sockets (Listening)

Opening ports is always a host decision:

```pawscript
# Only works if host provided a listener
client: {tcp_accept "webhook-server"}
request: {tcp_read ~client, 4096}
tcp_write ~client, "HTTP/1.0 200 OK\r\n\r\nOK"
tcp_close ~client
```

#### UDP

Connectionless messaging:

```pawscript
udp_send "metrics", "statsd.local", 8125, "myapp.requests:1|c"
```

## Proposed Command Set

| Command | Tier | Description |
|---------|------|-------------|
| `http_get url, ...` | 1 | Simple GET request |
| `http_post url, ...` | 1 | POST with body/headers |
| `http_request method, url, ...` | 1 | Full HTTP control |
| `http_download url, path` | 1 | Download file to disk |
| `socket_connect path` | 1 | Unix socket connection |
| `socket_read ~sock, ...` | 1 | Read from socket |
| `socket_write ~sock, data` | 1 | Write to socket |
| `socket_close ~sock` | 1 | Close socket |
| `websocket_connect url` | 2 | WebSocket client |
| `websocket_send ~ws, msg` | 2 | Send WebSocket message |
| `websocket_recv ~ws` | 2 | Receive message (async) |
| `websocket_close ~ws` | 2 | Close WebSocket |
| `tcp_connect name, host, port` | 3 | Host-provided TCP |
| `tcp_read ~conn, maxlen` | 3 | Read from TCP |
| `tcp_write ~conn, data` | 3 | Write to TCP |
| `tcp_close ~conn` | 3 | Close TCP connection |
| `tcp_accept name` | 3 | Accept on host-provided listener |
| `udp_send name, host, port, data` | 3 | Host-provided UDP send |

## HTTP Response Structure

HTTP commands return a list with:

```pawscript
response: {http_get "https://api.example.com/data"}

echo ~response.status       # 200
echo ~response.status_text  # "OK"
echo ~response.headers      # list of headers
echo ~response.body         # response body as string
echo ~response.elapsed      # request time in ms
```

## Host-Side Configuration (Go)

### Network Sandbox Configuration

```go
ps.SetNetworkConfig(&pawscript.NetworkConfig{
    // HTTP sandboxing
    AllowedDomains: []string{
        "api.example.com",
        "*.internal.corp",
        "httpbin.org",
    },
    BlockedDomains: []string{"*.evil.com"},

    // Unix socket paths (like file roots)
    AllowedSocketPaths: []string{
        "/var/run/docker.sock",
        "/tmp/*.sock",
    },

    // Timeouts
    DefaultTimeout: 30 * time.Second,
    MaxTimeout:     5 * time.Minute,

    // Features
    AllowWebSockets: true,
    AllowInsecureHTTP: false,  // Require HTTPS
})
```

### Providing Raw TCP/UDP Access

```go
// Allow TCP connections to specific hosts
ps.RegisterTCPDialer("game-server", &TCPConfig{
    AllowedHosts: []string{"game.example.com:7777"},
})

// Provide a TCP listener
listener, _ := net.Listen("tcp", ":8080")
ps.RegisterTCPListener("webhook-server", listener)

// Allow UDP to metrics server
ps.RegisterUDPEndpoint("metrics", &UDPConfig{
    AllowedHosts: []string{"statsd.local:8125"},
})
```

## Security Model

| Level | Network Access |
|-------|----------------|
| `--sandbox` (default) | None |
| `--allow-http` | HTTP to allowed domains only |
| `--allow-http=*` | HTTP to any domain |
| `--allow-sockets` | Unix sockets to allowed paths |
| `--allow-websockets` | WebSocket connections |
| `--allow-network` | All built-in networking |
| `--unrestricted` | Everything including host-provided |

## Risk Assessment

| Protocol | Risk Level | Justification |
|----------|------------|---------------|
| **HTTP/S Client** | Medium | Ubiquitous need, sandboxable via domain allowlist |
| **Unix Sockets** | Low | Local only, path-restricted like files |
| **WebSockets** | Medium | Real-time apps common, but opt-in |
| **Raw TCP Client** | High | Arbitrary network access |
| **Raw UDP** | High | Can be used for amplification attacks |
| **TCP Listener** | Very High | Opens attack surface, exposes ports |

## Async Integration

All network operations integrate with PawScript's paws/fiber system:

```pawscript
# Non-blocking with timeout
response: {http_get "https://slow-api.com/data", timeout: 5000}

# Parallel requests using fibers
urls: ("https://api1.com", "https://api2.com", "https://api3.com")
results: {list}

for ~urls, url, (
    fiber (
        resp: {http_get ~url}
        bubble "responses", {list url: ~url, data: ~resp}
    )
)

# Collect results
msleep 5000  # or use proper synchronization
fizz "responses", result, (
    echo ~result.url, "->", ~result.data.status
    burst
)
```

## Example: REST API Client

```pawscript
# Define API helper macro
macro api_call (
    method: $1
    endpoint: $2
    body: $3

    base_url: "https://api.example.com"
    response: {http_request ~method, "~base_url~endpoint",
        headers: {list
            Content-Type: "application/json",
            Authorization: "Bearer ~api_token"
        },
        body: {json ~body}
    }

    if {gte ~response.status, 400} then (
        bubble "api_errors", {list
            endpoint: ~endpoint,
            status: ~response.status,
            body: ~response.body
        }
        ret status: false
    )

    set_result {list from: json, ~response.body}
)

# Usage
users: {api_call "GET", "/users", nil}
new_user: {api_call "POST", "/users", {list name: "Alice", email: "alice@example.com"}}
```

## Example: WebSocket Chat Client

```pawscript
ws: {websocket_connect "wss://chat.example.com/room/123"}

# Receiver fiber
fiber (
    while true, (
        msg: {websocket_recv ~ws}
        if ~msg then (
            data: {list from: json, ~msg}
            echo "[~data.user]:", ~data.message
        ) else (
            echo "Disconnected"
            break
        )
    )
)

# Send messages from stdin
while true, (
    write "> "
    message: {read}
    if {eq ~message, "/quit"} then (
        break
    )
    websocket_send ~ws, {json {list message: ~message}}
)

websocket_close ~ws
```

## Implementation Notes

### Go Libraries

- **HTTP**: Standard `net/http` package
- **WebSockets**: `github.com/gorilla/websocket` or `nhooyr.io/websocket`
- **Unix Sockets**: Standard `net` package with `unix` network type

### Connection Pooling

HTTP connections should use Go's built-in connection pooling via `http.Client` with appropriate transport settings.

### Timeouts

All operations should respect timeouts and integrate with context cancellation for proper cleanup.

## What This Design Intentionally Excludes

- **DNS lookups** - Too low-level, security implications
- **Raw IP sockets** - Requires elevated privileges
- **Network interface enumeration** - Information disclosure risk
- **Proxy configuration in scripts** - Host should control this
- **TLS certificate management** - Host responsibility

## Summary

| Category | Approach |
|----------|----------|
| **HTTP/HTTPS** | Built-in, domain-sandboxed |
| **Unix Sockets** | Built-in, path-sandboxed |
| **WebSockets** | Built-in, opt-in |
| **Raw TCP/UDP** | Host-provided only |
| **Listeners** | Host-provided only |

This design provides practical networking for scripting while maintaining PawScript's security-first philosophy. The 90% use case (HTTP API calls) is easy and safe; advanced use cases require explicit host cooperation.
