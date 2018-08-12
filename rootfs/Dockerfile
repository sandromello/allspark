FROM golang:1.10 as frpBuild

ARG RELEASE=v0.20.0
RUN git clone -b ${RELEASE} --single-branch --depth 1 https://github.com/fatedier/frp.git /go/src/github.com/fatedier/frp
WORKDIR /go/src/github.com/fatedier/frp
ENV CGO_ENABLED=0
RUN make

FROM alpine:3.6

RUN mkdir -p /etc/frpc
RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*
COPY --from=frpBuild /go/src/github.com/fatedier/frp/bin/frpc /usr/local/bin
COPY --from=frpBuild /go/src/github.com/fatedier/frp/bin/frps /usr/local/bin
COPY . /

EXPOSE 80 443 6000 7000 7400 3500
WORKDIR /
