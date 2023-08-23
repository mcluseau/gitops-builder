from mcluseau/golang-builder:1.20.7 as build

from docker:24.0.5-cli-alpine3.18
entrypoint ["/bin/gitops-builder"]
run apk add git openssh
run git config --global user.email "builder@localhost" \
  ; git config --global user.name "builder"
copy --from=build /go/bin/ /bin/
