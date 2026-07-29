// Package openai implements nodes.Model with OpenAI's Chat Completions API.
package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/yclw/gag/nodes"

	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// Config configures an OpenAI-compatible Chat Completions endpoint.
type Config struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

// Model adapts OpenAI's streaming Chat Completions API to nodes.Model.
type Model struct {
	name   string
	client sdk.Client
}

var _ nodes.Model = (*Model)(nil)

// New creates an OpenAI model.
func New(config Config) (*Model, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("openai: API key is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("openai: model is required")
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}

	return &Model{
		name: config.Model,
		client: sdk.NewClient(
			option.WithAPIKey(config.APIKey),
			option.WithBaseURL(strings.TrimRight(config.BaseURL, "/")),
			option.WithHTTPClient(config.HTTPClient),
		),
	}, nil
}

// Generate sends input to OpenAI and emits each streamed response chunk.
func (m *Model) Generate(
	ctx context.Context,
	input nodes.ModelInput,
	emit func(nodes.ModelChunk) error,
) (nodes.ModelMessage, error) {
	params, err := requestParams(m.name, input)
	if err != nil {
		return nodes.ModelMessage{}, err
	}

	stream := m.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var accumulated sdk.ChatCompletionAccumulator
	for stream.Next() {
		chunk := stream.Current()
		if !accumulated.AddChunk(chunk) {
			return nodes.ModelMessage{}, errors.New("openai: inconsistent stream chunk")
		}
		if chunk.JSON.Usage.Valid() {
			// The SDK accumulator sums total counts but does not retain token details.
			accumulated.Usage = chunk.Usage
		}

		modelChunk, ok := responseChunk(chunk, accumulated.ChatCompletion)
		if ok && emit != nil {
			if err := emit(modelChunk); err != nil {
				return nodes.ModelMessage{}, err
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nodes.ModelMessage{}, fmt.Errorf("openai: read stream: %w", err)
	}
	if len(accumulated.Choices) == 0 {
		return nodes.ModelMessage{}, errors.New("openai: response contains no choices")
	}

	return responseMessage(accumulated.ChatCompletion), nil
}

func requestParams(model string, input nodes.ModelInput) (sdk.ChatCompletionNewParams, error) {
	messages, err := requestMessages(input.Messages)
	if err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}
	tools, err := requestTools(input.Tools)
	if err != nil {
		return sdk.ChatCompletionNewParams{}, err
	}

	return sdk.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: messages,
		Tools:    tools,
		StreamOptions: sdk.ChatCompletionStreamOptionsParam{
			IncludeUsage: sdk.Bool(true),
		},
	}, nil
}

func requestMessages(messages []nodes.ModelMessage) ([]sdk.ChatCompletionMessageParamUnion, error) {
	result := make([]sdk.ChatCompletionMessageParamUnion, 0, len(messages))
	for i, message := range messages {
		var converted sdk.ChatCompletionMessageParamUnion
		var err error

		switch message.Role {
		case nodes.SystemRole:
			converted, err = systemMessage(message)
		case nodes.UserRole:
			converted, err = userMessage(message)
		case nodes.AssistantRole:
			converted, err = assistantMessage(message)
		case nodes.ToolRole:
			converted, err = toolMessage(message)
		default:
			err = fmt.Errorf("unsupported role %q", message.Role)
		}
		if err != nil {
			return nil, fmt.Errorf("openai: convert message %d: %w", i, err)
		}
		result = append(result, converted)
	}
	return result, nil
}

func systemMessage(message nodes.ModelMessage) (sdk.ChatCompletionMessageParamUnion, error) {
	content, err := textContent(message.Content)
	if err != nil {
		return sdk.ChatCompletionMessageParamUnion{}, err
	}
	result := sdk.SystemMessage(content)
	if message.Name != "" {
		result.OfSystem.Name = sdk.String(message.Name)
	}
	return result, nil
}

func userMessage(message nodes.ModelMessage) (sdk.ChatCompletionMessageParamUnion, error) {
	if len(message.Content) == 0 {
		result := sdk.UserMessage("")
		if message.Name != "" {
			result.OfUser.Name = sdk.String(message.Name)
		}
		return result, nil
	}

	content := make([]sdk.ChatCompletionContentPartUnionParam, 0, len(message.Content))
	for i, part := range message.Content {
		converted, ok, err := userContentPart(part)
		if err != nil {
			return sdk.ChatCompletionMessageParamUnion{}, fmt.Errorf("convert content part %d: %w", i, err)
		}
		if ok {
			content = append(content, converted)
		}
	}

	result := sdk.UserMessage(content)
	if message.Name != "" {
		result.OfUser.Name = sdk.String(message.Name)
	}
	return result, nil
}

