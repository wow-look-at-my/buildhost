FROM busybox:musl AS dirs
RUN mkdir -p /data && chown 65532:65532 /data

# go-toolchain ships one fat APE. A shell turns it into a native ELF on the
# first exec. The final image has no shell, so do that here, and fail if the
# file is still an APE.
FROM busybox:musl AS bin
COPY --chmod=755 build/buildhost_cosmo_fat /buildhost
RUN /buildhost version > /dev/null && \
	{ head -c 4 /buildhost | grep -q ELF || \
	  { echo "buildhost did not assimilate into an ELF; distroless cannot exec an APE"; exit 1; }; }

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
