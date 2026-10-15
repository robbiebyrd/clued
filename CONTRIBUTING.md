# Contributing to clued

Issues and pull requests are welcome. clued (the plugin, the `mind-palace`
binary and Go module, the daemon and the self-hosted clued server) is
licensed under the [Apache License 2.0](LICENSE): what you contribute is
licensed under the same terms (Apache-2.0 section 5, inbound = outbound).
There is no CLA for this repository; instead every commit carries a
Developer Certificate of Origin sign-off.

Report security issues privately to `security@clued.cloud`, never in a
public issue or pull request (see [SECURITY.md](SECURITY.md)). Licensing
questions go to `legal@clued.cloud`.

## Before you open a pull request

Build and test from `mind-palace/` (see the Development section of the
[README](README.md#development)):

```bash
cd mind-palace
test -z "$(gofmt -l .)" && go vet ./... && go build ./...
go test -race ./...
```

Hook and shim changes are covered by `go test ./plugin/` (it runs the
real bash scripts; install `jq`). Update [PROTOCOL.md](PROTOCOL.md) when an
entrypoint changes.

## Sign-off

Every commit in a pull request must be signed off under the Developer
Certificate of Origin (DCO) 1.1 below: a `Signed-off-by:` trailer with
your real name and the e-mail address the commit is authored with. The
`DCO` workflow (`.github/workflows/dco.yml`, `scripts/check-dco.sh`) fails
a pull request that has a commit without it.

```bash
git commit -s -m "Explain the change"      # adds Signed-off-by: Your Name <you@example.com>
git commit --amend -s --no-edit            # sign off the last commit
git rebase --signoff origin/main           # sign off every commit of a branch
```

By signing off you certify the following
(from <https://developercertificate.org/>):

```text
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

## Trademarks

"clued" and its logo are trademarks of Space Hippo, LC; see
[TRADEMARKS.md](TRADEMARKS.md).
