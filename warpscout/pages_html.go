package main

import (
	_ "embed"
)

//go:embed public/index.html
var embeddedPagesHTML string
