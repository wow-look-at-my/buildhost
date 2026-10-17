# APT (Debian / Ubuntu)

buildhost serves each project as its own GPG-signed APT repository at `apt.<domain>/<project>` (suite `stable`, component `main`). Packages are generated on demand from the uploaded binary -- nothing is pre-built.

The fastest way to add a repository is the generated per-project installer. It saves the armored signing key to `/etc/apt/keyrings/`, writes a `signed-by` source, and refreshes the package index (APT reads the armored key directly via `signed-by`, so no `gpg` binary is needed on the client):

```bash
curl -fsSL https://apt.pazer.build/myapp/install.sh | sudo sh
sudo apt-get install myapp
```

For a private project, pass a read token. The installer also records it in `/etc/apt/auth.conf.d/`. That covers the apt host and the static host the `.deb` download redirects to.

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

## Private projects

A private project requires a token on every APT request. Put it in an `apt.conf.d`-style auth file. `apt update` and the package download then both authenticate. The package download redirects to the `static` subdomain. buildhost reads the token from the HTTP Basic **password** field. The username is ignored, so any value works, and `token` is the convention here.

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

This applies to a buildhost-GENERATED deb only, which means `fmt=deb`, from this APT repository. A pre-built `.deb` uploaded as an artifact, with `kind=archive`, is served byte-identical. buildhost never injects anything into an uploaded file.

## Runtime dependencies (apt_depends)

A project declares its runtime prerequisites in Debian relationship syntax, such as `bubblewrap | docker.io`. The generated deb and the Packages entry then carry a `Depends:` line. Set it with `apt_depends` on the `buildhost-publish` or `buildhost-create-release` action, or with `--apt-depends` on `buildhost publish`.

An empty input leaves the stored value untouched. `PATCH /api/v1/projects/{project}` with `{"apt_depends": ""}` clears it. The server refuses a value that is not relationship syntax. That refusal fails the publish.
