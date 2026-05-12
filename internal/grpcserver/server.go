// internal/grpcserver/server.go
// Parked gRPC scaffold. REST is the supported runtime API.
package grpcserver

import (
	"fmt"
	"log"
	"net"

	"citesearch/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	// Historical proto registration, kept only if gRPC is intentionally revived:
	// citesearchv1 "citesearch/gen/go/citesearch/v1"
)

// Server wraps the parked gRPC server scaffold.
type Server struct {
	cfg    *config.Config
	server *grpc.Server
}

// New creates the reflection-only scaffold. Supported behavior lives in REST handlers.
func New(cfg *config.Config) *Server {
	s := grpc.NewServer()

	// Reflection is left for scaffold introspection, not as an advertised API surface.
	reflection.Register(s)

	// Intentionally inactive. Prefer extending REST unless a future requirement needs gRPC.
	// citesearchv1.RegisterSystemServiceServer(s, &systemHandler{cfg: cfg})
	// citesearchv1.RegisterBannerServiceServer(s, &bannerHandler{cfg: cfg})
	// citesearchv1.RegisterSOPServiceServer(s, &sopHandler{cfg: cfg})

	return &Server{cfg: cfg, server: s}
}

// ListenAndServe starts the parked listener only for explicit scaffold experiments.
func (s *Server) ListenAndServe(port string) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}
	log.Printf("parked gRPC scaffold listening on :%s; REST remains the supported API", port)
	return s.server.Serve(lis)
}

// GracefulStop drains in-flight RPCs then stops.
func (s *Server) GracefulStop() {
	s.server.GracefulStop()
}
