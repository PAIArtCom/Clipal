package telemetry

import "testing"

func TestUsageExtractorJSON_OpenAI(t *testing.T) {
	extractor := NewUsageExtractor("openai", "openai_responses", "application/json")
	extractor.Append([]byte(`{"id":"r1","usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":5}}}`))
	usage, ok := extractor.Finalize()
	if !ok {
		t.Fatalf("expected usage")
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 34 || usage.TotalTokens != 46 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.ReasoningTokens != 5 {
		t.Fatalf("reasoning_tokens = %d", usage.ReasoningTokens)
	}
	if usage.Usage["input_tokens_details"] == nil {
		t.Fatalf("expected raw usage payload to be preserved")
	}
}

func TestUsageExtractorJSON_Claude(t *testing.T) {
	extractor := NewUsageExtractor("claude", "claude_messages", "application/json")
	extractor.Append([]byte(`{"type":"message","usage":{"input_tokens":8,"output_tokens":13,"cache_creation_input_tokens":5,"cache_read_input_tokens":21}}`))
	usage, ok := extractor.Finalize()
	if !ok {
		t.Fatalf("expected usage")
	}
	if usage.InputTokens != 34 || usage.OutputTokens != 13 || usage.TotalTokens != 47 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.Usage["cache_creation_input_tokens"] == nil {
		t.Fatalf("expected raw usage payload to be preserved")
	}
	if usage.Usage["cache_read_input_tokens"] == nil {
		t.Fatalf("expected raw usage payload to be preserved")
	}
}

func TestUsageExtractorJSON_Gemini(t *testing.T) {
	extractor := NewUsageExtractor("gemini", "gemini_generate_content", "application/json")
	extractor.Append([]byte(`{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":7,"totalTokenCount":12,"thoughtsTokenCount":2}}`))
	usage, ok := extractor.Finalize()
	if !ok {
		t.Fatalf("expected usage")
	}
	if usage.InputTokens != 5 || usage.OutputTokens != 7 || usage.TotalTokens != 12 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.ThoughtsTokens != 2 {
		t.Fatalf("thoughts_tokens = %d", usage.ThoughtsTokens)
	}
	if usage.Usage["thoughtsTokenCount"] == nil {
		t.Fatalf("expected raw usage payload to be preserved")
	}
}

func TestUsageExtractorSSEOpenAICompletedOnly(t *testing.T) {
	extractor := NewUsageExtractor("openai", "openai_responses", "text/event-stream")
	extractor.Append([]byte("event: response.output_text.delta\n"))
	extractor.Append([]byte("data: {\"type\":\"response.output_text.delta\"}\n\n"))
	extractor.Append([]byte("event: response.completed\n"))
	extractor.Append([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":15,\"total_tokens\":25,\"output_tokens_details\":{\"reasoning_tokens\":2}}}}\n\n"))

	usage, ok := extractor.Finalize()
	if !ok {
		t.Fatalf("expected usage")
	}
	if usage.TotalTokens != 25 || usage.OutputTokens != 15 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.ReasoningTokens != 2 {
		t.Fatalf("reasoning_tokens = %d", usage.ReasoningTokens)
	}
	if usage.Usage["output_tokens_details"] == nil {
		t.Fatalf("expected raw usage payload to be preserved")
	}
}

func TestUsageExtractorSSEOpenAIChatCompletionsUsageChunk(t *testing.T) {
	extractor := NewUsageExtractor("openai", "openai_chat_completions", "text/event-stream")
	extractor.Append([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
	extractor.Append([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":6,\"total_tokens\":16}}\n\n"))

	usage, ok := extractor.Finalize()
	if !ok {
		t.Fatalf("expected usage")
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 6 || usage.TotalTokens != 16 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestUsageExtractorSSEClaudeRequiresMessageStop(t *testing.T) {
	extractor := NewUsageExtractor("claude", "claude_messages", "text/event-stream")
	extractor.Append([]byte("event: message_start\n"))
	extractor.Append([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":3,\"cache_read_input_tokens\":4}}}\n\n"))
	extractor.Append([]byte("event: message_delta\n"))
	extractor.Append([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":6}}\n\n"))
	extractor.Append([]byte("event: message_stop\n"))
	extractor.Append([]byte("data: {\"type\":\"message_stop\"}\n\n"))

	usage, ok := extractor.Finalize()
	if !ok {
		t.Fatalf("expected usage")
	}
	if usage.InputTokens != 17 || usage.OutputTokens != 6 || usage.TotalTokens != 23 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.Usage["cache_creation_input_tokens"] == nil {
		t.Fatalf("expected merged raw usage payload to be preserved")
	}
	if usage.Usage["cache_read_input_tokens"] == nil {
		t.Fatalf("expected merged raw usage payload to be preserved")
	}
}

func TestUsageExtractorSSEGeminiUsesFinishedChunk(t *testing.T) {
	extractor := NewUsageExtractor("gemini", "gemini_stream_generate_content", "text/event-stream")
	extractor.Append([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}],\"usageMetadata\":{\"promptTokenCount\":99,\"totalTokenCount\":99}}\n\n"))
	extractor.Append([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"parts\":[{\"text\":\" world\"}]}}],\"usageMetadata\":{\"promptTokenCount\":17,\"candidatesTokenCount\":5,\"thoughtsTokenCount\":4,\"totalTokenCount\":26}}\n\n"))

	usage, ok := extractor.Finalize()
	if !ok {
		t.Fatalf("expected usage")
	}
	if usage.InputTokens != 17 || usage.OutputTokens != 5 || usage.TotalTokens != 26 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.ThoughtsTokens != 4 {
		t.Fatalf("thoughts_tokens = %d", usage.ThoughtsTokens)
	}
}
