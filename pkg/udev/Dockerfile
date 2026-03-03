# syntax=docker/dockerfile-upstream:1.5.0-rc2-labs

# Copyright (c) 2024 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

FROM lfedge/eve-alpine:12eac5bb74d78ba554b3e2281d63db34375f091f AS build
ENV PKGS="udev kmod"
RUN eve-alpine-deploy.sh

COPY etc/init.d/008-udevd /out/etc/init.d/
COPY etc/udev/rules.d/* /out/etc/udev/rules.d/

# Remove default rules from udev (we don't support many groups from these rules)
RUN rm /out/usr/lib/udev/rules.d/*

RUN find /out
FROM scratch
COPY --from=build /out/bin/udevadm /bin/
COPY --from=build /out/sbin/udevd /sbin/
COPY --from=build /out/usr/lib/udev /lib/udev
COPY --from=build /out/etc/udev /etc/udev
COPY --from=build /out/etc/init.d/*  /etc/init.d/
COPY --from=build /out/usr/lib/libblkid.so.1 /lib/
COPY --from=build /out/usr/lib/libkmod.so.2 /lib/
COPY --from=build /out/usr/lib/libzstd.so.1 /usr/lib/
COPY --from=build /out/usr/lib/liblzma.so.5 /usr/lib/
COPY --from=build /out/usr/lib/libz.so.1 /lib/
COPY --from=build /out/usr/lib/libcrypto.so.3 /usr/lib

ENTRYPOINT []
CMD []
