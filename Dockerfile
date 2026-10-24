# webframe in a container: Alpine with WebKitGTK and Xvfb, which webframe
# starts itself since there is no display.
#
#   docker build -t webframe .
#   docker run --rm webframe https://example.com
#   docker run --rm -i webframe -c about:blank
#   docker run --rm -v "$PWD:/work" webframe page.html

# Built with cgo against musl, since purego without cgo needs glibc.
FROM golang:1.27-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /webframe ./cmd/webframe

FROM alpine:3.23
# mesa-dri-gallium gives GTK a software GL driver (llvmpipe) under Xvfb.
RUN apk add --no-cache webkit2gtk-4.1 xvfb font-dejavu mesa-dri-gallium \
	&& adduser -D -h /home/webframe webframe \
	&& mkdir /work && chown webframe /work
COPY --from=build /webframe /usr/local/bin/webframe
USER webframe
WORKDIR /work
ENTRYPOINT ["webframe"]
