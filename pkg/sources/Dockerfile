FROM lfedge/eve-alpine:90ceb11c2370845097d9c3c991fe4652cbfec6cb AS tools

COPY collected_sources.tar.gz /var/collected_sources.tar.gz
RUN mkdir -p /var/sources && tar -C /var/sources -xzf /var/collected_sources.tar.gz

FROM scratch
COPY --from=tools /var/sources /
