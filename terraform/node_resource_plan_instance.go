package terraform

import (
	"fmt"

	"github.com/hashicorp/terraform/plans"
	"github.com/hashicorp/terraform/providers"
	"github.com/hashicorp/terraform/states"

	"github.com/hashicorp/terraform/addrs"
)

// NodePlannableResourceInstance represents a _single_ resource
// instance that is plannable. This means this represents a single
// count index, for example.
type NodePlannableResourceInstance struct {
	*NodeAbstractResourceInstance
	ForceCreateBeforeDestroy bool
	skipRefresh              bool
}

var (
	_ GraphNodeModuleInstance       = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeReferenceable        = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeReferencer           = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeConfigResource       = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeResourceInstance     = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeAttachResourceConfig = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeAttachResourceState  = (*NodePlannableResourceInstance)(nil)
	_ GraphNodeEvalable             = (*NodePlannableResourceInstance)(nil)
)

// GraphNodeEvalable
func (n *NodePlannableResourceInstance) EvalTree() EvalNode {
	addr := n.ResourceInstanceAddr()

	// Eval info is different depending on what kind of resource this is
	switch addr.Resource.Resource.Mode {
	case addrs.ManagedResourceMode:
		return n.evalTreeManagedResource(addr)
	case addrs.DataResourceMode:
		return n.evalTreeDataResource(addr)
	default:
		panic(fmt.Errorf("unsupported resource mode %s", n.Config.Mode))
	}
}

func (n *NodePlannableResourceInstance) evalTreeDataResource(addr addrs.AbsResourceInstance) EvalNode {
	config := n.Config
	var provider providers.Interface
	var providerSchema *ProviderSchema
	var change *plans.ResourceInstanceChange
	var state *states.ResourceInstanceObject

	return &EvalSequence{
		Nodes: []EvalNode{
			&EvalGetProvider{
				Addr:   n.ResolvedProvider,
				Output: &provider,
				Schema: &providerSchema,
			},

			&EvalReadState{
				Addr:           addr.Resource,
				Provider:       &provider,
				ProviderSchema: &providerSchema,
				Output:         &state,
			},

			&EvalValidateSelfRef{
				Addr:           addr.Resource,
				Config:         config.Config,
				ProviderSchema: &providerSchema,
			},

			&evalReadDataPlan{
				evalReadData: evalReadData{
					Addr:           addr.Resource,
					Config:         n.Config,
					Provider:       &provider,
					ProviderAddr:   n.ResolvedProvider,
					ProviderMetas:  n.ProviderMetas,
					ProviderSchema: &providerSchema,
					OutputChange:   &change,
					State:          &state,
					dependsOn:      n.dependsOn,
				},
			},

			// write the data source into both the refresh state and the
			// working state
			&EvalWriteState{
				Addr:           addr.Resource,
				ProviderAddr:   n.ResolvedProvider,
				ProviderSchema: &providerSchema,
				State:          &state,
				targetState:    refreshState,
			},

			&EvalWriteState{
				Addr:           addr.Resource,
				ProviderAddr:   n.ResolvedProvider,
				ProviderSchema: &providerSchema,
				State:          &state,
			},

			&EvalWriteDiff{
				Addr:           addr.Resource,
				ProviderSchema: &providerSchema,
				Change:         &change,
			},
		},
	}
}

func (n *NodePlannableResourceInstance) evalTreeManagedResource(addr addrs.AbsResourceInstance) EvalNode {
	config := n.Config
	var provider providers.Interface
	var providerSchema *ProviderSchema
	var change *plans.ResourceInstanceChange
	var instanceRefreshState *states.ResourceInstanceObject
	var instancePlanState *states.ResourceInstanceObject

	return &EvalSequence{
		Nodes: []EvalNode{
			&EvalGetProvider{
				Addr:   n.ResolvedProvider,
				Output: &provider,
				Schema: &providerSchema,
			},

			&EvalValidateSelfRef{
				Addr:           addr.Resource,
				Config:         config.Config,
				ProviderSchema: &providerSchema,
			},

			&EvalIf{
				If: func(ctx EvalContext) (bool, error) {
					return !n.skipRefresh, nil
				},
				Then: &EvalSequence{
					Nodes: []EvalNode{
						// Refresh the instance
						&EvalReadState{
							Addr:           addr.Resource,
							Provider:       &provider,
							ProviderSchema: &providerSchema,
							Output:         &instanceRefreshState,
						},
						&EvalRefreshLifecycle{
							Config:                   n.Config,
							State:                    &instanceRefreshState,
							ForceCreateBeforeDestroy: n.ForceCreateBeforeDestroy,
						},
						&EvalRefresh{
							Addr:           addr.Resource,
							ProviderAddr:   n.ResolvedProvider,
							Provider:       &provider,
							ProviderMetas:  n.ProviderMetas,
							ProviderSchema: &providerSchema,
							State:          &instanceRefreshState,
							Output:         &instanceRefreshState,
						},
						&EvalWriteState{
							Addr:           addr.Resource,
							ProviderAddr:   n.ResolvedProvider,
							State:          &instanceRefreshState,
							ProviderSchema: &providerSchema,
							targetState:    refreshState,
							Dependencies:   &n.Dependencies,
						},
					},
				},
			},

			// Plan the instance
			&EvalDiff{
				Addr:                addr.Resource,
				Config:              n.Config,
				CreateBeforeDestroy: n.ForceCreateBeforeDestroy,
				Provider:            &provider,
				ProviderAddr:        n.ResolvedProvider,
				ProviderMetas:       n.ProviderMetas,
				ProviderSchema:      &providerSchema,
				State:               &instanceRefreshState,
				OutputChange:        &change,
				OutputState:         &instancePlanState,
			},
			&EvalCheckPreventDestroy{
				Addr:   addr.Resource,
				Config: n.Config,
				Change: &change,
			},
			&EvalWriteState{
				Addr:           addr.Resource,
				ProviderAddr:   n.ResolvedProvider,
				State:          &instancePlanState,
				ProviderSchema: &providerSchema,
			},
			&EvalWriteDiff{
				Addr:           addr.Resource,
				ProviderSchema: &providerSchema,
				Change:         &change,
			},
		},
	}
}
