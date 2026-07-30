package react

import (
	"context"

	"github.com/yclw/gag/graph"
	"github.com/yclw/gag/nodes"
)

const (
	graphID     string = "adk.react"
	userNodeID  string = "react.user"
	modelNodeID string = "react.model"
	toolNodeID  string = "react.tool"
)

func New(m nodes.Model, systemPrompt string, tools ...nodes.Tool) (*graph.Graph, error) {
	userNode, err := nodes.NewUserMessageNode(userNodeID)
	if err != nil {
		return nil, err
	}

	modelNode, err := nodes.NewModelNode(modelNodeID, m)
	if err != nil {
		return nil, err
	}
	modelNode.SystemPrompt = systemPrompt
	for _, tool := range tools {
		modelNode.Tools = append(modelNode.Tools, tool.Definition())
	}

	toolNode, err := nodes.NewToolNode(toolNodeID, tools...)
	if err != nil {
		return nil, err
	}

	g, err := graph.NewBuilder(graphID, userNode, modelNode, toolNode).
		StartNode(userNodeID).
		Link(userNodeID, modelNodeID).
		Route(modelNodeID, func(ctx context.Context, events []graph.Event) (string, error) {
			calls, err := nodes.UnfinishedToolCalls(events)
			if err != nil {
				return "", err
			}
			if len(calls) > 0 {
				return toolNodeID, nil
			}
			return userNodeID, nil
		}).
		Link(toolNodeID, modelNodeID).
		Build()
	if err != nil {
		return nil, err
	}
	return g, nil
}
