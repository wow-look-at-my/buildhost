# On-demand repackaging

`internal/repackage/`. Extracted verbatim from CLAUDE.md. Paragraph breaks were added at the existing topic boundaries, no wording changed.

On-demand repackaging and stripping (tar.gz, tar.xz, tar.zst, zip, deb, brew, npm, oci).

## Everything streams

`Input` carries an `io.Reader`+`Size` (not a `[]byte`) and each format pipes the artifact through its compressor via `io.Pipe` rather than buffering it. `Output.Reader` is an `io.ReadCloser`. A streamed format whose compressed length is not known up front returns `Size = SizeUnknown` (the handler then omits Content-Length and the body is chunk-encoded). `OpenArtifactStream` binds the (optionally stripped) reader to its exact size -- so a tar/ar/npm header can never disagree with the body -- and `ChainClose` ties.

deb spools only the *compressed* data.tar.gz member to a temp under `Input.TmpDir` to learn its `ar` length -- and materializes `projects.create_service` for binary-kind. Prerm `--global disable` on remove. Everything guarded + ||-true so a systemctl-less host never leaves the package unconfigured). Flag-off debs are byte-identical (both members pinned single-entry), non-binary kinds never materialize, and uploaded `kind=archive` debs are passthrough -- never injected into (apt-install-e2e asserts the unit + enable symlink on install and their absence flag-off). Oci streams the layer into `Store.Put` while teeing the uncompressed tar through sha256 for the diffID.

## APE packaging

A Cosmopolitan APE rewrites its own file on first run, and dpkg installs binaries root-owned `0755`, so `apt install <pkg> && <pkg>` died with "cannot create. `peekAPE` detects the `MZqFpD='` prologue by byte comparison (never by executing the artifact) and such a package installs the binary at `/usr/lib/<pkg>/<pkg>` plus a generated `/usr/bin/<pkg>`. Running the packaged binary from a maintainer script to pre-assimilate it is deliberately NOT done -- a registry must never execute a publisher's. Non-APE packages are byte-identical to before.

Also fixed here because it blocked the above: `data.tar` carried no DIRECTORY entries, so dpkg can not unpack anything installed outside `/usr/bin`. No such file or directory") -- every `library`- and `assets`-kind deb buildhost ever produced was uninstallable. `tarEntry.Dir` now emits the install directory ahead of its files.

## Registration and embedded inputs

Self-registering via init(). Generator uses registry. Orchestrator just publishes releases. `cacerts/ca-certificates.crt` is a public CA bundle baked into the synthesized OCI essentials layer via `//go:embed`. It is **gitignored** and **fetched at build time** by a `//go:generate` directive beside that embed (`scripts/fetch-cacerts.sh`) -- so the repo carries no unreviewable. And it is produced like every other generated input rather than by a step each caller must remember (CI's `build` job has no fetch step: a broken directive must fail there, not just for humans). A build that skips generate fails on the missing-embed error. See `cacerts/README.md`.
