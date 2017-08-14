FROM golang:1.8.3-alpine as build

RUN apk --update add make

COPY . /go/src/github.com/seemethere/release-bot
WORKDIR /go/src/github.com/seemethere/release-bot
RUN make clean build

FROM alpine:latest
RUN apk --update add ca-certificates
COPY --from=build /go/src/github.com/seemethere/release-bot/build/release-bot /release-bot
ENTRYPOINT ["/release-bot"]
