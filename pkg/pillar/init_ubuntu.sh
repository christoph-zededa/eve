#!/usr/bin/bash

sudo mkdir -p /sys/fs/cgroup/init
echo 1 | sudo tee /sys/fs/cgroup/init/cgroup.procs
sudo echo +cpu > /sys/fs/cgroup/cgroup.subtree_control

cd /tmp
sudo nohup /usr/local/bin/containerd &
sleep 5
sudo chmod a+rw /run/containerd/containerd.sock

cd /src

exec /bin/bash
