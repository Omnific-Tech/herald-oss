# Contributing

## Running the tests

```sh
docker compose -f compose.test.yaml up -d --wait postgres
HERALD_TEST_DATABASE_URL="postgres://herald:herald@localhost:5433/herald?sslmode=disable" make check
```

`make check` runs `go vet`, the tests, and a build. The store, server, and Web
Push suites need a database and **skip** without `HERALD_TEST_DATABASE_URL`, so
a green `make check` without it has not exercised most of the code. CI always
sets it. Public CI verifies HTTP/database scenarios using `make e2e-admin` and
`scripts/test-delivery-errors-e2e.sh`, retaining JSON reports on every run.

Each suite creates its own `herald_test_<suite>` database, so packages run
concurrently.

`make e2e-admin` runs the admin HTTP/database and routing scenarios and writes their Go test
event stream to `artifacts/e2e/admin-api.json` for repeatable inspection.

## Conventions

- Conventional commit subjects: `feat:`, `fix:`, `chore:`, `docs:`, `test:`,
  `refactor:`. Keep the subject short; add a body only for something the diff
  cannot show, such as the root cause of a bug.
- Branches: `feat/`, `fix/`, `chore/`, `docs/`.
- `gofmt` is not optional. `make format`.
- Comments explain why, not what. A comment that restates the next line is
  noise; one that records a non-obvious constraint earns its place.

## Changing the HTTP surface

`docs/protocol-v1.md` and `docs/admin-api.md` are contracts, not descriptions.
Update them in the same commit as the code, and check `docs/versioning.md`
before removing or narrowing anything. `TestDocumentedRoutesMatchTheRouter`
fails if the route table and documents drift apart.

## Changing the schema

Add a numbered file in `migrations/`. Never edit an applied one: `heraldctl
migrate` records what it has run and will not re-run it. There are no down
migrations.
