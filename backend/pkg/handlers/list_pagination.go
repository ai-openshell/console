package handlers

import (
	"net/http"
	"strconv"

	openshell "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"

	"github.com/ai-openshell/console/backend/pkg/apiutils"
)

// listRequestOptions keeps the existing array response unless pageSize was
// supplied. A paged request forwards the gateway's opaque continuation token.
func listRequestOptions(w http.ResponseWriter, r *http.Request) (openshell.ListOptions, bool, bool) {
	query := r.URL.Query()
	opts := openshell.ListOptions{LabelSelector: query.Get("labelSelector")}
	sizes, paged := query["pageSize"]
	if !paged {
		if _, hasToken := query["pageToken"]; hasToken {
			apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidArgument, "pageToken requires pageSize")
			return opts, false, false
		}
		return opts, false, true
	}
	if len(sizes) != 1 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidArgument, "pageSize must be a single integer from 1 to 100")
		return opts, false, false
	}
	size, err := strconv.Atoi(sizes[0])
	if err != nil || size < 1 || size > 100 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidArgument, "pageSize must be an integer from 1 to 100")
		return opts, false, false
	}
	if len(query["pageToken"]) > 1 {
		apiutils.WriteError(w, http.StatusBadRequest, apiutils.InvalidArgument, "pageToken must appear once")
		return opts, false, false
	}
	opts.PageSize = size
	opts.PageToken = query.Get("pageToken")
	return opts, true, true
}

type listPageResponse[T any] struct {
	NextPageToken string `json:"nextPageToken"`
	Items         []T    `json:"items"`
}
