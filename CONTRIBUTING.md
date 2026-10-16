# Contributing to TableF

Thanks for helping! Bug reports, fixes and new engines are all welcome.

## Before you open a pull request

```bash
gofmt -l .                       # must print nothing
go vet ./...
go test ./...
cd frontend
pnpm format:check
pnpm check
pnpm test
```

CI runs the same commands.

## Code style

- Keep changes small and focused; match the style of the surrounding code.
- Comments explain *why*, not *what*. Exported Go identifiers get doc comments.
- Never log or return secrets (passwords, tokens, keys) in errors or messages.
- Mind the cost of hot paths: the grid renders thousands of rows, so avoid
  per-cell lookups that scan arrays (`indexOf`, `find`) — precompute a `Map`.

## Adding a database engine

1. Create `internal/engines/<name>/` and register from `init()`:
   - SQL engines: implement `driver.Driver` and call `driver.Register`.
     Implement the optional hooks that apply (`Featurer`, `Paginator`,
     `Versioner`, `CurrentDatabaser`, `EmptyInserter`) and report honest
     `Features` — the UI hides what an engine can't do.
   - Non-SQL engines: implement `driver.Store` / `driver.StoreConn` and call
     `driver.RegisterStore`.
2. Import the package in `internal/engines/engines.go` and add its id to the
   list in `internal/engines/engines_test.go`.
3. Add it to `frontend/src/lib/engines.ts` (label, logo, port and form fields).
4. Write tests that need no live server (DSN building, paging, value
   conversion). Use fakes (`httptest`, in-memory servers) where you can.

## Reporting bugs

Include the database engine and version, what you did, what you expected and
what happened. Remove credentials from any connection strings you paste.
