from mcluseau/golang-builder:1.23.2 as build

from docker:27.3.1-cli-alpine3.20
entrypoint ["/bin/gitops-builder"]
run apk add git openssh
run git config --global user.email "builder@localhost" \
  ; git config --global user.name "builder"
copy --from=build /go/bin/ /bin/
