FROM lfedge/eve-alpine:90ceb11c2370845097d9c3c991fe4652cbfec6cb AS build

ENV PKGS mtools dosfstools
RUN eve-alpine-deploy.sh

COPY make-config /out/
RUN mkdir -p /out/conf/raw

FROM scratch
COPY --from=build /out /

WORKDIR /
ENTRYPOINT [ "/make-config" ]
