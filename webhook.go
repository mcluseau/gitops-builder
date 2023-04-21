package main

import "os"

var (
	webhookSecret string
)

func init() {
	webhookSecret = os.Getenv("WEBHOOK_SECRET")
}
