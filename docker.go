package main

import "github.com/spf13/pflag"

var (
	dockerPrefix string

	// may be useless is we move to buildkit?
	dockerArgs []string
)

func init() {
	pflag.StringVar(&dockerPrefix, "docker-prefix", "", "prefix of produced docker images")
	pflag.StringSliceVar(&dockerArgs, "docker-arg", nil, "extra args for docker build")
}
