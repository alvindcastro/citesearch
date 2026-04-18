// internal/grpcserver/banner.go
// Implements BannerServiceServer (ask, ingest, blob, summarize).
// Uncomment the body after running `buf generate`.
package grpcserver

// import (
// 	"context"
//
// 	"citesearch/config"
// 	"citesearch/internal/azure"
// 	"citesearch/internal/ingest"
// 	"citesearch/internal/rag"
// 	citesearchv1 "citesearch/gen/go/citesearch/v1"
// )
//
// type bannerHandler struct {
// 	citesearchv1.UnimplementedBannerServiceServer
// 	cfg      *config.Config
// 	openai   *azure.OpenAIClient
// 	search   *azure.SearchClient
// 	pipeline *rag.Pipeline
// }
//
// func (h *bannerHandler) Ask(_ context.Context, req *citesearchv1.BannerAskRequest) (*citesearchv1.AskResponse, error) {
// 	topK := int(req.TopK)
// 	if topK == 0 {
// 		topK = h.cfg.TopKDefault
// 	}
// 	resp, err := h.pipeline.Ask(rag.AskRequest{
// 		Question:         req.Question,
// 		TopK:             topK,
// 		VersionFilter:    req.VersionFilter,
// 		ModuleFilter:     req.ModuleFilter,
// 		YearFilter:       req.YearFilter,
// 		SourceTypeFilter: "banner",
// 	})
// 	if err != nil {
// 		return nil, err
// 	}
// 	return toAskResponse(resp), nil
// }
//
// func (h *bannerHandler) Ingest(_ context.Context, req *citesearchv1.BannerIngestRequest) (*citesearchv1.IngestResponse, error) {
// 	docsPath := req.DocsPath
// 	if docsPath == "" {
// 		docsPath = "data/docs/banner"
// 	}
// 	pagesPerBatch := int(req.PagesPerBatch)
// 	if pagesPerBatch == 0 {
// 		pagesPerBatch = 10
// 	}
// 	result, err := ingest.Run(h.cfg, docsPath, req.Overwrite, pagesPerBatch, int(req.StartPage), int(req.EndPage))
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &citesearchv1.IngestResponse{
// 		Status:              result.Status,
// 		DocumentsProcessed:  int32(result.DocumentsProcessed),
// 		ChunksIndexed:       int32(result.ChunksIndexed),
// 		Message:             result.Message,
// 	}, nil
// }
//
// // toAskResponse converts a rag.AskResponse to the proto type.
// func toAskResponse(r *rag.AskResponse) *citesearchv1.AskResponse {
// 	resp := &citesearchv1.AskResponse{
// 		Answer:         r.Answer,
// 		RetrievalCount: int32(r.RetrievalCount),
// 	}
// 	for _, s := range r.Sources {
// 		resp.Sources = append(resp.Sources, &citesearchv1.SourceChunk{
// 			Filename:      s.Filename,
// 			PageNumber:    int32(s.PageNumber),
// 			ChunkText:     s.ChunkText,
// 			Score:         float32(s.Score),
// 			SourceType:    s.SourceType,
// 			SopNumber:     s.SOPNumber,
// 			DocumentTitle: s.DocumentTitle,
// 			BannerModule:  s.BannerModule,
// 			BannerVersion: s.BannerVersion,
// 		})
// 	}
// 	return resp
// }
