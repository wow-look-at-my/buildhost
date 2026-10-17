# Publishing real Docker images

A project can need a real prebuilt image, rather than a binary wrapped in a minimal layer. A custom base image, a native library, an entrypoint and an exposed port are the usual reasons. buildhost is a writable OCI registry. You can therefore `docker push` directly.

The OCI registry is served on the `oci.` subdomain. The apex host serves the API, and not `/v2/`.

```bash
docker login oci.builds.example.com -u oidc -p "$TOKEN"   # any username; password is a write-scoped token
docker buildx build --push -t oci.builds.example.com/myproject:v1.2.3 .
docker pull oci.builds.example.com/myproject:v1.2.3
```

A release that contains a pushed image is a **docker build**. The OCI `/v2` endpoint is the only place it is served. The apt, brew, npm and raw-download endpoints do not apply to it, because it is just a container image. A pushed image layer is content-addressed and deduplicated. An unchanged layer is therefore not re-uploaded on a later push. `BUILDHOST_MAX_BLOB_SIZE` caps the per-blob size, and it defaults to 10 GiB.

A proxy in front of the server may cap a request body. Cloudflare's edge answers 413 to a body near 100 MB. `docker push` then fails on a big layer, because docker and buildx send each layer as one request. Push through the CLI instead. It uploads a blob in chunks sized under the server's advertised limit, so any layer size goes through.

```bash
docker buildx build --output type=oci,dest=image.tar -t oci.builds.example.com/myproject:v1.2.3 .
buildhost docker-push --token "$TOKEN" --image image.tar oci.builds.example.com/myproject:v1.2.3
```

## From GitHub Actions

Use the `buildhost-publish-docker` action to build and push in one step. It authenticates with a GHA OIDC token, so there is no static secret. The project auto-provisions on the first push.

```yaml
permissions:
  id-token: write   # required to mint the OIDC token
  contents: read
  deployments: write   # optional, additive: register the publish as a GitHub Deployment
steps:
  - uses: actions/checkout@v4
  - uses: wow-look-at-my/buildhost/.github/actions/buildhost-publish-docker@master
    with:
      server: https://builds.example.com   # optional, defaults to https://pazer.build
      context: .                            # optional
```

With `tags` omitted, a push is tagged with the commit SHA and the sanitized branch name, so `claude/foo` becomes `claude-foo`. `latest` is added only on the default branch. A feature branch therefore never moves the `:latest` pointer.

Pass `tags`, newline-separated, to override that. A bare tag expands to `<registry>/<project>:<tag>`. A reference that contains `/` or `:` is used as-is. You can therefore also push to another registry you are logged in to.

To fetch an artifact back in a workflow, use `buildhost-download`. It resolves the same way the URL does, and it defaults to the runner's own platform.

```yaml
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-download@master
  id: cli
  with:
    project: buildhost      # optional: version, branch, os, arch, format, token
```

It outputs `path`. With `required: 'false'` a missing artifact sets `downloaded: 'false'` instead of a failure. A caller can therefore fall back.

For a build you drive yourself, use `buildhost-docker-push`. It takes an OCI layout you already produced, and pushes it in chunks. A layer over the proxy's body cap therefore still goes through. To obtain a CLI that can do that is the action's problem, and not yours.

```yaml
- run: docker buildx build --output type=oci,tar=false,dest=layout .
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-docker-push@master
  with:
    image: layout
    refs: oci.pazer.build/myproject:v1.2.3
```

A publish logs docker in for you, and so does a pull. To fetch a published image, into a nested daemon or onto the runner itself, use `buildhost-docker-pull`. It authenticates itself, so no workflow handles a registry credential.

```yaml
- uses: wow-look-at-my/buildhost/.github/actions/buildhost-docker-pull@master
  with:
    images: oci.pazer.build/myproject:v1.2.3
```

Those actions are `buildhost-publish-docker`, `buildhost-docker-push` and `buildhost-docker-pull`. They are the only supported way to authenticate a CI Docker workflow to buildhost. There is no login-only action, and no CLI equivalent.
