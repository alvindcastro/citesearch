// internal/grpcserver/sop.go
// Implements SOPServiceServer (ask, ingest, list).
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
// type sopHandler struct {
// 	citesearchv1.UnimplementedSOPServiceServer
// 	cfg      *config.Config
// 	search   *azure.SearchClient
// 	pipeline *rag.Pipeline
// }
//
// func (h *sopHandler) Ask(_ context.Context, req *citesearchv1.SOPAskRequest) (*citesearchv1.AskResponse, error) {
// 	topK := int(req.TopK)
// 	if topK == 0 {
// 		topK = h.cfg.TopKDefault
// 	}
// 	resp, err := h.pipeline.Ask(rag.AskRequest{
// 		Question:         req.Question,
// 		TopK:             topK,
// 		SourceTypeFilter: "sop",
// 	})
// 	if err != nil {
// 		return nil, err
// 	}
// 	return toAskResponse(resp), nil
// }
//
// func (h *sopHandler) Ingest(_ context.Context, req *citesearchv1.SOPIngestRequest) (*citesearchv1.IngestResponse, error) {
// 	result, err := ingest.Run(h.cfg, "data/docs/sop", req.Overwrite, 0, 0, 0)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &citesearchv1.IngestResponse{
// 		Status:             result.Status,
// 		DocumentsProcessed: int32(result.DocumentsProcessed),
// 		ChunksIndexed:      int32(result.ChunksIndexed),
// 		Message:            result.Message,
// 	}, nil
// }
//
// func (h *sopHandler) List(_ context.Context, _ *citesearchv1.SOPListRequest) (*citesearchv1.SOPListResponse, error) {
// 	entries, err := h.search.ListSOPs()
// 	if err != nil {
// 		return nil, err
// 	}
// 	resp := &citesearchv1.SOPListResponse{Count: int32(len(entries))}
// 	for _, e := range entries {
// 		resp.Sops = append(resp.Sops, &citesearchv1.SOPEntry{
// 			SopNumber:  e.SOPNumber,
// 			Title:      e.Title,
// 			ChunkCount: int32(e.ChunkCount),
// 		})
// 	}
// 	return resp, nil
// }
