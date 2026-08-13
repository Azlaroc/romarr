package api

import (
	"fmt"
	"strings"

	"gamarr/internal/download"
	"gamarr/internal/models"
)

// discSetFromRequest validates and extracts a download request's disc-set
// fields. All four travel together: partial stamps are a caller bug that
// would strand a member outside its set, so they 400 rather than degrade.
func discSetFromRequest(req *models.DownloadRequest) (download.DiscSet, error) {
	if req.DiscSetID == "" && req.DiscIndex == 0 && req.DiscTotal == 0 && req.SetDir == "" {
		return download.DiscSet{}, nil
	}
	switch {
	case req.DiscSetID == "":
		return download.DiscSet{}, fmt.Errorf("disc_set_id is required with disc-set fields")
	case req.SetDir == "":
		return download.DiscSet{}, fmt.Errorf("set_dir is required with disc-set fields")
	case strings.ContainsAny(req.SetDir, `/\`) || req.SetDir == "." || req.SetDir == "..":
		return download.DiscSet{}, fmt.Errorf("set_dir must be a single path component")
	case req.DiscIndex < 1 || req.DiscTotal < 1:
		return download.DiscSet{}, fmt.Errorf("disc_index and disc_total must be >= 1")
	case req.DiscIndex > req.DiscTotal:
		return download.DiscSet{}, fmt.Errorf("disc_index %d exceeds disc_total %d", req.DiscIndex, req.DiscTotal)
	}
	return download.DiscSet{
		ID:    req.DiscSetID,
		Index: req.DiscIndex,
		Total: req.DiscTotal,
		Dir:   req.SetDir,
	}, nil
}