func assistantMessage(message nodes.ModelMessage) (sdk.ChatCompletionMessageParamUnion, error) {
	content, err := textContent(message.Content)
	if err != nil {
		return sdk.ChatCompletionMessageParamUnion{}, err
	}
	result := sdk.AssistantMessage(content)
	if message.Name != "" {
		result.OfAssistant.Name = sdk.String(message.Name)
	}
	if len(message.ToolCalls) > 0 {
		result.OfAssistant.ToolCalls = make([]sdk.ChatCompletionMessageToolCallParam, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			result.OfAssistant.ToolCalls = append(result.OfAssistant.ToolCalls, sdk.ChatCompletionMessageToolCallParam{
				ID: call.ID,
				Function: sdk.ChatCompletionMessageToolCallFunctionParam{
					Name:      call.ToolName,
					Arguments: string(call.Arguments),
				},
			})
		}
	}
	return result, nil
}

func toolMessage(message nodes.ModelMessage) (sdk.ChatCompletionMessageParamUnion, error) {
	content, err := textContent(message.Content)
	if err != nil {
		return sdk.ChatCompletionMessageParamUnion{}, err
	}
	return sdk.ToolMessage(content, message.ToolCallID), nil
}

func textContent(parts []nodes.ContentPart) (string, error) {
	var content strings.Builder
	for _, part := range parts {
		switch part.Type {
		case nodes.TextContentType:
			content.WriteString(part.Text)
		case nodes.JSONContentType:
			content.Write(part.JSON)
		case nodes.ReasoningContentType:
			// Reasoning is provider-owned output and is not replayed as visible text.
		default:
			return "", fmt.Errorf("content type %q is not supported for this role", part.Type)
		}
	}
	return content.String(), nil
}

func userContentPart(part nodes.ContentPart) (sdk.ChatCompletionContentPartUnionParam, bool, error) {
	switch part.Type {
	case nodes.TextContentType:
		return sdk.TextContentPart(part.Text), true, nil
	case nodes.JSONContentType:
		return sdk.TextContentPart(string(part.JSON)), true, nil
	case nodes.ImageContentType:
		converted, err := imageContentPart(part.Media)
		return converted, true, err
	case nodes.AudioContentType:
		converted, err := audioContentPart(part.Media)
		return converted, true, err
	case nodes.FileContentType:
		converted, err := fileContentPart(part.Media)
		return converted, true, err
	case nodes.ReasoningContentType:
		return sdk.ChatCompletionContentPartUnionParam{}, false, nil
	default:
		return sdk.ChatCompletionContentPartUnionParam{}, false, fmt.Errorf("content type %q is not supported", part.Type)
	}
}

func imageContentPart(media *nodes.Media) (sdk.ChatCompletionContentPartUnionParam, error) {
	if media == nil {
		return sdk.ChatCompletionContentPartUnionParam{}, errors.New("image media is required")
	}
	url := media.URL
	if url == "" && len(media.Data) > 0 {
		if media.MIMEType == "" {
			return sdk.ChatCompletionContentPartUnionParam{}, errors.New("image MIME type is required for inline data")
		}
		url = dataURL(media.MIMEType, media.Data)
	}
	if url == "" {
		return sdk.ChatCompletionContentPartUnionParam{}, errors.New("image URL or data is required")
	}
	return sdk.ImageContentPart(sdk.ChatCompletionContentPartImageImageURLParam{
		URL:    url,
		Detail: media.Detail,
	}), nil
}

func audioContentPart(media *nodes.Media) (sdk.ChatCompletionContentPartUnionParam, error) {
	if media == nil || len(media.Data) == 0 {
		return sdk.ChatCompletionContentPartUnionParam{}, errors.New("audio data is required")
	}
	format := audioFormat(media)
	if format != "wav" && format != "mp3" {
		return sdk.ChatCompletionContentPartUnionParam{}, fmt.Errorf("unsupported audio format %q", format)
	}
	return sdk.InputAudioContentPart(sdk.ChatCompletionContentPartInputAudioInputAudioParam{
		Data:   base64.StdEncoding.EncodeToString(media.Data),
		Format: format,
	}), nil
}

func audioFormat(media *nodes.Media) string {
	switch strings.ToLower(media.MIMEType) {
	case "audio/wav", "audio/wave", "audio/x-wav":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	}
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(media.Name)), ".")
}

