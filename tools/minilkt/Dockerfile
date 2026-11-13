FROM ubuntu:latest

ARG UID=1
ARG GID=1
ARG USER=ubuntu
ARG HOME=/home/ubuntu

ENV DEBIAN_FRONTEND noninteractive

RUN apt-get update
RUN apt-get -y dist-upgrade

RUN apt-get -y install apt-file
RUN apt-file update

RUN apt-get -y install build-essential
RUN apt-get -y install git
RUN apt-get -y install podman
RUN apt-get -y install libgpgme-dev
RUN apt-get -y install libbtrfs-dev
RUN apt-get -y install tmux
RUN apt-get -y install hx
RUN apt-get -y install htop
RUN apt-get -y install vim
RUN apt-get -y install gopls
RUN apt-get -y install sudo
RUN apt-get -y install golang


RUN userdel $(id -nu $UID) || true
RUN getent group $GID || groupadd -g $GID $USER
RUN useradd $USER -u $UID -g $GID -d $HOME
RUN mkdir -p $HOME
RUN chown $USER $HOME
RUN echo $USER " ALL=(ALL) NOPASSWD: ALL" >> /etc/sudoers

RUN echo $USER:100000:65536 > /etc/subuid
RUN echo $USER:100000:65536 > /etc/subgid

ENV STORAGE_OPTS=overlay.mount_program=/usr/bin/fuse-overlayfs


USER $UID:$GID
WORKDIR $HOME
RUN mkdir -p $HOME/.minilkt
