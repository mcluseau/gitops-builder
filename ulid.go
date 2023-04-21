package main

import (
	"crypto/rand"
	"time"

	ulid "github.com/oklog/ulid/v2"
)

var (
	ulidEntropy = ulid.Monotonic(rand.Reader, 0)
)

func newUlid() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), ulidEntropy).String()
}
