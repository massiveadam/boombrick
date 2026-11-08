# Cross-toolchain for the Anbernic RG SP (Allwinner H700) under BaseOS.
#
# The recipe is plorpOS's (thwonp/plorpOS, mk/toolchain.Dockerfile, MIT): a
# stock Debian bullseye cross-compiler pinned by digest, apt from a fixed
# snapshot. Bullseye's glibc 2.31 is the floor the binary demands, below
# BaseOS's 2.35. Added here: the arm64 ALSA, Opus and Ogg development files
# Boombrick links against, through Debian multiarch. Go is not in the image;
# the build mounts the host's toolchain.
#
#   docker build -t boombrick-rgsp build/rgsp
FROM debian:bullseye-slim@sha256:f313b4bd62667092a59b3a664d7d3ab8b5e65f41675f48e81455a15dc5abe792

ARG DEBIAN_SNAPSHOT=20260901T000000Z
RUN printf '%s\n' \
        "deb http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT} bullseye main" \
        "deb http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT} bullseye-security main" \
        "deb http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT} bullseye-updates main" \
        > /etc/apt/sources.list \
 && dpkg --add-architecture arm64

RUN apt-get -o Acquire::Check-Valid-Until=false update && apt-get install -y --no-install-recommends \
        gcc-aarch64-linux-gnu g++-aarch64-linux-gnu libc6-dev-arm64-cross binutils-aarch64-linux-gnu \
        make cmake pkg-config file ca-certificates zip \
        libasound2-dev:arm64 libopus-dev:arm64 libogg-dev:arm64 \
    && rm -rf /var/lib/apt/lists/*

ENV CC=aarch64-linux-gnu-gcc
WORKDIR /src
