package main

import "github.com/spf13/pflag"

var dockerPrefix string

func init() {
	pflag.StringVar(&dockerPrefix, "docker-prefix", "", "prefix of produced docker images")
}
