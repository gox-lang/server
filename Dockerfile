# syntax=docker/dockerfile:1
FROM ubuntu:24.04 AS download
ARG GOX_VERSION=0.1.3
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY docker/download-release.sh /download-release.sh
RUN sh /download-release.sh "$GOX_VERSION" "$TARGETARCH" /out

FROM ubuntu:24.04
ARG GOX_VERSION=0.1.3
LABEL org.opencontainers.image.source="https://github.com/gox-lang/server" \
      org.opencontainers.image.title="GOX Server" \
      org.opencontainers.image.description="GOX development environment using verified GitHub release binaries" \
      org.opencontainers.image.version="$GOX_VERSION" \
      org.opencontainers.image.licenses="MIT"
# GCC and development headers are needed for Go plugins. GOX provisions Go/Node itself.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates build-essential git curl xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && usermod --login gox --home /home/gox --move-home ubuntu \
    && groupmod --new-name gox ubuntu \
    && mkdir -p /workspace /var/lib/gox \
    && chown gox:gox /workspace /var/lib/gox
COPY --from=download /out/gox /usr/local/bin/gox
COPY --from=download /out/LICENSE /usr/share/doc/gox/LICENSE
COPY docker/entrypoint.sh /usr/local/bin/gox-container
ENV GOX_HOME=/var/lib/gox HOME=/home/gox GOX_LSP_AUTO_UPDATE=0
USER gox
WORKDIR /workspace
EXPOSE 3000
ENTRYPOINT ["sh", "/usr/local/bin/gox-container"]
CMD ["--help"]
