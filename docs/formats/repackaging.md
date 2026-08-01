# On-demand repackaging

`internal/repackage/`. Extracted verbatim from CLAUDE.md; paragraph breaks were
added at the existing topic boundaries, no wording changed.

On-demand repackaging and stripping (tar.gz, tar.xz, tar.zst, zip, deb, brew,
npm, oci).

## Everything streams

`Input` carries an `io.Reader`+`Size` (not a `[]byte`) and each format pipes the
artifact through its compressor via `io.Pipe` rather than buffering it, so memory
is bounded by the compressor window, not the artifact size. `Output.Reader` is an
`io.ReadCloser`; a streamed format whose compressed length isn't known up front
returns `Size = SizeUnknown` (the handler then omits Content-Length and the body
is chunk-encoded). `OpenArtifactStream` binds the (optionally stripped) reader to
its exact size -- so a tar/ar/npm header can never disagree with the body -- and
`ChainClose` ties the input stream's lifetime to the output reader (a lazily-read
pipe keeps its source open until the consumer is done).

deb spools only the *compressed* data.tar.gz member to a temp under
`Input.TmpDir` to learn its `ar` length -- and materializes
`projects.create_service` for binary-kind artifacts: data.tar gains
`/usr/lib/systemd/user/<pkg>.service` (crash-only `Restart=on-failure`,
After/PartOf/WantedBy=graphical-session.target per systemd.special(7)) and
control.tar gains postinst/prerm maintainer scripts (`systemctl --global enable`
at configure = pure /etc/systemd/user symlinks, active at each user's NEXT
graphical login, plus a best-effort `systemctl --user -M "$SUDO_USER"@ start` for
the live session; prerm `--global disable` on remove; everything guarded + ||-true
so a systemctl-less host never leaves the package unconfigured); flag-off debs are
byte-identical (both members pinned single-entry), non-binary kinds never
materialize, and uploaded `kind=archive` debs are passthrough -- never injected
into (apt-install-e2e asserts the unit + enable symlink on install and their
absence flag-off); oci streams the layer into `Store.Put` while teeing the
uncompressed tar through sha256 for the diffID.

## APE packaging

A Cosmopolitan APE rewrites its own file on first run, and dpkg installs binaries
root-owned `0755`, so `apt install <pkg> && <pkg>` died with "cannot create
/usr/bin/<pkg>: Permission denied" for every non-root user. `peekAPE` detects the
`MZqFpD='` prologue by byte comparison (never by executing the artifact) and such
a package installs the binary at `/usr/lib/<pkg>/<pkg>` plus a generated
`/usr/bin/<pkg>` launcher (`debAPELauncher`) that maintains a per-user writable
copy under `$XDG_CACHE_HOME/buildhost/<pkg>/<version>/`, published by rename so
concurrent first runs cannot see a partial file, falling back to exec'ing the
installed path if no copy can be made. Running the packaged binary from a
maintainer script to pre-assimilate it is deliberately NOT done -- a registry must
never execute a publisher's binary as root on the installing machine. Non-APE
packages are byte-identical to before.

Also fixed here because it blocked the above: `data.tar` carried no DIRECTORY
entries, so dpkg could not unpack anything installed outside `/usr/bin` ("unable
to create ... No such file or directory") -- every `library`- and `assets`-kind
deb buildhost ever produced was uninstallable; `tarEntry.Dir` now emits the
install directory ahead of its files.

## Registration and embedded inputs

Self-registering via init(); Generator uses registry. Orchestrator just publishes
releases. `cacerts/ca-certificates.crt` is a public CA bundle baked into the
synthesized OCI essentials layer via `//go:embed`. It is **gitignored** and
**fetched at build time** by a `//go:generate` directive beside that embed
(`scripts/fetch-cacerts.sh`) -- so the repo carries no unreviewable cert blob, and
it is produced like every other generated input rather than by a step each caller
must remember (CI's `build` job has no fetch step: a broken directive must fail
there, not just for humans). A build that skips generate fails on the
missing-embed error; see `cacerts/README.md`.
