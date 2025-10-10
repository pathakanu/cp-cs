package main

import "embed"

//go:embed web/* web/static/*
var embeddedWebFS embed.FS
