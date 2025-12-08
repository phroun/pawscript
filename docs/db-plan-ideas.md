# PawScript Database Connectivity Plan

## Overview

This document outlines a proposed design for database connectivity in PawScript that aligns with the language's philosophy of being embedded, sandboxed, and host-extensible.

## Design Goals

1. **Built-in SQLite** - Zero-config, file-based database that fits the sandbox model
2. **Host-provided connections** - Allow host applications to provide connections to other SQL databases
3. **Unified interface** - Same PawScript commands work regardless of backend
4. **Portable scripts** - Code developed against SQLite can run unchanged against PostgreSQL/MySQL in production

## Baseline: SQLite (Built-in)

SQLite is the essential baseline for several reasons:

- File-based, fits the existing sandbox model perfectly
- Zero configuration, no server process required
- Database files can be restricted to write roots like any other file
- Ubiquitous - Tcl, Lua, Python all standardized on SQLite
- Pure Go implementation available (`modernc.org/sqlite`)

## Script-Side API (Unified Interface)

Scripts use the same commands regardless of whether they're talking to SQLite or a host-provided database:

```pawscript
# Open a named database connection
db: {sql_open "main"}

# Execute statements (INSERT/UPDATE/DELETE)
sql_exec ~db, "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, active BOOLEAN)"
sql_exec ~db, "INSERT INTO users (name, active) VALUES (?, ?)", "Alice", true

# Query with parameters (SELECT)
rows: {sql_query ~db, "SELECT * FROM users WHERE active = ?", true}

for ~rows, row, (
    echo "User:", ~row.name
)

# Close when done
sql_close ~db
```

## Proposed Command Set

| Command | Description |
|---------|-------------|
| `sql_open name` | Get handle to named database |
| `sql_close ~db` | Release connection |
| `sql_exec ~db, query, ...args` | Execute (INSERT/UPDATE/DELETE), return rows affected |
| `sql_query ~db, query, ...args` | Query (SELECT), return list of row-lists |
| `sql_transaction ~db, (body)` | Execute body in transaction, auto-commit/rollback |
| `sql_tables ~db` | List table names |
| `sql_columns ~db, table` | List column info for table |

## Host-Side API (Go)

### Registering Built-in SQLite

```go
// Built-in SQLite - always available, sandboxed to file roots
ps.RegisterSQLiteDatabase("local", "./data.db")
ps.RegisterSQLiteDatabase("cache", "/tmp/cache.db")
```

### Registering Host-Provided Databases

Host applications can provide connections to any database that implements Go's `database/sql` interface:

```go
// PostgreSQL connection pool
ps.RegisterSQLDatabase("main", myPostgresPool)      // *sql.DB

// ClickHouse for analytics
ps.RegisterSQLDatabase("analytics", myClickHouseConn)

// Any other SQL-compatible database
ps.RegisterSQLDatabase("warehouse", mySnowflakeConn)
```

### Factory Pattern for Dynamic Connections

For multi-tenant or lazy-loaded connections:

```go
ps.RegisterSQLDatabaseFactory("tenant", func(ctx *pawscript.Context) (*sql.DB, error) {
    tenantID := ctx.GetVariable("tenant_id")
    return getTenantDatabase(tenantID)
})
```

## Security Model

| Context | Database Access |
|---------|-----------------|
| Sandboxed scripts | SQLite only, restricted to file write roots |
| Trusted scripts | SQLite + any host-provided databases |
| Host application | Full control over what databases are available |

This mirrors PawScript's existing security model:
- `files::` module respects sandbox roots
- GUI commands require host-provided window contexts
- I/O channels can be customized by the host

## Benefits

1. **Portable scripts** - Same PawScript code works against SQLite locally, Postgres in production
2. **Host controls security** - Scripts can only access databases the host explicitly provides
3. **Go's `database/sql`** - Standard interface means any Go SQL driver works automatically
4. **Sandbox-friendly** - SQLite for sandboxed scripts, host DBs for trusted contexts
5. **No network in core** - Network databases are host-provided, keeping core simple

## Example: Development vs Production

### Development (SQLite)

```go
// main.go - development setup
ps := pawscript.New(config)
ps.RegisterSQLiteDatabase("main", "./dev.db")
ps.ExecuteFile("app.paw")
```

### Production (PostgreSQL)

```go
// main.go - production setup
ps := pawscript.New(config)
db, _ := sql.Open("postgres", os.Getenv("DATABASE_URL"))
ps.RegisterSQLDatabase("main", db)
ps.ExecuteFile("app.paw")
```

### The Script (unchanged)

```pawscript
# app.paw - works in both environments
db: {sql_open "main"}

sql_exec ~db, "INSERT INTO logs (message, timestamp) VALUES (?, ?)",
    "Application started", {microtime}

rows: {sql_query ~db, "SELECT * FROM config WHERE active = ?", true}
for ~rows, cfg, (
    echo "Config:", ~cfg.key, "=", ~cfg.value
)

sql_close ~db
```

## Alternative: Simple Key-Value Store

For cases where SQL is overkill, a simpler abstraction could be provided:

```pawscript
# Could layer on existing PSL file format or use BoltDB/BadgerDB
kv: {kv_open "cache"}
kv_set ~kv, "user:123", {list name: "Alice", age: 30}
user: {kv_get ~kv, "user:123"}
kv_delete ~kv, "user:123"
kv_close ~kv
```

This would fit use cases like:
- Session storage
- Simple caching
- Configuration persistence
- User preferences

## What This Design Intentionally Excludes

- **Network database drivers in core** - Conflicts with sandboxing, adds complexity
- **ORMs** - Over-engineering for a scripting language
- **NoSQL servers** (MongoDB, Redis) - Network-dependent, though hosts could provide them
- **Connection string parsing** - Host handles connection setup, scripts just use names

## Implementation Notes

The design leverages Go's `database/sql` interface, which means:

1. Any database with a Go driver works automatically
2. Connection pooling is handled by the driver
3. Prepared statements and parameter binding are standard
4. Transaction support is built-in

For SQLite specifically, `modernc.org/sqlite` provides a pure-Go implementation (no CGO), which simplifies cross-compilation.

## Conclusion

This design adds database connectivity while maintaining PawScript's core philosophy:
- **Minimal core** - Only SQLite built-in
- **Host extensible** - Any SQL database can be provided
- **Sandboxed by default** - Scripts only access what's explicitly allowed
- **Portable** - Same commands work across all backends

This would raise PawScript from 8.5/10 to approximately 9/10 for general-purpose scripting completeness.
