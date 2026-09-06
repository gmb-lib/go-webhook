# Contributing

Thank you for considering a contribution. Bug reports, fixes and improvements are welcome. For anything
that could be exploited, use the private route in [SECURITY.md](SECURITY.md) — never a public issue.

For anything larger than a small fix, please open an issue first and describe what you want to change
and why. It protects your time: a change that fights the library's design is better redirected before
it is written than after.

## Building and testing

You need the Go toolchain at the version named in [go.mod](go.mod). The gate a change must pass is the
same one CI runs:

```sh
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Three more checks run in CI and are worth running before you push:

- **Lint** — `golangci-lint run`, at the version pinned in
  [.github/workflows/ci.yml](.github/workflows/ci.yml); the repo's [.golangci.yml](.golangci.yml)
  carries the configuration.
- **Vulnerabilities** — `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
- **Fuzz** — every `Fuzz*` target runs for 30 seconds. This module fuzzes the signature-header parser
  (`FuzzParseSignature`) because that header arrives from outside; a change to the header syntax should
  extend it.

The committed tree must already be tidy: CI runs `go mod tidy -diff` and fails if it would change
anything, so run `go mod tidy` after touching dependencies. All Go code is `gofmt`-formatted, and
`.gitattributes` pins Go files to LF line endings — leave that alone, it keeps the tidy-diff gate stable
across platforms.

## What a change to this library needs

This library fixes what a **receiver** can observe — the headers, the signature scheme, what counts as
delivered, the retry schedule, the at-least-once promise — so that a host can move delivery to another
process without any receiver noticing. That gives it three rules:

- **The receiver-facing contract is append-only.** A new header, a new signature version beside `v1`, a
  new delivery status — fine, if an existing receiver keeps working unchanged. Renaming a header,
  changing what the signature covers, or reclassifying a status code from retry to drop is a breaking
  change and lands only in a major version, with the reason written down.
- **Never weaken verification.** `Verify` must not accept a signature that does not match a held
  secret, must not skip the tolerance window when one is set, and must compare in constant time. A
  change here comes with a test that shows the refusal.
- **Never send unsigned.** The worker refuses to deliver when a subscription has no active secret; a
  change that makes it deliver anyway is a defect, not a feature.

The library depends on the standard library only, and intends to stay that way. A new dependency here
would need an extraordinary reason.

## Proposing a change

- Work on a branch and open a pull request against `develop`. `develop` is merged into `main` and
  tagged there when a release goes out, so `main` is never committed to directly.
- **Sign off every commit.** This project uses the
  [Developer Certificate of Origin](https://developercertificate.org/): by adding a
  `Signed-off-by: Your Name <you@example.org>` line you certify that you wrote the change or otherwise
  have the right to submit it under this project's licence. `git commit -s` adds the line for you; the
  name and address must match the commit author. A pull request whose commits lack it fails the DCO
  check and cannot be merged.
- Keep the change focused: one concern per pull request.
- A change in behaviour comes with a test that fails without it.
- Match the style around you — naming, error handling, comment density. Comments explain what and why
  in plain terms.
- Pull requests also run a dependency review. A new dependency needs a reason the standard library
  cannot cover.

## Releases

A release is a tag on `main`, and for a Go module the tag *is* the publication — the release workflow
re-runs the gate, then checks the tagged version is actually importable from the module proxy before
declaring success. If your change is a breaking one, say so in the pull request: the version it lands
in is decided from that.

## Licence

This project is licensed under the MIT License (see [LICENSE](LICENSE)). By submitting a contribution
you agree that it is provided under the same licence.
