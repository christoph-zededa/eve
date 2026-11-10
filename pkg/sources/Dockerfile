ARG LFEDGE_EVE_ALPINE_AUTO_TAG
FROM lfedge/eve-alpine:${LFEDGE_EVE_ALPINE_AUTO_TAG} AS tools

COPY collected_sources.tar.gz /var/collected_sources.tar.gz
RUN mkdir -p /var/sources && tar -C /var/sources -xzf /var/collected_sources.tar.gz

FROM scratch
COPY --from=tools /var/sources /
