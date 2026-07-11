package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

type traceReader interface {
	GetTraceByID(id string) (*types.Trace, error)
}

type traceRequestsReader interface {
	traceReader
	ListRequestsByTraceID(traceID string) ([]types.Request, error)
}

func handleListTraces(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := chi.URLParam(r, "projectID")
		caller := UserFromContext(r.Context())
		callerMember := MemberFromContext(r.Context())
		p := parsePagination(r)

		var createdBy string
		if callerMember != nil && callerMember.Role == types.RoleDeveloper {
			createdBy = caller.ID
		}

		traces, total, err := st.ListTraces(projectID, p, createdBy)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to list traces")
			return
		}
		writeList(w, traces, total, p.Page, p.Limit())
	}
}

func handleGetTrace(st traceReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := chi.URLParam(r, "projectID")
		trace, err := st.GetTraceByID(chi.URLParam(r, "traceID"))
		if err != nil || trace == nil || !sameProjectID(trace.ProjectID, projectID) {
			writeError(w, http.StatusNotFound, "not_found", "trace not found")
			return
		}
		writeData(w, http.StatusOK, trace)
	}
}

func handleListTraceRequests(st traceRequestsReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := UserFromContext(r.Context())
		callerMember := MemberFromContext(r.Context())
		projectID := chi.URLParam(r, "projectID")
		traceID := chi.URLParam(r, "traceID")
		trace, err := st.GetTraceByID(traceID)
		if err != nil || trace == nil || !sameProjectID(trace.ProjectID, projectID) {
			writeError(w, http.StatusNotFound, "not_found", "trace not found")
			return
		}
		requests, err := st.ListRequestsByTraceID(traceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to list trace requests")
			return
		}

		isDeveloper := callerMember != nil && callerMember.Role == types.RoleDeveloper

		filtered := requests[:0]
		for i := range requests {
			// Developers can only see their own requests.
			if isDeveloper && requests[i].CreatedBy != caller.ID {
				continue
			}
			// Strip provider for non-superadmin users.
			if !caller.IsSuperadmin {
				requests[i].Provider = ""
			}
			filtered = append(filtered, requests[i])
		}

		writeData(w, http.StatusOK, filtered)
	}
}
