# Copyright (c) 2023 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

FROM lfedge/eve-alpine:77953f8d7b916b99325ceee3183b108163392ed6 AS build
ENV PKGS="alpine-baselayout musl-utils bash glib squashfs-tools util-linux e2fsprogs e2fsprogs-extra keyutils dosfstools coreutils sgdisk smartmontools"
RUN eve-alpine-deploy.sh

FROM scratch
COPY --from=build /out/ /
COPY storage-init.sh /

WORKDIR /
ENTRYPOINT []
CMD ["/storage-init.sh"]
