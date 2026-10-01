# On-demand repackaging

`internal/repackage/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

On-demand repackaging and stripping (tar.gz, tar.xz, tar.zst, zip, deb, brew, npm, oci).

## Everything streams

`Input` carries an `io.Reader` and a `Size`, never a `[]byte`. Each format pipes the artifact through its compressor with `io.Pipe` instead of a buffer. Memory is therefore bounded by the compressor window, not by the artifact size. `Output.Reader` is an `io.ReadCloser`. A streamed format whose compressed length is not known up front returns `Size = SizeUnknown`. The handler then omits Content-Length, and the body is chunk-encoded. `OpenArtifactStream` binds the reader, optionally stripped, to its exact size. A tar, ar or npm header can therefore never disagree with the body. `ChainClose` ties the input stream's lifetime to the output reader, so a lazily-read pipe keeps its source open until the consumer is done.

deb spools only the *compressed* data.tar.gz member to a temp under `Input.TmpDir`, to learn its `ar` length. It also materializes `projects.create_service` for a binary-kind artifact. data.tar gains `/usr/lib/systemd/user/<pkg>.service`. That unit is crash-only, with `Restart=on-failure` and After, PartOf and WantedBy set to graphical-session.target, per systemd.special(7). control.tar gains postinst and prerm maintainer scripts.

`systemctl --global enable` at configure writes pure /etc/systemd/user symlinks. They go active at each user's NEXT graphical login. A best-effort `systemctl --user -M "$SUDO_USER"@ start` covers the live session. prerm runs `--global disable` on remove. Everything is guarded and `||`-true, so a host with no systemctl never leaves the package unconfigured.

A flag-off deb is byte-identical, because both members are pinned single-entry. A non-binary kind never materializes the unit. An uploaded `kind=archive` deb is passthrough. Nothing is ever injected into it. The `apt-install-e2e` test asserts the unit and the enable symlink on install. It asserts their absence with the flag off. The `oci` format streams the layer into `Store.Put`. It tees the uncompressed tar through sha256 for the diffID.

## APE packaging

A Cosmopolitan APE rewrites its own file on first run. dpkg installs a binary root-owned at `0755`. `apt install <pkg> && <pkg>` therefore died with "cannot create /usr/bin/<pkg>: Permission denied" for every non-root user. `peekAPE` detects the `MZqFpD='` prologue by byte comparison, never by an execution of the artifact. Such a package installs the binary at `/usr/lib/<pkg>/<pkg>`, plus a generated `/usr/bin/<pkg>` launcher (`debAPELauncher`). The launcher maintains a per-user writable copy under `$XDG_CACHE_HOME/buildhost/<pkg>/<version>/`. A rename publishes it, so concurrent first runs cannot see a partial file. It falls back to an exec of the installed path when it cannot make a copy. A run of the packaged binary from a maintainer script, to pre-assimilate it, is deliberately NOT done. A registry must never execute a publisher's binary as root on the installing machine. A non-APE package is byte-identical to before.

One more fix landed here, because it blocked the above. `data.tar` carried no DIRECTORY entries. dpkg therefore failed to unpack anything installed outside `/usr/bin`. It reported "unable to create ... No such file or directory". Every `library`-kind and `assets`-kind deb buildhost ever produced was uninstallable. `tarEntry.Dir` now emits the install directory ahead of its files.

## Registration and embedded inputs

Self-registering through init(). The Generator uses the registry. The Orchestrator only publishes releases. `cacerts/ca-certificates.crt` is a public CA bundle baked into the synthesized OCI essentials layer with `//go:embed`. It is **gitignored** and **fetched at build time** by a `//go:generate` directive beside that embed (`scripts/fetch-cacerts.sh`). The repo therefore carries no unreviewable cert blob. It is produced like every other generated input, not by a step each caller must remember. CI's `build` job has no fetch step, because a broken directive must fail there and not only for a human. A build that skips generate fails on the missing-embed error. See `cacerts/README.md`.
