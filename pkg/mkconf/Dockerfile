ARG LFEDGE_EVE_ALPINE_AUTO_TAG
FROM lfedge/eve-alpine:${LFEDGE_EVE_ALPINE_AUTO_TAG} AS build

ENV PKGS mtools dosfstools
RUN eve-alpine-deploy.sh

COPY make-config /out/
RUN mkdir -p /out/conf/raw

FROM scratch
COPY --from=build /out /

WORKDIR /
ENTRYPOINT [ "/make-config" ]
