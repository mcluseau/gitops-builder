from mcluseau/golang-builder:1.20.4 as build

from alpine:3.17
entrypoint ["/bin/gitops-builder"]
run apk add git openssh docker-cli
run git config --global user.email "builder@localhost" \
  ; git config --global user.name "builder"
copy --from=build /go/bin/ /bin/
