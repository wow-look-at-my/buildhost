# APT (Debian / Ubuntu)

buildhost serves each project as its own GPG-signed APT repository at `apt.<domain>/<project>` (suite `stable`, component `main`). Packages are generated on demand from the uploaded binary -- nothing is pre-built.

The fastest way to add a repository is the generated per-project installer. It saves the armored signing key to `/etc/apt/keyrings/`, writes a `signed-by` source, and refreshes the package index (APT reads the armored key directly via `signed-by`, so no `gpg` binary is needed on the client):

```bash
curl -fsSL https://apt.pazer.build/myapp/install.sh | sudo sh
sudo apt-get install myapp
```

For a private project, pass a read token. The installer then adds the one authenticated source, `apt.<domain>/private`, instead of the project's own repository. It records the token in `/etc/apt/auth.conf.d/buildhost-private.conf`. See "The authenticated source" below.

```bash
curl -fsSL -H "Authorization: Bearer $TOKEN" https://apt.pazer.build/myapp/install.sh \
  | sudo BUILDHOST_TOKEN=$TOKEN sh
```

One-line install commands (and per-project copy buttons) are also available on the admin dashboard: see each project's page or the **Registries** tab.

A self-modifying binary is packaged with a launcher. A Cosmopolitan APE is such a binary, because it rewrites its own file the first time it runs. The binary installs under `/usr/lib/<pkg>/`. `/usr/bin/<pkg>` keeps a writable per-user copy. An ordinary user can therefore run it. Everything else installs straight to `/usr/bin`.

To set it up by hand instead, import the repository signing key once, add the source, then install. The key is served per project path. It is the same server-wide key on every path.

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://apt.pazer.build/myapp/key.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] https://apt.pazer.build/myapp stable main" \
  | sudo tee /etc/apt/sources.list.d/myapp.list
sudo apt update && sudo apt install myapp
```

## The authenticated source

`apt.<domain>/private` is ONE repository scoped to the credential. It lists every public project, plus each private project the token can read. One source line and one auth entry therefore cover a package and all of its private dependencies. It is the apt counterpart of the authenticated brew tap. An anonymous request gets a 401 with a Basic challenge, because apt sends the credential from `auth.conf` only after one. A private deb redirects to a signed download link.

The apt host is the only one that needs the token.

```bash
sudo install -d -m 0755 /etc/apt/keyrings /etc/apt/auth.conf.d
curl -fsSL -u "token:$TOKEN" https://apt.pazer.build/private/key.asc \
  | sudo tee /etc/apt/keyrings/buildhost-private.asc >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/buildhost-private.asc] https://apt.pazer.build/private stable main" \
  | sudo tee /etc/apt/sources.list.d/buildhost-private.list
printf 'machine https://apt.pazer.build/private\nlogin token\npassword %s\n' "$TOKEN" \
  | sudo tee /etc/apt/auth.conf.d/buildhost-private.conf >/dev/null
sudo chmod 600 /etc/apt/auth.conf.d/buildhost-private.conf
sudo apt update && sudo apt install myapp
```

Keep the scheme in the `machine` line: apt applies a line with no scheme to https only. A project named `private` has no per-project repository, because this path takes it.

## Private projects

A private project requires a token on every APT request. Put it in an `apt.conf.d`-style auth file. `apt update` and the package download then both authenticate. The package download redirects to the `static` subdomain. buildhost reads the token from the field of the HTTP Basic **password. The username is ignored, so any value works, and `token` is the convention here.

```bash
sudo install -d -m 0755 /etc/apt/keyrings
# key.asc is itself gated for a private project, so authenticate the key fetch too
curl -fsSL -u "token:$TOKEN" https://apt.pazer.build/myapp/key.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/buildhost.gpg
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] https://apt.pazer.build/myapp stable main" \
  | sudo tee /etc/apt/sources.list.d/myapp.list
cat <<EOF | sudo tee /etc/apt/auth.conf.d/buildhost.conf >/dev/null
machine apt.pazer.build login token password $TOKEN
machine static.pazer.build login token password $TOKEN
EOF
sudo chmod 600 /etc/apt/auth.conf.d/buildhost.conf
sudo apt update && sudo apt install myapp
```

## Slash-namespaced projects

A Debian package name cannot contain `/` or `_`. A slash-namespaced project therefore folds those characters to `-` in its package name. Project `pr-reviewer-agent/server` is served at `apt.pazer.build/pr-reviewer-agent/server`. The slash stays in the repository URL. It installs as the package **`pr-reviewer-agent-server`**. The binary lands at `/usr/bin/pr-reviewer-agent-server`.

```bash
echo "deb [signed-by=/etc/apt/keyrings/buildhost.gpg] https://apt.pazer.build/pr-reviewer-agent/server stable main" \
  | sudo tee /etc/apt/sources.list.d/pr-reviewer-agent-server.list
sudo apt update && sudo apt install pr-reviewer-agent-server
```

## Background services (create_service)

A `create_service` project's generated deb ships a systemd user unit at `/usr/lib/systemd/user/<pkg>.service`. See the Homebrew section for the flag itself. That unit is crash-only `Restart=on-failure`. It is bound to `graphical-session.target`.

The package sets it up at install. Its postinst runs `systemctl --global enable`. The service therefore starts at every user's next graphical login. The postinst also makes a best-effort immediate start, for the installing sudo user's live session. A removal of the package disables it again.

This applies to a buildhost-GENERATED deb only, which means `fmt=deb`, from this APT repository. A pre-built `.deb` uploaded as an artifact, with `kind=archive`, is served byte-same. buildhost never injects anything into an uploaded file.

## Runtime dependencies (apt_depends)

A project declares its runtime prerequisites in Debian relationship syntax, such as `bubblewrap | docker.io`. The generated deb and the Packages entry then carry a `Depends:` line. Set it with `apt_depends` on the `buildhost-publish` or `buildhost-create-release` action, or with `--apt-depends` on `buildhost publish`.

An empty input leaves the stored value untouched. `PATCH /api/v1/projects/{project}` with `{"apt_depends": ""}` clears it. The server refuses a value that is not relationship syntax. That refusal fails the publish.

## Dependencies on other buildhost projects (depends)

A project can also name the buildhost projects it depends on, such as `ascii-timeline`. Set it with `depends` on the `buildhost-publish` or `buildhost-create-release` action, with `--depends` on `buildhost publish`, or with go-toolchain's `depends` input. The `Depends:` line then names each one by its deb package name, ahead of `apt_depends`.

apt resolves a Depends only from a repository it configured, and each project is its own repository. Without a token, the generated `install.sh` therefore also adds the repository of every dependency, and of their dependencies in turn. That token is with a keyring and a source list for each one. With a token it adds the one authenticated source, which already lists them all. `apt-get install api-cli` then installs `ascii-timeline` too:

```bash
curl -fsSL https://apt.pazer.build/api-cli/install.sh | sudo sh
sudo apt-get install api-cli
```

A private dependency needs a read token, even under a public project: `curl -fsSL https://apt.pazer.build/api-cli/install.sh | sudo BUILDHOST_TOKEN=$TOKEN sh`. The installer stops with an error that names the dependency when `BUILDHOST_TOKEN` is unset. The server refuses a name it cannot resolve, or one the publishing token cannot read, as an unknown project, and the publish fails. It also refuses a project that depends on itself, directly or through a cycle. An absent input leaves the stored list untouched. `PATCH /api/v1/projects/{project}` with `{"depends": []}` clears it.

A rename of a dependency rewrites every list that names it.
