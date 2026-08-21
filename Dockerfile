FROM busybox:musl AS dirs
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev

LABEL org.opencontainers.image.source="https://github.com/wow-look-at-my/buildhost"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.description="Universal package registry server"

# An APE starts through its own shell trampoline: the file is a polyglot whose
# header is a shell script, and the kernel cannot exec it without either a
# binfmt handler or a shell to interpret that header. distroless ships neither,
# so the image carries one static busybox as /bin/sh and every entrypoint goes
# through it, from the same busybox image the /data stage already uses.
COPY --from=dirs /bin/sh /bin/sh
COPY --chmod=755 build/buildhost_cosmo_fat /usr/local/bin/buildhost
COPY --from=dirs --chown=65532:65532 /data /var/lib/buildhost

ENV BUILDHOST_DATA_DIR=/var/lib/buildhost
ENV BUILDHOST_DB_PATH=/var/lib/buildhost/buildhost.db

VOLUME /var/lib/buildhost

# Metadata only -- this publishes nothing on the host. It is what lets
# docker-updater find the port serving /.well-known/docker-updater/ without a
# per-deployment label. Exactly one port may be declared here or discovery has
# to be told which one; the admin port is deliberately left undeclared.
EXPOSE 8080

STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/bin/sh", "/usr/local/bin/buildhost", "healthcheck"]

USER nonroot
ENTRYPOINT ["/bin/sh", "/usr/local/bin/buildhost"]
CMD ["serve"]
