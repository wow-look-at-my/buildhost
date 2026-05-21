FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev

LABEL org.opencontainers.image.source="https://github.com/wow-look-at-my/buildhost"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.description="Universal package registry server"

COPY --chmod=755 build/buildhost_linux_amd64 /usr/local/bin/buildhost

ENV BUILDHOST_DATA_DIR=/var/lib/buildhost
ENV BUILDHOST_DB_PATH=/var/lib/buildhost/buildhost.db

VOLUME /var/lib/buildhost

STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/buildhost", "healthcheck"]

USER nonroot
ENTRYPOINT ["buildhost"]
CMD ["serve"]
