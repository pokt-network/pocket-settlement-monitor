package main

import (
	_ "go.uber.org/automaxprocs"

	"github.com/pokt-network/pocket-settlement-monitor/cmd"
)

func main() {
	cmd.Execute()
}
