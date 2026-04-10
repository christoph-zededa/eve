FROM lfedge/eve-alpine:1519fb51bba3787fcfe53efe2d7ddd34e7d78f59 AS tools

COPY collected_sources.tar.gz /var/collected_sources.tar.gz
RUN mkdir -p /var/sources && tar -C /var/sources -xzf /var/collected_sources.tar.gz

FROM scratch
COPY --from=tools /var/sources /
