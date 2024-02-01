#!/usr/bin/bash

sudo mkdir -p /sys/fs/cgroup/init
sudo echo 1 > /sys/fs/cgroup/init/cgroup.procs
sudo echo +cpu > /sys/fs/cgroup/cgroup.subtree_control

sudo nohup /usr/local/bin/containerd &

exec /bin/bash
