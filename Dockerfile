# syntax=docker/dockerfile:1.6.0

from mcluseau/golang-builder:1.26.1 as build
run strip /go/bin/*

from docker:29.3.0-cli-alpine3.23
entrypoint ["/bin/gitops-builder"]
run apk add git openssh
run git config --global user.email "builder@localhost" \
 && git config --global user.name  "builder"
copy --from=build /go/bin/ /bin/
