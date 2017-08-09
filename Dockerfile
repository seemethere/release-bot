FROM golang:1.8.3-alpine as build

RUN apk --update add make

COPY . /go/src/github.com/seemethere/release-bot
WORKDIR /go/src/github.com/seemethere/release-bot
RUN make clean build

ENTRYPOINT ["/go/src/github.com/seemethere/release-bot/build/release-bot"]
