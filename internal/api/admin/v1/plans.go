package adminv1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/types"
)

type planReadStore interface {
	ListPlansPaginated(types.PaginationParams) ([]types.Plan, int, error)
	GetPlanByID(string) (*types.Plan, error)
}

type listPlansInput struct {
	Page    int    `query:"page" default:"1" minimum:"1" doc:"Page number, starting at one."`
	PerPage int    `query:"per_page" default:"20" minimum:"1" maximum:"100" doc:"Number of plans returned per page."`
	Sort    string `query:"sort" default:"created_at" doc:"Plan field used for ordering."`
	Order   string `query:"order" default:"desc" enum:"asc,desc" doc:"Sort direction."`
}

func (input *listPlansInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

type listPlansOutput struct {
	Body ListResponse[types.Plan]
}

type planInput struct {
	// Legacy GetPlan maps malformed and missing IDs to the same 404.
	PlanID string `path:"planID" doc:"Plan identifier."`
}

type planOutput struct {
	Body DataResponse[types.Plan]
}

func registerPlanReadOperations(api huma.API, server *Server) {
	access := authz.System(authz.PermissionSystemPlansRead)
	listOperation := contract.Operation{
		ID:            "listPlans",
		Method:        http.MethodGet,
		Path:          "/api/v1/plans",
		Summary:       "List plans",
		Tags:          []string{"Plans"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden},
		Access:        access,
		Authorize:     server.authorizationMiddleware,
	}
	registerWithLegacyTrailingSlash(api, listOperation, server.listPlans)

	getOperation := contract.Operation{
		ID:            "getPlan",
		Method:        http.MethodGet,
		Path:          "/api/v1/plans/{planID}",
		Summary:       "Get plan",
		Tags:          []string{"Plans"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
		Access:        access,
		Authorize:     server.authorizationMiddleware,
	}
	registerWithLegacyTrailingSlash(api, getOperation, server.getPlan)
}

func (s *Server) listPlans(_ context.Context, input *listPlansInput) (*listPlansOutput, error) {
	if s == nil || s.Plans == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "plan management store is not configured", nil)
	}
	pagination := input.pagination()
	plans, total, err := s.Plans.ListPlansPaginated(pagination)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list plans", nil)
	}
	if plans == nil {
		plans = []types.Plan{}
	}
	return &listPlansOutput{Body: ListResponse[types.Plan]{
		Data: plans,
		Meta: paginationMeta(total, pagination),
	}}, nil
}

func (s *Server) getPlan(_ context.Context, input *planInput) (*planOutput, error) {
	if s == nil || s.Plans == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "plan management store is not configured", nil)
	}
	plan, err := s.Plans.GetPlanByID(input.PlanID)
	if err != nil || plan == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "plan not found", nil)
	}
	return &planOutput{Body: DataResponse[types.Plan]{Data: *plan}}, nil
}
