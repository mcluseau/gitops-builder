# syntax=docker/dockerfile:1.6.0

from mcluseau/golang-builder:1.24.2 as build

from docker:28.2.2-cli-alpine3.22
entrypoint ["/bin/gitops-builder"]
run apk add git openssh
run git config --global user.email "builder@localhost" \
 && git config --global user.name  "builder"
copy --from=build /go/bin/ /bin/