func fileContentPart(media *nodes.Media) (sdk.ChatCompletionContentPartUnionParam, error) {
	if media == nil {
		return sdk.ChatCompletionContentPartUnionParam{}, errors.New("file media is required")
	}
	file := sdk.ChatCompletionContentPartFileFileParam{}
	switch {
	case media.ID != "":
		file.FileID = sdk.String(media.ID)
	case len(media.Data) > 0:
		mimeType := media.MIMEType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		file.FileData = sdk.String(dataURL(mimeType, media.Data))
		if media.Name != "" {
			file.Filename = sdk.String(media.Name)
		}
	default:
		return sdk.ChatCompletionContentPartUnionParam{}, errors.New("file ID or data is required")
	}
	return sdk.FileContentPart(file), nil
}

func dataURL(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func requestTools(definitions []nodes.ToolDefinition) ([]sdk.ChatCompletionToolParam, error) {
	tools := make([]sdk.ChatCompletionToolParam, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Type != "" && definition.Type != "function" {
			return nil, fmt.Errorf("openai: unsupported tool type %q", definition.Type)
		}
		parameters, err := schemaParameters(definition)
		if err != nil {
			return nil, fmt.Errorf("openai: encode schema for tool %q: %w", definition.Name, err)
		}
		tools = append(tools, sdk.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        definition.Name,
				Description: sdk.String(definition.Description),
				Parameters:  parameters,
			},
		})
	}
	return tools, nil
}

func schemaParameters(definition nodes.ToolDefinition) (shared.FunctionParameters, error) {
	if definition.InputSchema == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(definition.InputSchema)
	if err != nil {
		return nil, err
	}
	var parameters shared.FunctionParameters
	if err := json.Unmarshal(encoded, &parameters); err != nil {
		return nil, err
	}
	return parameters, nil
}

func responseChunk(
	chunk sdk.ChatCompletionChunk,
	accumulated sdk.ChatCompletion,
) (nodes.ModelChunk, bool) {
	result := nodes.ModelChunk{Usage: modelUsage(chunk.Usage)}
	hasUsage := chunk.JSON.Usage.Valid()
	if len(chunk.Choices) == 0 {
		return result, hasUsage
	}

	choice := chunk.Choices[0]
	result.StopReason = choice.FinishReason
	result.Message = nodes.ModelMessage{Role: nodes.AssistantRole}
	if choice.Delta.Content != "" {
		result.Message.Content = append(result.Message.Content, nodes.ContentPart{
			Type: nodes.TextContentType,
			Text: choice.Delta.Content,
		})
	}
	if choice.Delta.Refusal != "" {
		result.Message.Content = append(result.Message.Content, nodes.ContentPart{
			Type: nodes.TextContentType,
			Text: choice.Delta.Refusal,
		})
	}
	if choice.FinishReason != "" && len(accumulated.Choices) > 0 {
		result.Message.ToolCalls = responseToolCalls(accumulated.Choices[0].Message.ToolCalls)
	}

	return result, len(result.Message.Content) > 0 ||
		len(result.Message.ToolCalls) > 0 ||
		result.StopReason != "" ||
		hasUsage
}

func responseMessage(completion sdk.ChatCompletion) nodes.ModelMessage {
	choice := completion.Choices[0]
	message := nodes.ModelMessage{
		Role:       nodes.AssistantRole,
		StopReason: choice.FinishReason,
		ToolCalls:  responseToolCalls(choice.Message.ToolCalls),
		Usage:      modelUsage(completion.Usage),
	}
	if choice.Message.Content != "" {
		message.Content = append(message.Content, nodes.ContentPart{
			Type: nodes.TextContentType,
			Text: choice.Message.Content,
		})
	}
	if choice.Message.Refusal != "" {
		message.Content = append(message.Content, nodes.ContentPart{
			Type: nodes.TextContentType,
			Text: choice.Message.Refusal,
		})
	}
	return message
}

func responseToolCalls(toolCalls []sdk.ChatCompletionMessageToolCall) []nodes.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	result := make([]nodes.ToolCall, 0, len(toolCalls))
	for i, call := range toolCalls {
		index := i
		result = append(result, nodes.ToolCall{
			ID:        call.ID,
			ToolName:  call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
			Index:     &index,
			Type:      string(call.Type),
		})
	}
	return result
}

func modelUsage(usage sdk.CompletionUsage) nodes.ModelUsage {
	return nodes.ModelUsage{
		InputTokens:       int(usage.PromptTokens),
		OutputTokens:      int(usage.CompletionTokens),
		TotalTokens:       int(usage.TotalTokens),
		CachedInputTokens: int(usage.PromptTokensDetails.CachedTokens),
		ReasoningTokens:   int(usage.CompletionTokensDetails.ReasoningTokens),
	}
}
