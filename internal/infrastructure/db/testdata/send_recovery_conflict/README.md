# Conflict-audit upgrade regressions

Migration 287 was allocated only after fetching main at `3a10faf0624bd598962207d14b0261eb1bfb6fe5` and independently verifying PR939 merged with migration 286. The regressions use the real embedded production SQL, not virtual migration names or manually applied `ALTER TABLE`.

Disposable local databases are initialized through all production migrations to version 284 or 286. Historical fixture rows are inserted before the actual `go run ./cmd/migrate` upgrades to 287. That command has no down action, so the down steps use the same native `openMigrate` runner with `Steps(-1)`. Empty-table down/up restores the old constraints and index. Both historical-only and new automatic evidence refuse down without altering rows, columns, constraints or indexes.

When later migrations are added, the version-specific cases stay pinned to 287 with the native runner; a separate actual CLI startup test always verifies upgrade from 284 to the latest embedded version. Current source ends at 287, so every version-specific up also executes the actual CLI.

Real refusal leaves `schema_migrations` dirty at the down target even though evidence and schema remain untouched. There is intentionally no automatic force/cleanup recovery for that state. Keep old control-plane mutators quiesced and use reviewed roll-forward rather than deleting evidence or blindly forcing a version.

The normal earlier CI `cmd/migrate` invocation initializes the shared live component database. The dedicated send-conflict step explicitly selects these migration regressions and the native authority, repair, ordinary-send and archive regressions.
