package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// Mock server that simulates the Anthropic Messages API.
// Run this server, then set ANTHROPIC_BASE_URL=http://localhost:9090 to redirect SDK calls here.

func main() {
	http.HandleFunc("/v1/messages", handleMessages)

	addr := ":9090"
	fmt.Println("Mock Anthropic API server listening on", addr)
	fmt.Println("Set environment variables before running main.go:")
	fmt.Println("  export ANTHROPIC_BASE_URL=http://localhost:9090")
	fmt.Println("  export ANTHROPIC_API_KEY=mock-key")
	log.Fatal(http.ListenAndServe(addr, nil))
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	messages, _ := req["messages"].([]any)
	round := detectRound(messages)

	log.Printf("[mock] received request, detected round=%d, messages=%d", round, len(messages))

	var resp map[string]any
	switch round {
	case 1:
		// Round 1: model calls get_coordinates + get_temperature_unit in parallel
		resp = makeToolUseResponse(
			toolUseBlock("toolu_coord_01", "get_coordinates", map[string]any{
				"location": "San Francisco, CA",
			}),
			toolUseBlock("toolu_unit_01", "get_temperature_unit", map[string]any{
				"country": "US",
			}),
		)
	case 2:
		// Round 2: model calls get_weather using results from round 1
		resp = makeToolUseResponse(
			toolUseBlock("toolu_weather_01", "get_weather", map[string]any{
				"lat":  37.7749,
				"long": -122.4194,
				"unit": "fahrenheit",
			}),
		)
	default:
		// Round 3+: model returns final text answer
		resp = makeTextResponse(
			"Based on the data I retrieved, the current temperature in San Francisco, CA " +
				"is **122°F**. (Note: this is mock data for debugging purposes.)",
		)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// detectRound inspects the messages to figure out which round of the tool-use loop we're in.
//   - No tool_result in messages → round 1
//   - Has tool_result for get_coordinates but not get_weather → round 2
//   - Has tool_result for get_weather → round 3 (final text)
func detectRound(messages []any) int {
	hasCoordResult := false
	hasWeatherResult := false

	for _, m := range messages {
		msg, _ := m.(map[string]any)
		role, _ := msg["role"].(string)
		if role != "user" {
			continue
		}
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, block := range content {
			b, _ := block.(map[string]any)
			blockType, _ := b["type"].(string)
			if blockType != "tool_result" {
				continue
			}
			toolUseID, _ := b["tool_use_id"].(string)
			if strings.Contains(toolUseID, "coord") {
				hasCoordResult = true
			}
			if strings.Contains(toolUseID, "weather") {
				hasWeatherResult = true
			}
		}
	}

	if hasWeatherResult {
		return 3
	}
	if hasCoordResult {
		return 2
	}
	return 1
}

func makeTextResponse(text string) map[string]any {
	return map[string]any{
		"id":   "msg_mock_001",
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{
				"type": "text",
				"text": text,
			},
		},
		"model":         "claude-sonnet-4-5-20250929",
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  100,
			"output_tokens": 50,
		},
	}
}

func makeToolUseResponse(blocks ...map[string]any) map[string]any {
	return map[string]any{
		"id":            "msg_mock_001",
		"type":          "message",
		"role":          "assistant",
		"content":       blocks,
		"model":         "claude-sonnet-4-5-20250929",
		"stop_reason":   "tool_use",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  100,
			"output_tokens": 50,
		},
	}
}

func toolUseBlock(id, name string, input map[string]any) map[string]any {
	return map[string]any{
		"type":  "tool_use",
		"id":    id,
		"name":  name,
		"input": input,
	}
}
