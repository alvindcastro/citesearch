// cmd/grpc/main.go
// Parked gRPC scaffold.
// REST in cmd/main.go is the supported runtime API; do not run this for demos or deploys.
package main

import (
	"log"

	"citesearch/config"
	"citesearch/internal/grpcserver"
)

func main() {
	cfg := config.Load()

	port := cfg.GRPCPort
	if port == "" {
		port = "9083"
	}

	s := grpcserver.New(cfg)
	if err := s.ListenAndServe(port); err != nil {
		log.Fatalf("gRPC server: %v", err)
	}
}
