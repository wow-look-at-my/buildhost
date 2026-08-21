FROM busybox:musl AS dirs
RUN mkdir -p /data && chown 65532:65532 /data

# The image runs the linux ELF beside the fat APE, not the APE. The APE is a
# PE that only a shell can start, and the final image has no shell. Refuse a
# file that is not an ELF, because that failure appears at run time otherwise.
FROM busybox:musl AS bin
COPY --chmod=755 build/buildhost_cosmo_fat.dbg /buildhost
RUN head -c 4 /buildhost | grep -q ELF || \
	{ echo "build/buildhost_cosmo_fat.dbg is not an ELF; distroless cannot exec it"; exit 1; }
RUN /buildhost version > /dev/null

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev

LABEL org.opencontainers.image.source="https://github.com/wow-look-at-my/buildhost"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.description="Universal package registry server"

COPY --from=bin --chmod=755 /buildhost /usr/local/bin/buildhost
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
    CMD ["/usr/local/bin/buildhost", "healthcheck"]

USER nonroot
ENTRYPOINT ["buildhost"]
CMD ["serve"]
