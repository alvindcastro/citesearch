package upload

// Service is the upload package entry point. Phase U.1 stores dependencies only;
// later phases add upload, chunk, status, list, and delete behavior.
type Service struct {
	deps Dependencies
}

func NewService(deps Dependencies) *Service {
	return &Service{deps: deps}
}

func (s *Service) Dependencies() Dependencies {
	return s.deps
}
