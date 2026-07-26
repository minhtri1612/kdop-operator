package main

import (
	"log"

	"github.com/minhtri1612/kdop-operator/internal/tunnel"
)

func main() {
	srv := tunnel.NewServer(":9000", ":9001", "", []string{"127.0.0.1:80"})
	log.Fatal(srv.Start())
}
