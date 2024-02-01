#!/usr/bin/bash

mkdir -p /sys/fs/cgroup/init
echo 1 > /sys/fs/cgroup/init/cgroup.procs
echo +cpu > /sys/fs/cgroup/cgroup.subtree_control

/usr/local/bin/containerd &
exec /bin/bash
