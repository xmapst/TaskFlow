package iproto

import "github.com/xmapst/xdag"

// DAGDependencies converts a step DAG definition to xdag dependencies.
func DAGDependencies(routing *SStepRouting) xdag.DependencySpec {
	var deps xdag.DependencySpec
	if routing == nil {
		return deps
	}
	if routing.Join == "any" {
		deps.Join = xdag.AnyOf
	}
	for _, edge := range routing.Edges {
		deps.Edges = append(deps.Edges, xdag.DependencyEdge{
			Task: edge.Step,
			On:   xdag.Trigger(edge.Trigger),
		})
	}
	return deps
}
