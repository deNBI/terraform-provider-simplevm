# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Setup

You need Go (see `go.mod` for the version) and, for the end-to-end tests, Terraform 1.5+ and/or OpenTofu 1.6+.

```bash
git clone https://github.com/deNBI/terraform-provider-simplevm
cd terraform-provider-simplevm
make build test
```

## Workflow

1. Create a branch and make your change.
2. Add or update tests. Tests run against the in-process fake API in `internal/fakeapi`; no credentials are needed.
3. Run `make fmt lint test`.
4. If you changed a schema, a description or an example, run `make docs` and commit the changes in `docs/`. CI fails when `docs/` is stale. Never edit `docs/` by hand; edit the schema descriptions, `examples/` or `templates/` instead.
5. Add a line to the Unreleased section of [CHANGELOG.md](CHANGELOG.md).
6. Open a pull request.

## Rules of thumb

- Every schema attribute and every resource or data source needs a `MarkdownDescription`.
- Anything that changes what a VM or volume is uses `RequiresReplace`, so the plan shows the destruction.
- Request shapes that the SimpleVM OpenAPI schema does not document are marked "assumed from wiki" in code comments; keep that marking.
- Developer documentation lives in `development/`, never in `docs/` (reserved for generated output).

## More

- [Architecture](development/architecture.md), including how to add a resource
- [Testing](development/testing.md)
- [Releasing](development/releasing.md)
