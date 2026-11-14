FROM ubuntu:24.04 AS build
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates git ccache cmake make gcc g++ pkg-config \
    libfftw3-dev libmbedtls-dev libsctp-dev libyaml-cpp-dev libzmq3-dev \
 && rm -rf /var/lib/apt/lists/*
ARG OCUDU_COMMIT
ARG MARCH=x86-64-v3
WORKDIR /src
RUN git init -q . \
 && git remote add origin https://gitlab.com/ocudu/ocudu.git \
 && git fetch -q --depth 1 origin "$OCUDU_COMMIT" \
 && git checkout -q FETCH_HEAD
RUN --mount=type=cache,target=/root/.cache/ccache \
    cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DMARCH="$MARCH" -DBUILD_TESTING=False \
      -DENABLE_UHD=Off -DENABLE_DPDK=Off -DENABLE_ZEROMQ=On \
 && cmake --build build --target gnb -j"$(nproc)" \
 && install -D build/apps/gnb/gnb /opt/ocudu/bin/gnb \
 && rm -rf build

FROM ubuntu:24.04
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    libfftw3-single3 libmbedcrypto7t64 libsctp1 libyaml-cpp0.8 libzmq5 gettext-base iproute2 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /opt/ocudu/bin/gnb /usr/local/bin/gnb
ENTRYPOINT ["gnb"]
