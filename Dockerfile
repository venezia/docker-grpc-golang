ARG ALPINE_VERSION=3.18
ARG GO_VERSION=1.21.1
ARG GRPC_GATEWAY_VERSION=2.18.0
ARG GRPC_GO_GRPC_VERSION=1.58.1
ARG PROTOC_GEN_GO_VERSION=1.5.3
ARG PROTOC_GEN_GOGO_VERSION=1.3.2
ARG PROTOC_GEN_LINT_VERSION=0.3.0
ARG PROTOC_GEN_DOC_VERSION=1.5.1


FROM quay.io/venezia/golang:${GO_VERSION}-alpine${ALPINE_VERSION} as go_builder
RUN apk add --no-cache build-base curl git
ADD ./third_party ./third_party

ARG GRPC_GO_GRPC_VERSION
RUN mkdir -p ${GOPATH}/src/github.com/grpc/grpc-go && \
    curl -sSL https://api.github.com/repos/grpc/grpc-go/tarball/v${GRPC_GO_GRPC_VERSION} | tar xz --strip 1 -C ${GOPATH}/src/github.com/grpc/grpc-go &&\
    cd ${GOPATH}/src/github.com/grpc/grpc-go/cmd/protoc-gen-go-grpc && \
    go build -ldflags '-w -s' -o /protoc-gen-go-grpc-out/protoc-gen-go-grpc && \
    install -Ds /protoc-gen-go-grpc-out/protoc-gen-go-grpc /out/usr/bin/protoc-gen-go-grpc

ARG PROTOC_GEN_GO_VERSION
RUN mkdir -p ${GOPATH}/src/github.com/golang/protobuf && \
    curl -sSL https://api.github.com/repos/golang/protobuf/tarball/v${PROTOC_GEN_GO_VERSION} | tar xz --strip 1 -C ${GOPATH}/src/github.com/golang/protobuf &&\
    cd ${GOPATH}/src/github.com/golang/protobuf && \
    go build -ldflags '-w -s' -o /golang-protobuf-out/protoc-gen-go ./protoc-gen-go && \
    install -Ds /golang-protobuf-out/protoc-gen-go /out/usr/bin/protoc-gen-go

ARG PROTOC_GEN_GOGO_VERSION
RUN mkdir -p ${GOPATH}/src/github.com/gogo/protobuf && \
    curl -sSL https://api.github.com/repos/gogo/protobuf/tarball/v${PROTOC_GEN_GOGO_VERSION} | tar xz --strip 1 -C ${GOPATH}/src/github.com/gogo/protobuf &&\
    cd ${GOPATH}/src/github.com/gogo/protobuf && \
    go build -ldflags '-w -s' -o /gogo-protobuf-out/protoc-gen-gogo ./protoc-gen-gogo && \
    install -Ds /gogo-protobuf-out/protoc-gen-gogo /out/usr/bin/protoc-gen-gogo && \
    mkdir -p /out/usr/include/github.com/gogo/protobuf/protobuf/google/protobuf && \
    install -D $(find ./protobuf/google/protobuf -name '*.proto') -t /out/usr/include/github.com/gogo/protobuf/protobuf/google/protobuf && \
    install -D ./gogoproto/gogo.proto /out/usr/include/github.com/gogo/protobuf/gogoproto/gogo.proto

ARG PROTOC_GEN_LINT_VERSION
RUN cd / && \
    curl -sSLO https://github.com/ckaznocha/protoc-gen-lint/releases/download/v${PROTOC_GEN_LINT_VERSION}/protoc-gen-lint_linux_amd64.zip && \
    mkdir -p /protoc-gen-lint-out && \
    cd /protoc-gen-lint-out && \
    unzip -q /protoc-gen-lint_linux_amd64.zip && \
    install -Ds /protoc-gen-lint-out/protoc-gen-lint /out/usr/bin/protoc-gen-lint

ARG GRPC_GATEWAY_VERSION
RUN mkdir -p ${GOPATH}/src/github.com/grpc-ecosystem/grpc-gateway && \
    curl -sSL https://api.github.com/repos/grpc-ecosystem/grpc-gateway/tarball/v${GRPC_GATEWAY_VERSION} | tar xz --strip 1 -C ${GOPATH}/src/github.com/grpc-ecosystem/grpc-gateway && \
    cd ${GOPATH}/src/github.com/grpc-ecosystem/grpc-gateway && \
    go build -ldflags '-w -s' -o /grpc-gateway-out/protoc-gen-grpc-gateway ./protoc-gen-grpc-gateway && \
    go build -ldflags '-w -s' -o /grpc-gateway-out/protoc-gen-openapiv2 ./protoc-gen-openapiv2 && \
    install -Ds /grpc-gateway-out/protoc-gen-grpc-gateway /out/usr/bin/protoc-gen-grpc-gateway && \
    install -Ds /grpc-gateway-out/protoc-gen-openapiv2 /out/usr/bin/protoc-gen-openapiv2 && \
    mkdir -p /out/usr/include/protoc-gen-openapiv2/options && \
    install -D $(find ./protoc-gen-openapiv2/options -name '*.proto') -t /out/usr/include/protoc-gen-openapiv2/options && \
    mkdir -p /out/usr/include/google/api && \
    install -D $(find /go/third_party/googleapis/google/api -name '*.proto') -t /out/usr/include/google/api && \
    mkdir -p /out/usr/include/google/rpc && \
    install -D $(find /go/third_party/googleapis/google/rpc -name '*.proto') -t /out/usr/include/google/rpc

ARG PROTOC_GEN_DOC_VERSION
RUN mkdir -p ${GOPATH}/src/github.com/pseudomuto/protoc-gen-doc && \
    curl -sSL https://api.github.com/repos/pseudomuto/protoc-gen-doc/tarball/v${PROTOC_GEN_DOC_VERSION} | tar xz --strip 1 -C ${GOPATH}/src/github.com/pseudomuto/protoc-gen-doc && \
    cd ${GOPATH}/src/github.com/pseudomuto/protoc-gen-doc && \
    go build -ldflags '-w -s' ./cmd/... && \
    install -Ds ./protoc-gen-doc /out/usr/bin/protoc-gen-doc

FROM quay.io/venezia/alpine:${ALPINE_VERSION} as packer
RUN apk add --no-cache curl

# Integrate all output from go_builder
COPY --from=go_builder /out/ /out/

RUN find /out -name "*.a" -delete -or -name "*.la" -delete


FROM quay.io/venezia/alpine:${ALPINE_VERSION}
COPY --from=packer /out/ /
RUN apk add --no-cache bash libstdc++ protoc protobuf-dev
ENTRYPOINT ["/bin/bash"]

